package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/store"
)

// The history page sees what started up to the moment it asks, filtered by
// day, channel and title.
func TestHistoryAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	st.UpsertLibrary(ctx, store.Library{ID: "1", Title: "Filme", Type: "movie", Enabled: true})
	st.UpsertMedia(ctx, []*store.Media{{RatingKey: "m1", LibraryID: "1", Type: "movie", Title: "Hot Fuzz", DurationMs: 1},
		{RatingKey: "m2", LibraryID: "1", Type: "movie", Title: "Paul", DurationMs: 1}}, 1)
	ch := &store.Channel{Name: "Kino"}
	st.CreateChannel(ctx, ch)
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	st.InsertPrograms(ctx, []store.Program{
		{ChannelID: ch.ID, Start: yesterday.UnixMilli(), Stop: yesterday.Add(time.Hour).UnixMilli(), RatingKey: "m2"},
		{ChannelID: ch.ID, Start: now.Add(-time.Minute).UnixMilli(), Stop: now.Add(time.Hour).UnixMilli(), RatingKey: "m1"},
		{ChannelID: ch.ID, Start: now.Add(time.Hour).UnixMilli(), Stop: now.Add(2 * time.Hour).UnixMilli(), RatingKey: "m2"},
	})
	srv, err := New(config.Config{DataDir: dir, Horizon: 24}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	get := func(query string) []store.HistoryEntry {
		w := httptest.NewRecorder()
		srv.handleHistory(w, httptest.NewRequest("GET", "/api/history?"+query, nil))
		var out []store.HistoryEntry
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %s", query, w.Body.String())
		}
		return out
	}
	if got := get(""); len(got) != 2 || got[0].Title != "Hot Fuzz" || got[1].Title != "Paul" {
		t.Fatalf("all: %+v", got)
	}
	if got := get("day=" + yesterday.Format("2006-01-02")); len(got) != 1 || got[0].Title != "Paul" {
		t.Fatalf("yesterday: %+v", got)
	}
	if got := get("q=fuzz&channel=" + strconv.FormatInt(ch.ID, 10)); len(got) != 1 || got[0].Title != "Hot Fuzz" {
		t.Fatalf("search: %+v", got)
	}
	w := httptest.NewRecorder()
	srv.handleHistory(w, httptest.NewRequest("GET", "/api/history?day=gestern", nil))
	if w.Code != 400 {
		t.Fatalf("bad day: %d", w.Code)
	}
}
