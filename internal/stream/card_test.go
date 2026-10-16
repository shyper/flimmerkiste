package stream

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

// TestPlayCard streams a 7s slate: a 2s clip encoded once, repeated by copying,
// with continuous timestamps and audio; the second time the cached clip is used.
func TestPlayCard(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	png := filepath.Join(dir, "card-test.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=1280x720", "-frames:v", "1", png)
	s := &Streamer{FFmpeg: "ffmpeg", CacheDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Card: func(context.Context, *store.Channel, *store.Program, int) (string, error) { return png, nil }}
	for run := 0; run < 2; run++ {
		rec := httptest.NewRecorder()
		out := &flushWriter{w: rec, rc: http.NewResponseController(rec)}
		err := s.playCard(context.Background(), out, &store.Channel{}, &store.Program{}, 7*time.Second, 3*time.Second, 10*time.Second,
			Options{NormalizeRes: 720}, s.Log)
		if err != nil {
			t.Fatal(err)
		}
		ts := filepath.Join(dir, "out.ts")
		os.WriteFile(ts, rec.Body.Bytes(), 0o644)
		info, _ := exec.Command("ffprobe", "-v", "error", "-count_frames", "-show_entries", "stream=codec_name,nb_read_frames,start_time",
			"-of", "csv=p=0", ts).Output()
		lines := strings.Fields(string(info)) // each stream is listed twice (program and stream)
		if len(lines) < 2 || !strings.HasPrefix(lines[0], "h264,") || !strings.HasPrefix(lines[1], "aac,") {
			t.Fatalf("run %d: streams %q", run, info)
		}
		// 7s at 25 fps, starting at the timestamp offset (3s + the muxer's 1.4s)
		if v := strings.Split(lines[0], ","); v[2] != "175" || !strings.HasPrefix(v[1], "4.4") {
			t.Errorf("run %d: video %v, want 175 frames from 4.4s", run, v)
		}
		// no gaps where the clip repeats
		pts, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts_time", "-of", "csv=p=0", ts).Output()
		var prev float64
		for i, f := range strings.Fields(string(pts)) {
			var v float64
			if _, err := fmt.Sscan(f, &v); err != nil {
				continue
			}
			if i > 0 && (v-prev > 0.05 || v <= prev-0.2) {
				t.Errorf("run %d: jump from %.3f to %.3f", run, prev, v)
			}
			prev = v
		}
	}
	if clips, _ := filepath.Glob(filepath.Join(dir, "card-test.ts")); len(clips) != 1 {
		t.Errorf("clip not cached next to the slate: %v", clips)
	}
}

// TestPlayCardTheme: theme music plays once, then the slate goes on in silence
// (no loop) and the audio lasts as long as the slate.
func TestPlayCardTheme(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	png := filepath.Join(dir, "card-test.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=1280x720", "-frames:v", "1", png)
	theme := filepath.Join(dir, "theme.mp3")
	ffmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "libmp3lame", theme)
	s := &Streamer{FFmpeg: "ffmpeg", CacheDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Card:      func(context.Context, *store.Channel, *store.Program, int) (string, error) { return png, nil },
		CardAudio: func(context.Context, *store.Channel, *store.Program) (string, bool) { return theme, true }}
	rec := httptest.NewRecorder()
	out := &flushWriter{w: rec, rc: http.NewResponseController(rec)}
	if err := s.playCard(context.Background(), out, &store.Channel{}, &store.Program{}, 7*time.Second, 0, 10*time.Second,
		Options{NormalizeRes: 720}, s.Log); err != nil {
		t.Fatal(err)
	}
	ts := filepath.Join(dir, "out.ts")
	os.WriteFile(ts, rec.Body.Bytes(), 0o644)
	// loudness per second: the tone in the first seconds, silence from 4s on
	level := func(from float64) float64 {
		b, err := exec.Command("ffmpeg", "-hide_banner", "-ss", fmt.Sprint(from), "-t", "1", "-i", ts, "-map", "0:a",
			"-af", "volumedetect", "-f", "null", "-").CombinedOutput()
		if err != nil {
			t.Fatalf("volumedetect: %v %s", err, b)
		}
		var db float64
		if i := strings.Index(string(b), "mean_volume: "); i >= 0 {
			fmt.Sscan(string(b[i+len("mean_volume: "):]), &db)
		}
		return db
	}
	if db := level(1); db < -40 {
		t.Errorf("theme not heard at 1s: %.1f dB", db)
	}
	if db := level(5); db > -60 {
		t.Errorf("theme repeated at 5s: %.1f dB", db)
	}

	// tuned in 2s into a 9s slate: 1s of the theme is left, not 3s
	rec = httptest.NewRecorder()
	out = &flushWriter{w: rec, rc: http.NewResponseController(rec)}
	prog := &store.Program{Start: 0, Stop: 9000}
	if err := s.playCard(context.Background(), out, &store.Channel{}, prog, 7*time.Second, 0, 10*time.Second,
		Options{NormalizeRes: 720}, s.Log); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(ts, rec.Body.Bytes(), 0o644)
	if db := level(0.2); db < -40 {
		t.Errorf("joined: theme not heard at once: %.1f dB", db)
	}
	if db := level(2); db > -60 {
		t.Errorf("joined: theme started from the beginning (heard at 2s): %.1f dB", db)
	}
	pts, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "a", "-show_entries", "packet=pts_time", "-of", "csv=p=0", ts).Output()
	f := strings.Fields(strings.ReplaceAll(string(pts), ",", ""))
	var first, last float64
	if len(f) > 0 {
		fmt.Sscan(f[0], &first)
		fmt.Sscan(f[len(f)-1], &last)
	}
	if dur := last - first; dur < 6.8 || dur > 7.3 {
		t.Errorf("audio lasts %.2fs, want 7s", dur)
	}
}

// TestPlayCardClock: the slate shows the time, switching exactly when the minute
// changes, with continuous timestamps across the cut clips.
func TestPlayCardClock(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	png := filepath.Join(dir, "card-test.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=1280x720", "-frames:v", "1", png)
	clocks := map[string]string{"20:06": "red", "20:07": "blue"}
	s := &Streamer{FFmpeg: "ffmpeg", CacheDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Card: func(context.Context, *store.Channel, *store.Program, int) (string, error) { return png, nil },
		Clock: func(_ context.Context, text string, _ int) (string, error) {
			p := filepath.Join(dir, "clock-"+strings.ReplaceAll(text, ":", "")+".png")
			ffmpeg(t, "-f", "lavfi", "-i", "color="+clocks[text]+":s=100x50", "-frames:v", "1", "-y", p)
			return p, nil
		}}
	// joined at 20:06:55 (local time), 7s before the end: the minute changes after 5s
	start := time.Date(2026, 1, 1, 20, 6, 50, 0, time.Local)
	prog := &store.Program{Start: start.UnixMilli(), Stop: start.Add(12 * time.Second).UnixMilli()}
	rec := httptest.NewRecorder()
	out := &flushWriter{w: rec, rc: http.NewResponseController(rec)}
	if err := s.playCard(context.Background(), out, &store.Channel{}, prog, 7*time.Second, 0, 10*time.Second,
		Options{NormalizeRes: 720}, s.Log); err != nil {
		t.Fatal(err)
	}
	ts := filepath.Join(dir, "out.ts")
	os.WriteFile(ts, rec.Body.Bytes(), 0o644)

	info, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-count_frames", "-show_entries", "stream=nb_read_frames",
		"-of", "csv=p=0", ts).Output()
	if n := strings.TrimSpace(strings.Split(string(info), "\n")[0]); n != "175" {
		t.Errorf("video frames %q, want 175", n)
	}
	pts, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts_time", "-of", "csv=p=0", ts).Output()
	var prev float64
	for i, f := range strings.Fields(string(pts)) {
		var v float64
		if _, err := fmt.Sscan(f, &v); err != nil {
			continue
		}
		if i > 0 && (v-prev > 0.05 || v <= prev-0.2) {
			t.Errorf("jump from %.3f to %.3f", prev, v)
		}
		prev = v
	}
	// colour of the clock in every frame: red until the minute changes after 5s (125 frames), then blue
	px, err := exec.Command("ffmpeg", "-v", "error", "-i", ts, "-vf", "crop=20:20:1150:50,scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	var seq strings.Builder
	for i := 0; i+2 < len(px); i += 3 {
		switch {
		case px[i] > 180 && px[i+2] < 80:
			seq.WriteByte('R')
		case px[i+2] > 180 && px[i] < 80:
			seq.WriteByte('B')
		default:
			seq.WriteByte('?')
		}
	}
	if want := strings.Repeat("R", 125) + strings.Repeat("B", 50); seq.String() != want {
		t.Errorf("clock per frame:\n%s\nwant\n%s", seq.String(), want)
	}
}
