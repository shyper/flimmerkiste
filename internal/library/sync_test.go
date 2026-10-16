package library

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// failingServer serves a movie library and fails on the show library.
type failingServer struct {
	mediaserver.Server
	movies []mediaserver.Item
}

func (f *failingServer) Libraries(context.Context) ([]mediaserver.Library, error) {
	return []mediaserver.Library{{ID: "m", Title: "A Filme", Type: "movie"}, {ID: "s", Title: "B Serien", Type: "show"}}, nil
}

func (f *failingServer) Items(_ context.Context, lib string, _ mediaserver.Kind, fn func([]mediaserver.Item) error) error {
	if lib == "s" {
		return errors.New("timeout")
	}
	return fn(f.movies)
}

func movieItem(id string) mediaserver.Item {
	return mediaserver.Item{ID: id, Type: "movie", Title: id, Duration: 60000,
		Media: []mediaserver.MediaInfo{{Parts: []mediaserver.Part{{Key: "/p/" + id}}}}}
}

// A library failing after others were synced must not swallow their changes:
// they are in the database now and a later sync would not report them again.
func TestSyncErrorKeepsChangesOfSyncedLibraries(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertLibrary(ctx, store.Library{ID: "m", Title: "A Filme", Type: "movie"})
	st.UpsertMedia(ctx, []*store.Media{{RatingKey: "gone", LibraryID: "m", Type: "movie", Title: "gone", DurationMs: 60000}}, 1)

	srv := &failingServer{movies: []mediaserver.Item{movieItem("new1"), movieItem("new2")}}
	l := New(st, func(context.Context) (mediaserver.Server, error) { return srv, nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if all, _ := l.All(ctx); len(all) != 1 {
		t.Fatalf("catalog before: %d", len(all))
	}
	got := make(chan Changes, 1)
	l.OnSynced(func(c Changes) { got <- c })
	l.StartSync()
	var c Changes
	select {
	case c = <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("changes of the synced library were not applied")
	}
	if !c.Removed["gone"] || !c.Added["new1"] || !c.Added["new2"] {
		t.Errorf("changes: %+v", c)
	}
	if l.Get(ctx, "gone") != nil || l.Get(ctx, "new1") == nil {
		t.Error("the catalog still shows the state before the sync")
	}
	if l.Status().Error == "" {
		t.Error("the error is not reported")
	}
}
