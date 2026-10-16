package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/store"
)

// The media picker pages through the library while scrolling.
func TestSearchPaging(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	st.UpsertLibrary(ctx, store.Library{ID: "1", Title: "Mediathek", Type: "movie", Enabled: true})
	var items []*store.Media
	for i := range 130 {
		items = append(items, &store.Media{RatingKey: fmt.Sprintf("m%d", i), LibraryID: "1", Type: "movie", Title: fmt.Sprintf("Film %03d", i), SortTitle: fmt.Sprintf("Film %03d", i)})
	}
	for i := range 70 {
		for e := range 2 {
			items = append(items, &store.Media{RatingKey: fmt.Sprintf("e%d-%d", i, e), LibraryID: "1", Type: "episode", SortTitle: fmt.Sprintf("Serie %03d", i), ShowKey: fmt.Sprintf("s%d", i),
				ShowTitle: fmt.Sprintf("Serie %03d", i), Season: 1, Episode: e + 1})
		}
	}
	if err := st.UpsertMedia(ctx, items, 1); err != nil {
		t.Fatal(err)
	}
	srv, err := New(config.Config{DataDir: dir, Horizon: 24}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}

	type page struct {
		Movies                []mediaView
		Shows                 []showView
		MoreMovies, MoreShows bool
	}
	get := func(query string) page {
		w := httptest.NewRecorder()
		srv.handleSearch(w, httptest.NewRequest("GET", "/api/library/search?"+query, nil))
		var p page
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	seen := map[string]bool{}
	for offset, want := range []int{60, 60, 10} {
		p := get(fmt.Sprintf("type=movie&offset=%d", offset*60))
		if len(p.Movies) != want || p.MoreMovies != (offset < 2) {
			t.Fatalf("page %d: %d movies, more=%v", offset, len(p.Movies), p.MoreMovies)
		}
		for _, m := range p.Movies {
			if seen[m.Key] {
				t.Fatalf("movie %s on two pages", m.Key)
			}
			seen[m.Key] = true
		}
	}
	if p := get("type=show&offset=60"); len(p.Shows) != 10 || p.MoreShows || p.Shows[0].Episodes != 2 {
		t.Fatalf("second show page: %d shows, more=%v", len(p.Shows), p.MoreShows)
	}
	if p := get("q=film+12"); len(p.Movies) != 10 || p.MoreMovies {
		t.Fatalf("search: %d movies, more=%v", len(p.Movies), p.MoreMovies)
	}
}
