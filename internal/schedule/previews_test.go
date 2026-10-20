package schedule

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/ai"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/rules"
	"github.com/shyper/flimmerkiste/internal/store"
)

func TestFillGapPreviewSlot(t *testing.T) {
	g := &generator{cfg: &Config{Previews: true}}
	from := time.Date(2026, 10, 10, 19, 55, 0, 0, time.Local)
	progs := g.fillGap(from, from.Add(5*time.Minute))
	if len(progs) != 1 || progs[0].Kind != store.KindPreview || progs[0].Stop-progs[0].Start != 5*60000 {
		t.Fatalf("expected one 5 min preview slot, got %+v", progs)
	}
	short := g.fillGap(from, from.Add(time.Minute))
	if len(short) != 1 || short[0].Kind != store.KindCard {
		t.Fatalf("short breaks keep the plain slate, got %+v", short)
	}
	g.cfg.Previews = false
	if p := g.fillGap(from, from.Add(5*time.Minute)); p[0].Kind != store.KindCard {
		t.Fatal("previews disabled")
	}
}

func TestBindSlot(t *testing.T) {
	s := &Scheduler{}
	base := time.Date(2026, 10, 10, 19, 57, 0, 0, time.Local)
	ms := func(d time.Duration) int64 { return base.Add(d).UnixMilli() }
	slot := store.Program{ID: 9, ChannelID: 1, Start: ms(0), Stop: ms(3 * time.Minute), RatingKey: store.PreviewKey, Kind: store.KindPreview}
	progs := []store.Program{
		slot,
		{Start: ms(3 * time.Minute), Stop: ms(2 * time.Hour), RatingKey: "next"},       // airs right after: has its own slate
		{Start: ms(2 * time.Hour), Stop: ms(4 * time.Hour), RatingKey: "late"},         // 21:57, trailer fits
		{Start: ms(4 * time.Hour), Stop: ms(6 * time.Hour), RatingKey: "long"},         // trailer too long for the slot
		{Start: ms(30 * time.Hour), Stop: ms(32 * time.Hour), RatingKey: "tomorrow+1"}, // beyond reach
	}
	tr := func(key string, sec int64) *store.Trailer {
		return &store.Trailer{Key: "t:" + key, MediaKey: key, DurationMs: sec * 1000}
	}
	idx := &trailerIndex{byMedia: map[string]*store.Trailer{
		"next": tr("next", 60), "late": tr("late", 120), "long": tr("long", 200), "tomorrow+1": tr("tomorrow+1", 60),
	}}
	out := s.bindSlot(slot, progs, idx, map[int64]bool{}, map[string]time.Time{}, rand.New(rand.NewSource(1)))
	if len(out) != 3 {
		t.Fatalf("expected trailer, promo, slate: %+v", out)
	}
	if out[0].RatingKey != "t:late" || out[0].Kind != store.KindFiller || out[0].Stop-out[0].Start != 120000 {
		t.Fatalf("trailer: %+v", out[0])
	}
	if out[1].RatingKey != store.PromoKey || out[1].Note != strconv.FormatInt(ms(2*time.Hour), 10) || out[1].Stop-out[1].Start != promoLen.Milliseconds() {
		t.Fatalf("promo: %+v", out[1])
	}
	if out[2].Kind != store.KindCard || out[2].Stop != slot.Stop || out[0].Start != slot.Start {
		t.Fatalf("slate must end the slot: %+v", out[2])
	}
	// Nothing left to announce: a plain slate.
	none := s.bindSlot(slot, progs, idx, map[int64]bool{ms(2 * time.Hour): true}, map[string]time.Time{}, rand.New(rand.NewSource(1)))
	if len(none) != 1 || none[0].RatingKey != store.CardKey || none[0].Start != slot.Start || none[0].Stop != slot.Stop {
		t.Fatalf("expected plain slate: %+v", none)
	}
}

// A show trailer stands for every episode, so its last run counts for all of them.
func TestBindSlotShowTrailer(t *testing.T) {
	s := &Scheduler{}
	base := time.Date(2026, 10, 10, 19, 57, 0, 0, time.Local)
	ms := func(d time.Duration) int64 { return base.Add(d).UnixMilli() }
	slot := store.Program{ID: 9, ChannelID: 1, Start: ms(0), Stop: ms(3 * time.Minute), RatingKey: store.PreviewKey, Kind: store.KindPreview}
	progs := []store.Program{slot, {Start: ms(2 * time.Hour), Stop: ms(3 * time.Hour), RatingKey: "ep2"}}
	show := &store.Trailer{Key: "t:s", ShowKey: "s", DurationMs: 60000}
	idx := &trailerIndex{byMedia: map[string]*store.Trailer{"show:s": show, "ep2": show}}
	out := s.bindSlot(slot, progs, idx, map[int64]bool{}, map[string]time.Time{}, rand.New(rand.NewSource(1)))
	if len(out) < 2 || out[0].RatingKey != "t:s" {
		t.Fatalf("expected the show trailer: %+v", out)
	}
	lastPromo := map[string]time.Time{}
	s.bindSlot(slot, progs, idx, map[int64]bool{}, lastPromo, rand.New(rand.NewSource(1)))
	if _, ok := lastPromo["show:s"]; !ok {
		t.Fatalf("last run not recorded for the show: %v", lastPromo)
	}
}

func TestShowTrailersFollowChannelFilter(t *testing.T) {
	ep := &store.Media{RatingKey: "e1", LibraryID: "2", Type: "episode", Title: "Pilot", ShowKey: "s1", ShowTitle: "Krimi",
		Season: 1, Episode: 1, Tags: store.Tags{Genre: []string{"Krimi"}}}
	cfg := &Config{
		Source: Source{Type: "rules", Rules: &rules.Group{Match: "all", Rules: []rules.Rule{{Field: "genre", Op: "is", Values: []string{"Krimi"}}}}},
		Filler: &Source{Type: "trailers", TrailerOrigin: "library", ChannelFilter: true},
	}
	g := &generator{ch: &store.Channel{Kind: KindSmart}, cfg: cfg, catalog: []*store.Media{ep},
		resolved: map[string][]*store.Media{}, langs: []string{"deu"},
		trailers: []*store.Trailer{
			{Key: "t:s1", Origin: store.OriginLibrary, ShowKey: "s1", Kind: "show", Title: "Krimi", Language: "deu", DurationMs: 1000},
			{Key: "t:gone", Origin: store.OriginLibrary, ShowKey: "gone", Kind: "show", Title: "Weg", Language: "deu", DurationMs: 1000},
		}}
	g.index()
	pool := g.fillerPool(time.Now())
	if len(pool) != 1 || pool[0].RatingKey != "t:s1" {
		t.Fatalf("expected the trailer of the show on the channel, got %+v", pool)
	}
}

func TestTrailersFollowChannelFilter(t *testing.T) {
	horror := &store.Media{RatingKey: "1", LibraryID: "1", Type: "movie", Title: "Grusel", Tags: store.Tags{Genre: []string{"Horror"}}}
	comedy := &store.Media{RatingKey: "2", LibraryID: "1", Type: "movie", Title: "Spaß", Tags: store.Tags{Genre: []string{"Komödie"}}}
	cfg := &Config{
		Source: Source{Type: "rules", Rules: &rules.Group{Match: "all", Rules: []rules.Rule{
			{Field: "genre", Op: "is", Values: []string{"Horror"}}, {Field: "library", Op: "is", Values: []string{"1"}}}}},
		Filler: &Source{Type: "trailers", TrailerOrigin: "all", ChannelFilter: true},
	}
	g := &generator{ch: &store.Channel{Kind: KindSmart}, cfg: cfg, catalog: []*store.Media{horror, comedy},
		resolved: map[string][]*store.Media{}, langs: []string{"deu"},
		trailers: []*store.Trailer{
			{Key: "t:1", Origin: store.OriginLibrary, MediaKey: "1", Title: "Grusel", Language: "deu", DurationMs: 1000},
			{Key: "t:2", Origin: store.OriginLibrary, MediaKey: "2", Title: "Spaß", Language: "deu", DurationMs: 1000},
			// Discover titles belong to no library: that rule is skipped for them, the genre still counts.
			{Key: "t:d1", Origin: store.OriginDiscover, GUID: "plex://movie/a", Kind: "movie", Title: "Neuer Grusel", Genres: []string{"Horror"}, Language: "de", DurationMs: 1000},
			{Key: "t:d2", Origin: store.OriginDiscover, GUID: "plex://movie/b", Kind: "movie", Title: "Neuer Spaß", Genres: []string{"Komödie"}, Language: "de", DurationMs: 1000},
		}}
	g.index()
	var keys []string
	for _, m := range g.fillerPool(time.Now()) {
		keys = append(keys, m.RatingKey)
	}
	if len(keys) != 2 || keys[0] != "t:1" || keys[1] != "t:d1" {
		t.Fatalf("expected the horror trailers, got %v", keys)
	}
	cfg.Filler.ChannelFilter = false
	g.resolved = map[string][]*store.Media{}
	if n := len(g.fillerPool(time.Now())); n != 4 {
		t.Fatalf("without the channel filter all trailers qualify, got %d", n)
	}
}

// fakeAI records requests and picks the first candidate.
type fakeAI struct{ themes []string }

func (f *fakeAI) Pick(_ context.Context, req ai.Request) ([]ai.Pick, error) {
	f.themes = append(f.themes, req.Theme)
	return []ai.Pick{{ID: req.Candidates[0].ID, Reason: "neu"}}, nil
}

func TestEditedPromptDropsQueuedPicks(t *testing.T) {
	fake := &fakeAI{}
	s := &Scheduler{ai: func(context.Context) (ai.Provider, error) { return fake, nil },
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	catalog := []*store.Media{{RatingKey: "old", Type: "movie", Title: "Alt"}, {RatingKey: "new", Type: "movie", Title: "Neu"}}
	src := &Source{Type: "ai", AI: &AIConfig{Theme: "Westernfilme", BatchSize: 1, EpisodesPerShow: 1}}
	g := &generator{s: s, ctx: context.Background(), ch: &store.Channel{Name: "KI"}, cfg: &Config{}, catalog: catalog,
		rng: rand.New(rand.NewSource(1)), resolved: map[string][]*store.Media{}, lastAired: map[string]time.Time{}}
	g.index()
	ss := &SourceState{ShowCursor: map[string]string{}, AIConfig: aiFingerprint(src.AI),
		Queue: []QueuedPick{{Key: "old", Reason: "Western"}}}

	// Same prompt: the queue is used, no request.
	if m, _, _ := g.pickAI("main", ss, src, time.Now()); m.RatingKey != "old" || len(fake.themes) != 0 {
		t.Fatalf("queued pick expected, got %s (%d requests)", m.RatingKey, len(fake.themes))
	}
	// Edited prompt: queued picks of the old theme are dropped, the AI is asked at once
	// even while paused after an earlier error.
	ss.Queue, ss.AIBackoff = []QueuedPick{{Key: "old", Reason: "Western"}}, time.Now().Add(time.Hour).Unix()
	src.AI.Theme = "Superheldenfilme"
	m, reason, err := g.pickAI("main", ss, src, time.Now())
	if err != nil || reason != "neu" || len(fake.themes) != 1 || fake.themes[0] != "Superheldenfilme" {
		t.Fatalf("new prompt not used: %v %q %v %v", m, reason, err, fake.themes)
	}
	// States saved before the fingerprint existed keep their queue.
	legacy := &SourceState{ShowCursor: map[string]string{}, Queue: []QueuedPick{{Key: "old"}}}
	if m, _, _ := g.pickAI("main", legacy, src, time.Now()); m.RatingKey != "old" {
		t.Fatal("queue of an older state should be kept")
	}
}

// candidateAI records the candidates of the last request.
type candidateAI struct{ cands []ai.Candidate }

func (c *candidateAI) Pick(_ context.Context, req ai.Request) ([]ai.Pick, error) {
	c.cands = req.Candidates
	return []ai.Pick{{ID: req.Candidates[0].ID}}, nil
}

// A niche theme must not depend on luck when the library exceeds what one request can carry.
func TestAICandidatesPreferTheme(t *testing.T) {
	fake := &candidateAI{}
	s := &Scheduler{ai: func(context.Context) (ai.Provider, error) { return fake, nil },
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var catalog []*store.Media
	for i := 0; i < DefaultAICandidates+400; i++ {
		catalog = append(catalog, &store.Media{RatingKey: fmt.Sprint(i), Type: "movie", Title: fmt.Sprintf("Film %d", i), Summary: "Irgendein Film."})
	}
	// Relevant through the collection or late in the summary (not in the excerpt sent to the model).
	catalog[5].Tags.Collection = []string{"Edgar Wallace Filmreihe"}
	catalog[700].Summary = strings.Repeat("Lange Beschreibung. ", 20) + "Nach einem Roman von Edgar Wallace."
	catalog[1100].Title = "Edgar Wallace: Der Hexer"
	src := &Source{Type: "ai", AI: &AIConfig{Theme: "Edgar-Wallace-Krimis der 60er", BatchSize: 3, EpisodesPerShow: 1}}
	for seed := int64(1); seed <= 5; seed++ {
		g := &generator{s: s, ctx: context.Background(), ch: &store.Channel{Name: "KI"}, cfg: &Config{}, catalog: catalog,
			rng: rand.New(rand.NewSource(seed)), resolved: map[string][]*store.Media{}, lastAired: map[string]time.Time{}}
		g.index()
		if err := g.refillAI(&SourceState{ShowCursor: map[string]string{}}, src.AI, catalog, time.Now()); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, c := range fake.cands {
			got[c.ID] = true
		}
		if len(fake.cands) != DefaultAICandidates || !got["5"] || !got["700"] || !got["1100"] {
			t.Fatalf("seed %d: relevant titles missing among %d candidates", seed, len(fake.cands))
		}
	}
	if w := themeWords("Edgar-Wallace-Krimis der 60er, bitte mit Spannung"); strings.Join(w, ",") != "edgar,wallace,krimis,60er,spannung" {
		t.Fatalf("theme words: %v", w)
	}
}

// The number of candidates per request comes from the settings, within bounds.
func TestAICandidatesSetting(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	fake := &candidateAI{}
	s := &Scheduler{store: st, ai: func(context.Context) (ai.Provider, error) { return fake, nil },
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var catalog []*store.Media
	for i := 0; i < 300; i++ {
		catalog = append(catalog, &store.Media{RatingKey: fmt.Sprint(i), Type: "movie", Title: fmt.Sprintf("Film %d", i)})
	}
	src := &Source{Type: "ai", AI: &AIConfig{Theme: "Krimis", BatchSize: 3, EpisodesPerShow: 1}}
	for _, c := range []struct {
		setting string
		want    int
	}{{"", 300}, {"120", 120}, {"10", MinAICandidates}, {"999999", 300}} {
		if err := st.SetSetting(ctx, store.SetAICandidates, c.setting); err != nil {
			t.Fatal(err)
		}
		g := &generator{s: s, ctx: ctx, ch: &store.Channel{Name: "KI"}, cfg: &Config{}, catalog: catalog,
			rng: rand.New(rand.NewSource(1)), resolved: map[string][]*store.Media{}, lastAired: map[string]time.Time{}}
		g.index()
		if err := g.refillAI(&SourceState{ShowCursor: map[string]string{}}, src.AI, catalog, time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(fake.cands) != c.want {
			t.Errorf("setting %q: %d candidates, want %d", c.setting, len(fake.cands), c.want)
		}
	}
	if err := st.SetSetting(ctx, store.SetAICandidates, "999999"); err != nil {
		t.Fatal(err)
	}
	if got := s.aiCandidates(ctx); got != MaxAICandidates {
		t.Errorf("upper bound: %d, want %d", got, MaxAICandidates)
	}
}

func TestFillGapOverrun(t *testing.T) {
	movie := &store.Media{RatingKey: "1", LibraryID: "1", Type: "movie", Title: "Film"}
	cfg := &Config{Filler: &Source{Type: "trailers", TrailerOrigin: "library"}}
	cfg.Filler.normalize()
	g := &generator{ch: &store.Channel{Kind: KindSmart}, cfg: cfg, catalog: []*store.Media{movie},
		resolved: map[string][]*store.Media{}, lastAired: map[string]time.Time{}, langs: []string{"deu"},
		rng: rand.New(rand.NewSource(1)), overrun: fillerOverrun,
		trailers: []*store.Trailer{
			{Key: "t:40", Origin: store.OriginLibrary, MediaKey: "1", Title: "Film", Language: "deu", DurationMs: 40_000},
			{Key: "t:90", Origin: store.OriginLibrary, MediaKey: "1", Title: "Film", Language: "deu", DurationMs: 90_000},
		}}
	g.index()
	from := time.Date(2026, 10, 10, 19, 59, 30, 0, time.Local)
	to := from.Add(30 * time.Second)
	progs := g.fillGap(from, to)
	if len(progs) != 1 || progs[0].RatingKey != "t:40" || progs[0].Stop != to.Add(10*time.Second).UnixMilli() {
		t.Fatalf("expected the 40s trailer running 10s past the round time, got %+v", progs)
	}
	g.overrun = 0
	if progs := g.fillGap(from, to); len(progs) != 1 || progs[0].Kind != store.KindCard || progs[0].Stop != to.UnixMilli() {
		t.Fatalf("without overrun the slate fills the gap, got %+v", progs)
	}
}

// A show trailer advertises a season premiere or the first episode of the
// show in the program, not every episode.
func TestBindPreviewsShowTrailer(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, library.New(st, nil, log), nil, time.Hour, log)
	st.UpsertLibrary(ctx, store.Library{ID: "s", Title: "Serien", Type: "show"})
	ep := func(key string, season, episode int) *store.Media {
		return &store.Media{RatingKey: key, LibraryID: "s", Type: "episode", Title: key, ShowKey: "sh", ShowTitle: "Show",
			Season: season, Episode: episode, DurationMs: 3600000}
	}
	st.UpsertMedia(ctx, []*store.Media{ep("e3", 1, 3), ep("e4", 1, 4), ep("s2e1", 2, 1)}, 1)
	st.SyncShows(ctx, "s", []store.Show{{Key: "sh"}})
	st.SetShowTrailers(ctx, "sh", []*store.Trailer{{Key: "t:x", Origin: store.OriginLibrary, ShowKey: "sh", Kind: "show",
		Title: "Show", Language: "deu", DurationMs: 60000, Parts: []store.TrailerPart{{Height: 720, URL: "/x"}}}}, time.Now())

	number := 0
	run := func(name string, eps ...string) []store.Program {
		t.Helper()
		number++
		ch := &store.Channel{Number: number, Name: name, Enabled: true, Kind: KindSmart}
		if err := st.CreateChannel(ctx, ch); err != nil {
			t.Fatal(err)
		}
		base := time.Now().Add(10 * time.Minute).Truncate(time.Minute)
		progs := []store.Program{{ChannelID: ch.ID, Start: base.UnixMilli(), Stop: base.Add(3 * time.Minute).UnixMilli(),
			RatingKey: store.PreviewKey, Kind: store.KindPreview}}
		at := base.Add(-6 * time.Hour)
		for _, e := range eps {
			if e == "|" { // the slot
				at = base.Add(time.Hour)
				continue
			}
			progs = append(progs, store.Program{ChannelID: ch.ID, Start: at.UnixMilli(), Stop: at.Add(time.Hour).UnixMilli(), RatingKey: e})
			at = at.Add(time.Hour)
		}
		if err := st.InsertPrograms(ctx, progs); err != nil {
			t.Fatal(err)
		}
		if err := s.BindPreviews(ctx, ch.ID, base.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		out, err := st.ProgramsBetween(ctx, ch.ID, base, base.Add(3*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := run("first", "|", "e3", "e4"); len(out) == 0 || out[0].RatingKey != "t:x" {
		t.Errorf("first episode of the show: %+v", out)
	}
	if out := run("again", "e3", "|", "e4"); len(out) == 0 || out[0].RatingKey == "t:x" {
		t.Errorf("an episode after another one got the trailer: %+v", out)
	}
	if out := run("premiere", "e4", "|", "s2e1"); len(out) == 0 || out[0].RatingKey != "t:x" {
		t.Errorf("season premiere: %+v", out)
	}
}
