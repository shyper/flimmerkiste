package store

import (
	"context"
	"testing"
	"time"
)

func TestHistory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertLibrary(ctx, Library{ID: "1", Title: "Filme", Type: "movie"})
	st.UpsertMedia(ctx, []*Media{
		{RatingKey: "m1", LibraryID: "1", Type: "movie", Title: "Die Ärzte", Year: 1999, DurationMs: 1},
		{RatingKey: "e1", LibraryID: "1", Type: "episode", Title: "Pilot", ShowTitle: "Columbo", Season: 1, Episode: 2, DurationMs: 1},
	}, time.Now().Unix())
	ch := &Channel{Name: "Kino"}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	at := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	st.InsertPrograms(ctx, []Program{
		{ChannelID: ch.ID, Start: at(-3 * time.Hour), Stop: at(-2 * time.Hour), RatingKey: "m1"},
		{ChannelID: ch.ID, Start: at(-2 * time.Hour), Stop: at(-2*time.Hour + time.Minute), RatingKey: TrailerPrefix + "x", Kind: KindFiller},
		{ChannelID: ch.ID, Start: at(-time.Hour), Stop: at(time.Hour), RatingKey: "e1"},
		{ChannelID: ch.ID, Start: at(time.Hour), Stop: at(3 * time.Hour), RatingKey: "m1"}, // still ahead
	})
	if n, err := st.RecordHistory(ctx, now); err != nil || n != 2 {
		t.Fatalf("record: %d %v", n, err)
	}
	if n, _ := st.RecordHistory(ctx, now); n != 0 {
		t.Fatalf("recorded twice: %d", n)
	}
	// a stream sent the episode
	st.MarkHistory(ctx, ch.ID, at(-time.Hour), false)
	all, err := st.History(ctx, HistoryQuery{})
	if err != nil || len(all) != 2 {
		t.Fatalf("history: %+v %v", all, err)
	}
	if e := all[0]; e.RatingKey != "e1" || e.ShowTitle != "Columbo" || e.Season != 1 || e.Episode != 2 || !e.Watched || e.Failed || e.ChannelName != "Kino" {
		t.Fatalf("episode: %+v", e)
	}
	if e := all[1]; e.RatingKey != "m1" || e.Watched || e.Times != 1 || e.Previous != 0 {
		t.Fatalf("movie: %+v", e)
	}

	// a stream cannot mark a program ahead: it is not recorded before its start
	later := now.Add(2 * time.Hour)
	if err := st.MarkHistory(ctx, ch.ID, at(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.History(ctx, HistoryQuery{}); len(got) != 2 {
		t.Fatalf("a program ahead was recorded: %+v", got)
	}
	// the movie airs again: counted twice, the time before is known; it failed and was replaced
	if n, _ := st.RecordHistory(ctx, later); n != 1 {
		t.Fatalf("second airing: %d", n)
	}
	st.MarkHistory(ctx, ch.ID, at(time.Hour), true)
	got, _ := st.History(ctx, HistoryQuery{Search: "ärzte"})
	if len(got) != 2 || got[0].Times != 2 || got[0].Previous != at(-3*time.Hour) || !got[0].Failed || !got[0].Watched {
		t.Fatalf("search: %+v", got)
	}
	if got, _ := st.History(ctx, HistoryQuery{Search: "colu"}); len(got) != 1 {
		t.Fatalf("search by show: %+v", got)
	}
	if got, _ := st.History(ctx, HistoryQuery{Search: "%"}); len(got) != 0 {
		t.Fatalf("wildcard not escaped: %+v", got)
	}
	if got, _ := st.History(ctx, HistoryQuery{Before: at(0), Limit: 1}); len(got) != 1 || got[0].RatingKey != "e1" {
		t.Fatalf("page: %+v", got)
	}
	if got, _ := st.History(ctx, HistoryQuery{ChannelID: ch.ID + 1}); len(got) != 0 {
		t.Fatalf("other channel: %+v", got)
	}
	// deleting the channel keeps its history
	if err := st.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.History(ctx, HistoryQuery{}); len(got) != 3 {
		t.Fatalf("after deleting the channel: %d", len(got))
	}
}

// Pages continue right after the last entry, also when several channels
// started at the same time; old entries are purged.
func TestHistoryPagesAndPurge(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertLibrary(ctx, Library{ID: "1", Title: "Filme", Type: "movie"})
	st.UpsertMedia(ctx, []*Media{{RatingKey: "m1", LibraryID: "1", Type: "movie", Title: "Film", DurationMs: 1}}, time.Now().Unix())
	now := time.Now().Truncate(time.Minute)
	var progs []Program
	for i := 0; i < 3; i++ {
		ch := &Channel{Name: "Kanal", Number: i + 1}
		if err := st.CreateChannel(ctx, ch); err != nil {
			t.Fatal(err)
		}
		progs = append(progs,
			Program{ChannelID: ch.ID, Start: now.Add(-time.Hour).UnixMilli(), Stop: now.UnixMilli(), RatingKey: "m1"},
			Program{ChannelID: ch.ID, Start: now.Add(-9 * 24 * time.Hour).UnixMilli(), Stop: now.Add(-9*24*time.Hour + time.Hour).UnixMilli(), RatingKey: "m1"})
	}
	st.InsertPrograms(ctx, progs)
	if _, err := st.RecordHistory(ctx, now); err != nil {
		t.Fatal(err)
	}
	var seen []HistoryEntry
	q := HistoryQuery{Limit: 2}
	for {
		page, err := st.History(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page...)
		if len(page) < q.Limit {
			break
		}
		q.Before, q.BeforeChannel = page[len(page)-1].Start, page[len(page)-1].ChannelID
	}
	if len(seen) != 6 {
		t.Fatalf("paged through %d of 6 entries", len(seen))
	}
	if err := st.PurgeHistoryBefore(ctx, now.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.History(ctx, HistoryQuery{}); len(got) != 3 {
		t.Fatalf("after the purge: %d entries, want 3", len(got))
	}
}
