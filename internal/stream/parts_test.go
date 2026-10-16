package stream

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

// TestServeStackedTitle plays a title Plex stores in two files (CD1, CD2):
// both play one after the other, and tuning in during the second file starts
// there instead of seeking past the end of the first.
func TestServeStackedTitle(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	for _, f := range []string{"cd1", "cd2"} {
		ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=320x180:r=25:d=3", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
			"-t", "3", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-c:a", "aac", filepath.Join(dir, f+".mkv"))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/library/metadata/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","duration":6000,"Media":[{"duration":6000,"Part":[
			{"key":"/parts/cd1","duration":3000,"Stream":[{"streamType":1,"index":0,"codec":"h264"},{"streamType":2,"index":1,"codec":"aac"}]},
			{"key":"/parts/cd2","duration":3000,"Stream":[{"streamType":1,"index":0,"codec":"h264"},{"streamType":2,"index":1,"codec":"aac"}]}]}]}]}}`)
	})
	mux.HandleFunc("/parts/{f}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(dir, r.PathValue("f")+".mkv"))
	})
	plexSrv := httptest.NewServer(mux)
	defer plexSrv.Close()

	// frames streams the title tuned in at offset and counts the video frames.
	frames := func(offset time.Duration) int {
		start := time.Now().Add(-offset)
		prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(6 * time.Second).UnixMilli(), RatingKey: "1"}
		s := &Streamer{
			FFmpeg: "ffmpeg",
			Server: func(context.Context) (mediaserver.Server, error) {
				return plex.NewBackend(&plex.Server{BaseURL: plexSrv.URL}, nil), nil
			},
			Program: func(_ context.Context, _ int64, t time.Time) (*store.Program, error) {
				if !t.Before(prog.StopTime()) {
					return nil, errors.New("end of the test guide")
				}
				return prog, nil
			},
			Media: func(context.Context, string) (*store.Media, error) {
				return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", DurationMs: 6000, PartKey: "/parts/cd1"}, nil
			},
			Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			CacheDir: dir,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rec := httptest.NewRecorder()
		s.Serve(rec, httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx), &store.Channel{ID: 1}, Options{Mode: "copy"})
		out := filepath.Join(dir, "out.ts")
		os.WriteFile(out, rec.Body.Bytes(), 0o644)
		b, _ := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", out).Output()
		fields := strings.Fields(string(b))
		if len(fields) == 0 {
			return 0
		}
		n, _ := strconv.Atoi(fields[0])
		return n
	}

	if n := frames(0); n < 140 {
		t.Errorf("from the start: %d frames, want both files (~150)", n)
	}
	if n := frames(4500 * time.Millisecond); n < 30 || n > 70 {
		t.Errorf("tuned in during the second file: %d frames, want the rest of it (~40–50)", n)
	}
}
