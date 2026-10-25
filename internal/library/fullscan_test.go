package library

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

type fullScanServer struct {
	shortListServer
	listed []string
}

func (f *fullScanServer) Libraries(context.Context) ([]mediaserver.Library, error) {
	return []mediaserver.Library{
		{ID: "m", Title: "Filme", Type: "movie"},
		{ID: "s", Title: "Serien", Type: "show"},
		{ID: "new", Title: "New library", Type: "movie"},
	}, nil
}

func (f *fullScanServer) Items(_ context.Context, id string, kind mediaserver.Kind, fn func([]mediaserver.Item) error) error {
	f.listed = append(f.listed, id)
	switch kind {
	case mediaserver.KindShow:
		return fn([]mediaserver.Item{{ID: "sh", Type: "show", Title: "Show"}})
	case mediaserver.KindEpisode:
		ep := movieItem("ep")
		ep.Type, ep.ShowID, ep.ShowTitle = "episode", "sh", "Show"
		return fn([]mediaserver.Item{ep})
	default:
		return fn([]mediaserver.Item{movieItem("film")})
	}
}

func TestFullScanOnlyTouchesSelectedLibrary(t *testing.T) {
	for _, id := range []string{"m", "s"} {
		t.Run(id, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			for _, lib := range []store.Library{
				{ID: "m", Title: "Filme", Type: "movie"},
				{ID: "s", Title: "Serien", Type: "show"},
				{ID: "missing", Title: "Keep this library", Type: "movie"},
			} {
				if err := st.UpsertLibrary(ctx, lib); err != nil {
					t.Fatal(err)
				}
				if err := st.MarkLibrarySynced(ctx, lib.ID, 123); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.UpsertMedia(ctx, []*store.Media{
				{RatingKey: "film", LibraryID: "m", Type: "movie", Title: "Movie", DurationMs: 1000},
				{RatingKey: "ep", LibraryID: "s", Type: "episode", ShowKey: "sh", Title: "Episode", DurationMs: 1000},
			}, 1); err != nil {
				t.Fatal(err)
			}
			if err := st.SyncShows(ctx, "s", []store.Show{{Key: "sh"}}); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(24 * time.Hour).Unix()
			detail := func(key string) store.Details {
				return store.Details{RatingKey: key, Raw: json.RawMessage(`{"old":true}`), FetchedAt: 123, DueAt: future}
			}
			if err := st.SetDetails(ctx, []store.Details{detail("film"), detail("ep")}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetShowDetails(ctx, []store.Details{detail("sh")}); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSetting(ctx, store.SetLastSync, "123"); err != nil {
				t.Fatal(err)
			}
			srv := &fullScanServer{}
			l := New(st, func(context.Context) (mediaserver.Server, error) { return srv, nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if l.StartFullScan("") {
				t.Fatal("empty full scan must not start a global sync")
			}
			done := make(chan Changes, 1)
			l.OnSynced(func(c Changes) { done <- c })
			if !l.StartFullScan(id) {
				t.Fatal("full scan did not start")
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("full scan did not finish")
			}
			if err := l.Status().Error; err != "" {
				t.Fatal(err)
			}
			wantListed, wantFetched, untouched := []string{"m"}, []string{"film"}, "ep"
			if id == "s" {
				wantListed, wantFetched, untouched = []string{"s", "s"}, []string{"ep", "sh"}, "film"
			}
			if !slices.Equal(srv.listed, wantListed) {
				t.Fatalf("listed libraries %v, want %v", srv.listed, wantListed)
			}
			if got := srv.takeFetched(); !slices.Equal(got, wantFetched) {
				t.Fatalf("fetched details %v, want %v", got, wantFetched)
			}
			if raw, err := st.MediaDetails(ctx, untouched); err != nil || string(raw) != `{"old":true}` {
				t.Fatalf("other library's metadata changed: %s, %v", raw, err)
			}
			libs, err := st.ListLibraries(ctx)
			if err != nil || len(libs) != 3 {
				t.Fatalf("scan discovered or deleted unrelated libraries: %+v, %v", libs, err)
			}
			for _, lib := range libs {
				if lib.ID != id && lib.SyncedAt != 123 {
					t.Fatalf("other library was marked synced: %+v", lib)
				}
			}
			if got := st.Setting(ctx, store.SetLastSync, ""); got != "123" {
				t.Fatalf("per-library scan advanced global last sync to %s", got)
			}
			// A rejected concurrent scan must not reset metadata for later work.
			l.mu.Lock()
			l.status.Running = true
			l.mu.Unlock()
			if l.StartFullScan(id) {
				t.Fatal("busy full scan should not start")
			}
			if keys, err := st.DueDetails(ctx, id, 0); err != nil || len(keys) != 0 {
				t.Fatalf("rejected scan reset metadata: %v, %v", keys, err)
			}
		})
	}
}
