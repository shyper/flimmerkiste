package library

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// shortListServer lists movies with one actor, like a Plex listing that
// leaves out the rest of the cast, and has the full cast per item.
type shortListServer struct {
	mediaserver.Server
	movies []mediaserver.Item

	mu      sync.Mutex
	fetched []string
}

func (f *shortListServer) Libraries(context.Context) ([]mediaserver.Library, error) {
	return []mediaserver.Library{{ID: "m", Title: "Filme", Type: "movie"}}, nil
}

func (f *shortListServer) Items(_ context.Context, _ string, _ mediaserver.Kind, fn func([]mediaserver.Item) error) error {
	return fn(f.movies)
}

func (f *shortListServer) Item(_ context.Context, id string) (*mediaserver.Item, error) {
	f.mu.Lock()
	f.fetched = append(f.fetched, id)
	f.mu.Unlock()
	it := movieItem(id)
	it.Actors = []string{"Lead", "Willem Dafoe"}
	it.Writers = []string{"Writer"}
	it.Collections = []string{"Old collection"}
	return &it, nil
}

func (f *shortListServer) Extras(context.Context, string) ([]mediaserver.Item, error) {
	return nil, nil
}

func (f *shortListServer) takeFetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.fetched
	f.fetched = nil
	slices.Sort(out)
	return out
}

func runSync(t *testing.T, l *Library) {
	t.Helper()
	done := make(chan Changes, 1)
	l.OnSynced(func(c Changes) { done <- c })
	if !l.StartSync() {
		t.Fatal("sync did not start")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("sync did not finish")
	}
	if err := l.Status().Error; err != "" {
		t.Fatal(err)
	}
}

func TestSyncFetchesFullMetadataOfNewMovies(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, b := movieItem("a"), movieItem("b")
	a.Actors, b.Actors = []string{"Lead"}, []string{"Lead"}
	srv := &shortListServer{movies: []mediaserver.Item{a, b}}
	l := New(st, func(context.Context) (mediaserver.Server, error) { return srv, nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))

	runSync(t, l)
	if got := srv.takeFetched(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("fetched %v, want both new movies", got)
	}
	m := l.Get(ctx, "a")
	if m == nil || !slices.Equal(m.Tags.Actor, []string{"Lead", "Willem Dafoe"}) {
		t.Fatalf("actors: %+v", m)
	}
	if len(m.Tags.Collection) != 0 {
		t.Errorf("collections come from the sync only: %v", m.Tags.Collection)
	}
	if raw, err := st.MediaDetails(ctx, "a"); err != nil || len(raw) == 0 {
		t.Errorf("full metadata not stored: %q %v", raw, err)
	}

	// Not due yet: the next sync keeps the full cast without fetching again,
	// although the listing writes the short one.
	c := movieItem("c")
	srv.movies = append(srv.movies, c)
	runSync(t, l)
	if got := srv.takeFetched(); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("fetched %v, want only the new movie", got)
	}
	if m := l.Get(ctx, "a"); !slices.Contains(m.Tags.Actor, "Willem Dafoe") {
		t.Errorf("sync dropped the full cast: %v", m.Tags.Actor)
	}

	// A full scan fetches everything again.
	if err := st.ResetDetails(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	runSync(t, l)
	if got := srv.takeFetched(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("full scan fetched %v", got)
	}
	libs, _ := st.ListLibraries(ctx)
	if len(libs) != 1 || libs[0].Detailed != 3 {
		t.Errorf("libraries: %+v", libs)
	}
}

func TestNextDetailsSpreadsAroundThirtyDays(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	lo, hi := now.Add(20*24*time.Hour).Unix(), now.Add(40*24*time.Hour).Unix()
	seen := map[int64]bool{}
	for range 200 {
		d := nextDetails(now)
		if d < lo || d >= hi {
			t.Fatalf("due %v outside 20–40 days", time.Unix(d, 0).Sub(now))
		}
		seen[(d-lo)/(24*3600)] = true
	}
	if len(seen) < 10 {
		t.Errorf("due dates not spread: %d distinct days", len(seen))
	}
}

// showServer lists one show with a short cast and its episodes; the show on
// its own has the full cast.
type showServer struct {
	shortListServer
	shows []mediaserver.Item
}

func (f *showServer) Libraries(context.Context) ([]mediaserver.Library, error) {
	return []mediaserver.Library{{ID: "s", Title: "Serien", Type: "show"}}, nil
}

func (f *showServer) Items(_ context.Context, _ string, kind mediaserver.Kind, fn func([]mediaserver.Item) error) error {
	if kind == mediaserver.KindShow {
		return fn(f.shows)
	}
	var eps []mediaserver.Item
	for _, s := range f.shows {
		ep := movieItem(s.ID + "-e1")
		ep.Type, ep.ShowID, ep.ShowTitle = "episode", s.ID, s.Title
		eps = append(eps, ep)
	}
	return fn(eps)
}

func TestSyncFetchesFullMetadataOfShows(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	show := mediaserver.Item{ID: "show", Type: "show", Title: "Show", Actors: []string{"Lead"}, Summary: "Ein Detektiv in Neapel"}
	other := mediaserver.Item{ID: "other", Type: "show", Title: "Other"}
	srv := &showServer{shows: []mediaserver.Item{show, other}}
	l := New(st, func(context.Context) (mediaserver.Server, error) { return srv, nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))

	runSync(t, l)
	if got := srv.takeFetched(); !slices.Equal(got, []string{"other", "show"}) {
		t.Fatalf("fetched %v, want the shows, not their episodes", got)
	}
	ep := l.Get(ctx, "show-e1")
	if ep == nil || !slices.Equal(ep.Tags.Actor, []string{"Lead", "Willem Dafoe"}) {
		t.Fatalf("episode actors: %+v", ep)
	}
	if !slices.Equal(ep.Tags.Writer, []string{"Writer"}) || ep.ShowSummary != "Ein Detektiv in Neapel" {
		t.Errorf("episode lacks the writers or description of its show: %v %q", ep.Tags.Writer, ep.ShowSummary)
	}
	libs, _ := st.ListLibraries(ctx)
	if len(libs) != 1 || libs[0].Detailed != 2 || libs[0].Shows != 2 {
		t.Errorf("libraries: %+v", libs)
	}

	// A show that is gone takes its metadata along; the other is not due.
	srv.shows = srv.shows[:1]
	runSync(t, l)
	if got := srv.takeFetched(); len(got) != 0 {
		t.Fatalf("fetched %v, nothing is due", got)
	}
	if _, err := st.ShowDetails(ctx, "other"); err != store.ErrNotFound {
		t.Errorf("metadata of the removed show: %v", err)
	}
	if raw, err := st.ShowDetails(ctx, "show"); err != nil || len(raw) == 0 {
		t.Errorf("metadata of the show: %q %v", raw, err)
	}
}

// trailerShowServer has a trailer among the extras of the show "show".
type trailerShowServer struct{ showServer }

func (f *trailerShowServer) Extras(_ context.Context, id string) ([]mediaserver.Item, error) {
	if id != "show" {
		return nil, nil
	}
	return []mediaserver.Item{{ID: "x1", Type: "clip", Subtype: "trailer", Title: "Trailer", Duration: 90000,
		Media: []mediaserver.MediaInfo{{Parts: []mediaserver.Part{{Key: "/p/x1"}}}}}}, nil
}

func TestSyncFindsTrailersOfShows(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := &trailerShowServer{showServer{shows: []mediaserver.Item{
		{ID: "show", Type: "show", Title: "Show", Summary: "Ein Detektiv in Neapel"},
		{ID: "other", Type: "show", Title: "Other"},
	}}}
	l := New(st, func(context.Context) (mediaserver.Server, error) { return srv, nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runSync(t, l)

	// The trailer lookup runs after the sync.
	var ts []*store.Trailer
	for deadline := time.Now().Add(10 * time.Second); len(ts) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		ts, _ = st.Trailers(ctx)
	}
	if len(ts) != 1 {
		t.Fatalf("trailers: %d", len(ts))
	}
	tr := ts[0]
	if tr.ShowKey != "show" || tr.MediaKey != "" || tr.Kind != "show" || tr.Title != "Show" ||
		tr.Summary != "Ein Detektiv in Neapel" || tr.Advertised() != "show:show" {
		t.Errorf("trailer: %+v", tr)
	}
	// Not due again for a while.
	if keys, _ := st.ShowsWithoutExtras(ctx, time.Now().Add(-time.Hour)); len(keys) != 0 {
		t.Errorf("checked shows still due: %v", keys)
	}

	// A show that is gone takes its trailers along.
	srv.shows = srv.shows[1:]
	runSync(t, l)
	if ts, _ := st.Trailers(ctx); len(ts) != 0 {
		t.Errorf("trailers of the removed show remain: %+v", ts)
	}
}
