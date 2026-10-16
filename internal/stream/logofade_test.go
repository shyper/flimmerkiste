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
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/store"
)

// TestLogoFadesInAfterSlate: after the slate (no logo) the logo of the next
// title fades in; tuning straight into a title shows it at once.
func TestLogoFadesInAfterSlate(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x360:r=25:d=6", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "6", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	png := filepath.Join(dir, "card-x.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=gray:s=1280x720", "-frames:v", "1", png)
	logo := filepath.Join(dir, "logo.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=red:s=200x100", "-frames:v", "1", logo)
	// ffmpeg wrapper recording every call
	calls := filepath.Join(dir, "calls.txt")
	wrapper := filepath.Join(dir, "ffmpeg.sh")
	os.WriteFile(wrapper, []byte("#!/bin/sh\necho \"$*\" >> "+calls+"\nexec ffmpeg \"$@\"\n"), 0o755)

	mux := http.NewServeMux()
	mux.HandleFunc("/library/metadata/1", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","Media":[{"aspectRatio":1.78,"Part":[{"key":"/library/parts/1","file":"/m.mkv",
			"Stream":[{"streamType":1,"index":0,"codec":"h264"},{"streamType":2,"index":1,"codec":"aac"}]}]}]}]}}`)
	})
	plexSrv := httptest.NewServer(mux)
	defer plexSrv.Close()

	run := func(progs []store.Program) []string {
		os.Remove(calls)
		s := &Streamer{
			FFmpeg: wrapper,
			Server: func(context.Context) (mediaserver.Server, error) {
				return plex.NewBackend(&plex.Server{BaseURL: plexSrv.URL}, nil), nil
			},
			Program: func(_ context.Context, _ int64, at time.Time) (*store.Program, error) {
				for i := range progs {
					if progs[i].Start <= at.UnixMilli() && at.UnixMilli() < progs[i].Stop {
						return &progs[i], nil
					}
				}
				return &progs[len(progs)-1], nil
			},
			Media: func(context.Context, string) (*store.Media, error) {
				return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", DurationMs: 6000, PartKey: "/library/parts/1"}, nil
			},
			Card:      func(context.Context, *store.Channel, *store.Program, int) (string, error) { return png, nil },
			LocalFile: func(context.Context, string) string { return movie },
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			CacheDir:  dir,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Serve(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx), &store.Channel{ID: 1},
			Options{Mode: "full", NormalizeRes: 720, Watermark: &Watermark{Path: logo, Position: "tr", Opacity: 80, Size: 10}})
		b, _ := os.ReadFile(calls)
		var titles []string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, movie) {
				titles = append(titles, l)
			}
		}
		return titles
	}
	now := time.Now()
	// (a slate with less than a second left would be skipped)
	slate := store.Program{Start: now.Add(-time.Second).UnixMilli(), Stop: now.Add(2 * time.Second).UnixMilli(), RatingKey: store.CardKey, Kind: store.KindCard}
	title := store.Program{Start: now.Add(2 * time.Second).UnixMilli(), Stop: now.Add(8 * time.Second).UnixMilli(), RatingKey: "1"}
	if calls := run([]store.Program{slate, title}); len(calls) == 0 || !strings.Contains(calls[0], "fade=t=in") {
		t.Errorf("after the slate the logo should fade in: %q", calls)
	}
	now = time.Now()
	title = store.Program{Start: now.Add(-time.Second).UnixMilli(), Stop: now.Add(5 * time.Second).UnixMilli(), RatingKey: "1"}
	if calls := run([]store.Program{title}); len(calls) == 0 || strings.Contains(calls[0], "fade=t=in") {
		t.Errorf("tuned into a title the logo should be there at once: %q", calls)
	}

	// trailers and clips: in with the first, on through the run, out before the title, in with the title
	now = time.Now()
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	slate = store.Program{Start: ms(-time.Second), Stop: ms(2 * time.Second), RatingKey: store.CardKey, Kind: store.KindCard}
	trailer1 := store.Program{Start: ms(2 * time.Second), Stop: ms(4 * time.Second), RatingKey: "1", Kind: store.KindFiller}
	trailer2 := store.Program{Start: ms(4 * time.Second), Stop: ms(6 * time.Second), RatingKey: "1", Kind: store.KindFiller}
	title = store.Program{Start: ms(6 * time.Second), Stop: ms(8 * time.Second), RatingKey: "1"}
	seq := run([]store.Program{slate, trailer1, trailer2, title})
	if len(seq) < 3 {
		t.Fatalf("calls: %q", seq)
	}
	for i, want := range []struct{ in, out bool }{{true, false}, {false, true}, {true, false}} {
		if in, out := strings.Contains(seq[i], "fade=t=in"), strings.Contains(seq[i], "fade=t=out"); in != want.in || out != want.out {
			t.Errorf("item %d: fade in %v, out %v; want %v, %v", i+1, in, out, want.in, want.out)
		}
	}
}

// TestLogoFadesOutBeforeSlate: before a slate the logo fades out over the last
// second instead of vanishing with the cut; the title keeps its length.
func TestLogoFadesOutBeforeSlate(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=1280x720:r=25:d=6", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "6", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	logo := filepath.Join(dir, "logo.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=red:s=200x100", "-frames:v", "1", logo)
	args := buildArgs(movie, 0, 4*time.Second, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, LogoFadeIn: true, LogoFadeOut: true,
		Watermark: &Watermark{Path: logo, Position: "tl", Opacity: 100, Size: 10}})
	ts := filepath.Join(dir, "out.ts")
	args[len(args)-1] = ts
	ffmpeg(t, args...)

	// red share of the logo area per frame (the logo is 128 px wide at 3% margin)
	px, err := exec.Command("ffmpeg", "-v", "error", "-i", ts, "-vf", "crop=60:30:60:60,scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	var red []float64 // 0 = white, 1 = full red
	for i := 0; i+2 < len(px); i += 3 {
		red = append(red, 1-float64(px[i+1])/255)
	}
	// as long as without the logo (the test file yields 99 frames for 4s)
	if len(red) < 99 || len(red) > 100 {
		t.Fatalf("%d frames, want 4s", len(red))
	}
	for _, c := range []struct {
		frame    int
		min, max float64
	}{{0, 0, 0.1}, {20, 0.2, 0.8}, {50, 0.9, 1}, {74, 0.9, 1}, {87, 0.2, 0.8}, {len(red) - 1, 0, 0.1}} {
		if v := red[c.frame]; v < c.min || v > c.max {
			t.Errorf("frame %d: logo %.2f, want %.1f–%.1f", c.frame, v, c.min, c.max)
		}
	}
}

// TestLogoMoves: in another picture format the logo fades out at its old place
// and in at the new one; in the same format it simply stays.
func TestLogoMoves(t *testing.T) {
	wm := &Watermark{Path: "logo.png", Position: "tl", Opacity: 100, Size: 10}
	scope := pictureRect(1280, 720, 2.39)
	same := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, Watermark: wm, Aspect: 2.39, LogoFrom: scope}), " ")
	if strings.Contains(same, "fade") || strings.Contains(same, "-loop") {
		t.Errorf("same place: logo should stay: %s", same)
	}
	moved := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, Watermark: wm, LogoFrom: scope}), " ")
	for _, want := range []string{"-loop 1", "split[wmn][wmo]", "[wmo]fade=t=out:st=0:d=0.60:alpha=1[wmold]",
		"[base][wmold]overlay=38:130", "[wmb][wmnew]overlay=38:38"} {
		if !strings.Contains(moved, want) {
			t.Errorf("moved: missing %q in %s", want, moved)
		}
	}
}

// TestLogoMovesE2E: the old logo is gone after the move, the new one is there.
func TestLogoMovesE2E(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=1280x720:r=25:d=4", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "4", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	logo := filepath.Join(dir, "logo.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=red:s=200x100", "-frames:v", "1", logo)
	// the previous title was 2.39:1: its logo sat lower (y 130 instead of 38)
	args := buildArgs(movie, 0, 3*time.Second, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720,
		Watermark: &Watermark{Path: logo, Position: "tl", Opacity: 100, Size: 10}, LogoFrom: pictureRect(1280, 720, 2.39)})
	ts := filepath.Join(dir, "out.ts")
	args[len(args)-1] = ts
	ffmpeg(t, args...)
	// red share per frame in a box of the old and the new place
	red := func(crop string) []float64 {
		px, err := exec.Command("ffmpeg", "-v", "error", "-i", ts, "-vf", "crop="+crop+",scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		var v []float64
		for i := 0; i+2 < len(px); i += 3 {
			v = append(v, 1-float64(px[i+1])/255)
		}
		return v
	}
	old, cur := red("40:20:60:150"), red("40:20:60:50")
	if len(old) < 70 {
		t.Fatalf("%d frames", len(old))
	}
	for _, c := range []struct {
		name  string
		v     []float64
		frame int
		min   float64
		max   float64
	}{{"old", old, 0, 0.9, 1}, {"old", old, 20, 0, 0.05}, {"old", old, 70, 0, 0.05},
		{"new", cur, 0, 0, 0.1}, {"new", cur, 20, 0.3, 0.8}, {"new", cur, 50, 0.9, 1}, {"new", cur, 70, 0.9, 1}} {
		if v := c.v[c.frame]; v < c.min || v > c.max {
			t.Errorf("%s place, frame %d: logo %.2f, want %.2f–%.2f", c.name, c.frame, v, c.min, c.max)
		}
	}
}
