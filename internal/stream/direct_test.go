package stream

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

// TestServeDirectFile plays a title from disk while Plex refuses to stream it,
// and falls back to Plex when the file on disk cannot be read.
func TestServeDirectFile(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x360:r=25:d=4", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "4", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	broken := filepath.Join(dir, "broken.mkv")
	os.WriteFile(broken, []byte("not a video"), 0o644)

	var plexServes atomic.Bool
	var partHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/library/metadata/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","Media":[{"Part":[{"key":"/library/parts/1","file":"/data/movies/movie.mkv",
			"Stream":[{"streamType":1,"index":0,"codec":"h264"},{"streamType":2,"index":1,"codec":"aac"}]}]}]}]}}`)
	})
	mux.HandleFunc("/library/parts/1", func(w http.ResponseWriter, r *http.Request) {
		partHits.Add(1)
		if !plexServes.Load() {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		http.ServeFile(w, r, movie)
	})
	plexSrv := httptest.NewServer(mux)
	defer plexSrv.Close()

	run := func(local string) (int, error) {
		start := time.Now()
		prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(4 * time.Second).UnixMilli(), RatingKey: "1"}
		var asked string
		s := &Streamer{
			FFmpeg: "ffmpeg",
			Server: func(context.Context) (mediaserver.Server, error) {
				return plex.NewBackend(&plex.Server{BaseURL: plexSrv.URL}, nil), nil
			},
			Program: func(context.Context, int64, time.Time) (*store.Program, error) { return prog, nil },
			Media: func(context.Context, string) (*store.Media, error) {
				return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", DurationMs: 4000, PartKey: "/library/parts/1"}, nil
			},
			LocalFile: func(_ context.Context, p string) string { asked = p; return local },
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			CacheDir:  dir,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		rec := httptest.NewRecorder()
		s.Serve(rec, httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx), &store.Channel{ID: 1}, Options{Mode: "copy"})
		if asked != "/data/movies/movie.mkv" {
			t.Errorf("asked for %q, want the path Plex reports", asked)
		}
		out := filepath.Join(dir, "out.ts")
		os.WriteFile(out, rec.Body.Bytes(), 0o644)
		b, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", out).Output()
		fields := strings.Fields(string(b)) // the stream is listed twice (program and stream)
		if len(fields) == 0 {
			return 0, err
		}
		n, _ := strconv.Atoi(fields[0])
		return n, err
	}

	// Plex answers 503: only the file on disk can deliver the title.
	if n, err := run(movie); err != nil || n < 50 {
		t.Fatalf("direct file: %d frames (%v)", n, err)
	}
	// The local file is broken: Plex takes over.
	plexServes.Store(true)
	partHits.Store(0)
	if n, err := run(broken); err != nil || n < 50 {
		t.Fatalf("fallback: %d frames (%v)", n, err)
	}
	if partHits.Load() == 0 {
		t.Error("fallback did not ask Plex")
	}
}
