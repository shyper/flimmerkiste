package stream

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

// TestServeReportsAired: a stream reports the guide titles it sends for the
// history, and those it could not play.
func TestServeReportsAired(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "film.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x360:r=25:d=4", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "4", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", clip)
	png := filepath.Join(dir, "card.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=blue:s=1280x720", "-frames:v", "1", png)
	mux := http.NewServeMux()
	mux.HandleFunc("/library/metadata/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","Media":[{"Part":[{"key":"/library/parts/1","file":"/data/film.mkv",
			"Stream":[{"streamType":1,"index":0,"codec":"h264"},{"streamType":2,"index":1,"codec":"aac"}]}]}]}]}}`)
	})
	mux.HandleFunc("/library/parts/1", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, clip) })
	plexSrv := httptest.NewServer(mux)
	defer plexSrv.Close()

	for _, c := range []struct {
		name    string
		missing bool
	}{{"sent", false}, {"missing in the media server", true}} {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(4 * time.Second).UnixMilli(), RatingKey: "1"}
			var mu sync.Mutex
			var aired []bool
			s := &Streamer{
				FFmpeg: "ffmpeg", CacheDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
				Server: func(context.Context) (mediaserver.Server, error) {
					return plex.NewBackend(&plex.Server{BaseURL: plexSrv.URL}, nil), nil
				},
				Program: func(context.Context, int64, time.Time) (*store.Program, error) { return prog, nil },
				Media: func(context.Context, string) (*store.Media, error) {
					if c.missing {
						return nil, errors.New("deleted")
					}
					return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", DurationMs: 4000, PartKey: "/library/parts/1"}, nil
				},
				Card: func(context.Context, *store.Channel, *store.Program, int) (string, error) { return png, nil },
				Aired: func(_ context.Context, _ *store.Channel, p *store.Program, failed bool) {
					if p.Start != prog.Start {
						t.Errorf("reported another program: %+v", p)
					}
					mu.Lock()
					aired = append(aired, failed)
					mu.Unlock()
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s.Serve(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx), &store.Channel{ID: 1}, Options{Mode: "copy"})
			mu.Lock()
			defer mu.Unlock()
			if len(aired) == 0 || aired[len(aired)-1] != c.missing {
				t.Fatalf("reported %v, want failed=%v", aired, c.missing)
			}
		})
	}
}
