package library

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

func TestSyncFolder(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.UpsertLibrary(ctx, store.Library{ID: "1", Title: "Filme", Type: "movie"})
	st.UpsertMedia(ctx, []*store.Media{{RatingKey: "m3", LibraryID: "1", Type: "movie", Title: "Habe ich schon", DurationMs: 1,
		Ext: map[string]string{"imdb": "tt0000003"}}}, time.Now().Unix())
	l := New(st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	dir := t.TempDir()
	write := func(base string, f FolderFile, video bool) {
		b, _ := json.Marshal(f)
		if err := os.WriteFile(filepath.Join(dir, base+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
		if video {
			if err := os.WriteFile(filepath.Join(dir, f.Video), []byte("mp4"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("Neu (2026) [imdbid-tt0000001] de", FolderFile{Video: "Neu (2026) [imdbid-tt0000001] de.mp4", TMDb: 1, IMDb: "tt0000001",
		Title: "Neu", Year: 2026, Released: "2026-12-03", Genres: []string{"Drama"}, ContentRating: "de/12", Summary: "Inhalt",
		Poster: "https://image.tmdb.org/t/p/w500/p1.jpg", Backdrop: "https://image.tmdb.org/t/p/w1280/b1.jpg", RuntimeMin: 125,
		Language: "de", Name: "Offizieller Trailer", Height: 1080, DurationMs: 150000}, true)
	write("Ohne Video", FolderFile{Video: "Ohne Video.mp4", TMDb: 2, Title: "Ohne Video", Language: "de", DurationMs: 1000}, false)
	write("Habe ich schon", FolderFile{Video: "Habe ich schon.mp4", TMDb: 3, IMDb: "tt0000003", Title: "Habe ich schon", Language: "de", DurationMs: 1000}, true)
	write("Unvollständig", FolderFile{Video: "Unvollständig.mp4", Title: "Unvollständig"}, true)
	os.WriteFile(filepath.Join(dir, "kaputt.json"), []byte("{"), 0o644)

	res, err := l.SyncFolder(ctx, dir)
	if err != nil || res.Trailers != 1 {
		t.Fatalf("sync: %+v %v", res, err)
	}
	all, _ := st.Trailers(ctx)
	if len(all) != 1 {
		t.Fatalf("%d trailers stored", len(all))
	}
	got := all[0]
	video := filepath.Join(dir, "Neu (2026) [imdbid-tt0000001] de.mp4")
	if got.Key != FolderPrefix+"1-de" || got.Origin != store.OriginFolder || got.GUID != "tmdb://1" || got.Released != "2026-12-03" ||
		got.ContentRating != "de/12" || got.Thumb == "" || got.Art == "" || got.DurationMs != 150000 || got.Language != "de" ||
		got.Part(720) != store.FileURL(video) || store.LocalFile(got.Part(720)) != video {
		t.Fatalf("trailer: %+v", got)
	}

	// trailers.py deleted the trailer: it disappears with the next scan
	os.Remove(video)
	if res, err := l.SyncFolder(ctx, dir); err != nil || res.Removed != 1 {
		t.Fatalf("second sync: %+v %v", res, err)
	}
	if all, _ := st.Trailers(ctx); len(all) != 0 {
		t.Fatalf("%d trailers left", len(all))
	}
	// a folder that cannot be read keeps what is stored
	if _, err := l.SyncFolder(ctx, filepath.Join(dir, "fehlt")); err == nil {
		t.Fatal("missing folder: no error")
	}
}
