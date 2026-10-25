package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/ai"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/store"
)

type outageAI struct {
	calls int
	err   error
	empty bool
}

func (p *outageAI) Pick(_ context.Context, req ai.Request) ([]ai.Pick, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	if p.empty {
		return nil, nil
	}
	return []ai.Pick{{ID: req.Candidates[0].ID, Reason: "AI choice"}}, nil
}

func outageChannel(t *testing.T, horizon time.Duration) (*Scheduler, *store.Channel, *outageAI) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.UpsertLibrary(ctx, store.Library{ID: "movies", Title: "Movies", Type: "movie"}); err != nil {
		t.Fatal(err)
	}
	var catalog []*store.Media
	for i := range 50 {
		catalog = append(catalog, &store.Media{RatingKey: fmt.Sprint(i), LibraryID: "movies",
			Type: "movie", Title: fmt.Sprintf("Movie %d", i), DurationMs: (2 * time.Hour).Milliseconds()})
	}
	if err := st.UpsertMedia(ctx, catalog, 1); err != nil {
		t.Fatal(err)
	}
	ch := &store.Channel{Number: 1, Name: "AI", Kind: KindAI, Enabled: true,
		Config: json.RawMessage(`{"source":{"type":"ai","ai":{"theme":"Movies"}},"noRepeatHours":48}`)}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	p := &outageAI{err: errors.New("provider overloaded")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, library.New(st, nil, log), func(context.Context) (ai.Provider, error) { return p, nil }, horizon, log)
	return s, ch, p
}

func TestAIOutageAddsAtMostOneMovieBelowHalfHorizon(t *testing.T) {
	for _, tc := range []struct {
		name               string
		horizon, remaining time.Duration
		wantRemaining      time.Duration
	}{
		{"enough programme", 48 * time.Hour, 30 * time.Hour, 30 * time.Hour},
		{"one whole fallback movie", 48 * time.Hour, 23 * time.Hour, 25 * time.Hour},
		{"new channel", 48 * time.Hour, 0, 2 * time.Hour},
		{"short guide", 48 * time.Hour, 4 * time.Hour, 6 * time.Hour},
		{"odd horizon", 5 * time.Hour, time.Hour, 3 * time.Hour},
		{"odd horizon new channel", 5 * time.Hour, 0, 2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ch, provider := outageChannel(t, tc.horizon)
			ctx := context.Background()
			now := time.Now()
			if tc.remaining > 0 {
				if err := s.store.InsertPrograms(ctx, []store.Program{{ChannelID: ch.ID,
					Start: now.Add(-time.Hour).UnixMilli(), Stop: now.Add(tc.remaining).UnixMilli(),
					RatingKey: "0", Note: "already announced", SourceID: "main"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Ensure(ctx, ch.ID); err == nil {
				t.Fatal("AI failure should remain visible")
			}
			if provider.calls != 1 {
				t.Fatalf("got %d requests in one run, want 1", provider.calls)
			}
			last, err := s.store.LastProgram(ctx, ch.ID)
			if err != nil {
				t.Fatal(err)
			}
			if delta := last.StopTime().Sub(now.Add(tc.wantRemaining)); delta < -time.Second || delta > time.Second {
				t.Fatalf("guide ends at %s, want approximately %s", last.StopTime(), now.Add(tc.wantRemaining))
			}
			if tc.remaining > 0 {
				current, err := s.store.ProgramAt(ctx, ch.ID, now)
				if err != nil || current.Note != "already announced" {
					t.Fatalf("existing programme changed: %+v, %v", current, err)
				}
			}
		})
	}
}

func TestAIOutageRetriesNextRunAndRecovers(t *testing.T) {
	s, ch, provider := outageChannel(t, 48*time.Hour)
	ctx := context.Background()
	// Old installations may still have a persistent 30-minute backoff. It must
	// not suppress the new policy's next-run retry.
	if err := s.store.SaveChannelState(ctx, ch.ID, json.RawMessage(fmt.Sprintf(
		`{"sources":{"main":{"aiBackoff":%d}}}`, time.Now().Add(time.Hour).Unix()))); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := s.Ensure(ctx, ch.ID); err == nil {
			t.Fatal("expected AI failure")
		}
		if provider.calls != attempt {
			t.Fatalf("run %d: got %d requests", attempt, provider.calls)
		}
		progs, err := s.store.ProgramsBetween(ctx, ch.ID, time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour))
		if err != nil || len(progs) != attempt {
			t.Fatalf("run %d: expected exactly one additional fallback movie, got %d programmes, %v", attempt, len(progs), err)
		}
	}
	before, err := s.store.ProgramsBetween(ctx, ch.ID, time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	provider.err = nil
	started := time.Now()
	if err := s.Ensure(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	last, err := s.store.LastProgram(ctx, ch.ID)
	if err != nil || last.StopTime().Before(started.Add(48*time.Hour)) || last.Note != "AI choice" {
		t.Fatalf("AI did not fill the full horizon: %+v, %v", last, err)
	}
	if s.LastError(ch.ID) != nil {
		t.Fatal("successful recovery should clear the displayed failure")
	}
	after, err := s.store.ProgramsBetween(ctx, ch.ID, time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range before {
		if after[i] != p {
			t.Fatal("recovery changed previously announced programme")
		}
	}
}

func TestAIOutageKeepsQueuedChoices(t *testing.T) {
	s, ch, provider := outageChannel(t, 48*time.Hour)
	ctx := context.Background()
	cfg, err := ParseConfig(ch.Config)
	if err != nil {
		t.Fatal(err)
	}
	state := &State{Sources: map[string]*SourceState{"main": {
		AIConfig: aiFingerprint(cfg.Source.AI), Queue: []QueuedPick{
			{Key: "1", Reason: "first AI choice"}, {Key: "2", Reason: "second AI choice"},
		},
	}}}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveChannelState(ctx, ch.ID, raw); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := s.Ensure(ctx, ch.ID); err == nil {
		t.Fatal("expected failure after consuming saved AI choices")
	}
	progs, err := s.store.ProgramsBetween(ctx, ch.ID, started.Add(-time.Second), started.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) != 3 || progs[0].RatingKey != "1" || progs[0].Note != "first AI choice" ||
		progs[1].RatingKey != "2" || progs[1].Note != "second AI choice" || provider.calls != 1 {
		t.Fatalf("queued choices or fallback coverage lost: %+v; requests %d", progs, provider.calls)
	}
}

func TestAIOutageHalfHorizonBoundary(t *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			s, ch, provider := outageChannel(t, 48*time.Hour)
			ctx := context.Background()
			cfg, err := ParseConfig(ch.Config)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := s.lib.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			cutoff := time.Now().Add(24 * time.Hour)
			g := &generator{s: s, ctx: ctx, ch: ch, cfg: cfg, catalog: catalog,
				aiFallbackUntil: cutoff, rng: rand.New(rand.NewSource(1)), lastAired: map[string]time.Time{}}
			g.index()
			ss := &SourceState{AIConfig: aiFingerprint(cfg.Source.AI)}
			item, _, err := g.pickAI("main", ss, &cfg.Source, cutoff.Add(offset))
			if offset < 0 {
				if err != nil || item == nil {
					t.Fatalf("below half horizon: expected fallback, got %+v, %v", item, err)
				}
			} else if err == nil || item != nil {
				t.Fatalf("at or above half horizon: expected planning to stop, got %+v, %v", item, err)
			}
			if provider.calls != 1 {
				t.Fatalf("expected one AI attempt before checking fallback, got %d", provider.calls)
			}
		})
	}
}

func TestAIEmptyResponseUsesOutagePolicy(t *testing.T) {
	s, ch, provider := outageChannel(t, 5*time.Hour)
	provider.err, provider.empty = nil, true
	started := time.Now()
	if err := s.Ensure(context.Background(), ch.ID); err == nil {
		t.Fatal("an empty response must count as a failed request")
	}
	last, err := s.store.LastProgram(context.Background(), ch.ID)
	if err != nil || last.StopTime().Before(started.Add(2*time.Hour-time.Second)) || last.StopTime().After(started.Add(2*time.Hour+time.Second)) {
		t.Fatalf("empty response did not limit random coverage to one whole movie: %+v, %v", last, err)
	}
	if provider.calls != 1 {
		t.Fatalf("empty response caused %d requests in one run", provider.calls)
	}
}

func TestAIOutageStopsAddingOnceHalfHorizonCovered(t *testing.T) {
	s, ch, provider := outageChannel(t, 5*time.Hour)
	ctx := context.Background()
	for attempt := 1; attempt <= 3; attempt++ {
		if err := s.Ensure(ctx, ch.ID); err == nil {
			t.Fatal("expected AI failure")
		}
		progs, err := s.store.ProgramsBetween(ctx, ch.ID, time.Now().Add(-time.Hour), time.Now().Add(5*time.Hour))
		if err != nil || len(progs) != min(attempt, 2) {
			t.Fatalf("run %d: expected one added movie until half the horizon is covered, got %d programmes, %v", attempt, len(progs), err)
		}
		if provider.calls != attempt {
			t.Fatalf("run %d: expected AI retry, got %d requests", attempt, provider.calls)
		}
	}
}

func TestManualResetAsksAIAgain(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual=%v", manual), func(t *testing.T) {
			s, ch, provider := outageChannel(t, 12*time.Hour)
			provider.err = nil
			ctx := context.Background()
			cfg, err := ParseConfig(ch.Config)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(&State{Sources: map[string]*SourceState{"main": {
				AIConfig: aiFingerprint(cfg.Source.AI), Queue: []QueuedPick{{Key: "40", Reason: "queued"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.store.SaveChannelState(ctx, ch.ID, raw); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			progs := []store.Program{{ChannelID: ch.ID, Start: now.Add(-time.Hour).UnixMilli(),
				Stop: now.Add(time.Hour).UnixMilli(), RatingKey: "0", Note: "running", SourceID: "main"}}
			for i := range 6 {
				start := now.Add(time.Hour + time.Duration(i)*2*time.Hour)
				progs = append(progs, store.Program{ChannelID: ch.ID, Start: start.UnixMilli(),
					Stop: start.Add(2 * time.Hour).UnixMilli(), RatingKey: fmt.Sprint(10 + i), SourceID: "main"})
			}
			if err := s.store.InsertPrograms(ctx, progs); err != nil {
				t.Fatal(err)
			}
			if manual {
				err = s.Reset(ctx, ch.ID)
			} else {
				err = s.Replan(ctx, ch.ID, now)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.store.ProgramsBetween(ctx, ch.ID, now.Add(-time.Hour), now.Add(12*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Note != "running" {
				t.Fatalf("running programme changed: %+v", got[0])
			}
			for i, p := range got[1:] {
				if manual && (p.Note != "AI choice" || p.RatingKey == "40") {
					t.Fatalf("reset kept an old pick at %d: %+v", i, p)
				}
				if !manual && i < 6 && p.RatingKey != fmt.Sprint(10+i) {
					t.Fatalf("automatic re-plan did not keep pick %d: %+v", i, p)
				}
			}
			if manual == (provider.calls == 0) {
				t.Fatalf("manual=%v: %d AI requests", manual, provider.calls)
			}
		})
	}
}

type blockingAI struct {
	started, release chan struct{}
}

func (p *blockingAI) Pick(_ context.Context, req ai.Request) ([]ai.Pick, error) {
	p.started <- struct{}{}
	<-p.release
	return []ai.Pick{{ID: req.Candidates[0].ID, Reason: "AI choice"}}, nil
}

func TestPlanningIsVisibleDuringAIRequest(t *testing.T) {
	s, ch, _ := outageChannel(t, 2*time.Hour)
	p := &blockingAI{started: make(chan struct{}), release: make(chan struct{})}
	s.ai = func(context.Context) (ai.Provider, error) { return p, nil }
	done := make(chan error)
	go func() { done <- s.Reset(context.Background(), ch.ID) }()
	<-p.started
	if !s.Planning(ch.ID) {
		t.Fatal("channel should count as planning while the AI answers")
	}
	close(p.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Planning(ch.ID) {
		t.Fatal("planning flag stuck after the run")
	}
}

func TestTrackCountsNestedRuns(t *testing.T) {
	s, ch, _ := outageChannel(t, time.Hour)
	outer := s.Track(ch.ID)
	inner := s.Track(ch.ID)
	inner()
	inner() // calling done twice must not end the outer run
	if !s.Planning(ch.ID) {
		t.Fatal("outer run ended too early")
	}
	outer()
	if s.Planning(ch.ID) {
		t.Fatal("planning flag stuck")
	}
}

