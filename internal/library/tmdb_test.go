package library

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/tmdb"
)

func TestSyncTMDb(t *testing.T) {
	future := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	past := time.Now().AddDate(0, -1, 0).Format("2006-01-02")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /movie/upcoming", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "k" || r.URL.Query().Get("region") != "DE" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"total_pages": 1, "results": []map[string]any{
			{"id": 1, "title": "Neu im Kino", "release_date": future, "backdrop_path": "/b1.jpg", "poster_path": "/p1.jpg"},
			{"id": 2, "title": "Schon gelaufen", "release_date": past, "backdrop_path": "/b2.jpg"},
			{"id": 3, "title": "Habe ich schon", "release_date": future, "backdrop_path": "/b3.jpg"},
		}})
	})
	mux.HandleFunc("GET /movie/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "1" {
			w.Write([]byte(`{"runtime": 90}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"runtime": 125, "overview": "Inhalt", "genres": []map[string]string{{"name": "Drama"}},
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "DE", "release_dates": []map[string]any{
				{"certification": "12", "release_date": future + "T00:00:00.000Z", "type": 3}}}}}})
	})
	api := httptest.NewServer(mux)
	defer api.Close()

	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertLibrary(ctx, store.Library{ID: "1", Title: "Filme", Type: "movie"})
	st.UpsertMedia(ctx, []*store.Media{{RatingKey: "m3", LibraryID: "1", Type: "movie", Title: "Habe ich schon", DurationMs: 1, Ext: map[string]string{"tmdb": "3"}}}, time.Now().Unix())
	l := New(st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	res, err := l.SyncTMDb(ctx, &tmdb.Client{Key: "k", Base: api.URL}, "DE", "de-DE", 20*time.Second)
	if err != nil || res.Titles != 1 {
		t.Fatalf("sync: %+v %v", res, err)
	}
	tr, err := st.GetTrailer(ctx, store.TrailerPrefix+"tmdb-1")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Origin != store.OriginTMDb || tr.ContentRating != "de/12" || tr.RuntimeMin != 125 || tr.DurationMs != 20000 ||
		tr.Art != "https://image.tmdb.org/t/p/w1280/b1.jpg" || tr.Released != future || len(tr.Parts) != 0 {
		t.Errorf("slate: %+v", tr)
	}
	if _, err := l.SyncTMDb(ctx, &tmdb.Client{Key: "wrong", Base: api.URL}, "DE", "de-DE", time.Second); err == nil {
		t.Error("wrong key accepted")
	}
}
