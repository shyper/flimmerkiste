package stream

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// TestServeFolderTrailer plays a trailer of the trailer folder straight from
// disk, without asking the media server.
func TestServeFolderTrailer(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "Kino (2026) [imdbid-tt0000001] de.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x360:r=25:d=4", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "4", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", clip)
	tr := &store.Trailer{Key: store.TrailerPrefix + "folder-1-de", Origin: store.OriginFolder, Title: "Kino", DurationMs: 4000,
		Parts: []store.TrailerPart{{Height: 1080, URL: store.FileURL(clip)}}}
	start := time.Now()
	prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(4 * time.Second).UnixMilli(), RatingKey: tr.Key, Kind: store.KindFiller}
	asked := false
	s := &Streamer{
		FFmpeg: "ffmpeg",
		Server: func(context.Context) (mediaserver.Server, error) {
			asked = true
			return nil, errors.New("no media server")
		},
		Program:  func(context.Context, int64, time.Time) (*store.Program, error) { return prog, nil },
		Media:    func(context.Context, string) (*store.Media, error) { return tr.Media(720), nil },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		CacheDir: dir,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	s.Serve(rec, httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx), &store.Channel{ID: 1}, Options{Mode: "copy"})
	out := filepath.Join(dir, "out.ts")
	os.WriteFile(out, rec.Body.Bytes(), 0o644)
	b, _ := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", out).Output()
	n := 0
	if f := strings.Fields(string(b)); len(f) > 0 {
		n, _ = strconv.Atoi(f[0])
	}
	if n < 50 {
		t.Fatalf("%d frames", n)
	}
	if asked {
		t.Error("asked the media server for a trailer on disk")
	}
}
