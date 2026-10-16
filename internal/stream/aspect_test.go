package stream

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPictureRect(t *testing.T) {
	for _, c := range []struct {
		aspect float64
		want   image.Rectangle
	}{
		{0, image.Rect(0, 0, 1280, 720)},
		{1.78, image.Rect(0, 0, 1280, 720)},
		{4.0 / 3, image.Rect(160, 0, 1120, 720)},
		{2.39, image.Rect(0, 92, 1280, 628)},
	} {
		if got := pictureRect(1280, 720, c.aspect); got != c.want {
			t.Errorf("%.2f: got %v, want %v", c.aspect, got, c.want)
		}
	}
	// 4:3 in the credits box: full height, centred
	if got := fitRect(CreditsVideo(1280, 720), 4.0/3); got != image.Rect(112, 38, 552, 368) {
		t.Errorf("4:3 in the credits box: %v", got)
	}
}

func TestLogoInPicture(t *testing.T) {
	wm := &Watermark{Path: "logo.png", Position: "tr", Opacity: 80, Size: 10}
	for _, c := range []struct {
		aspect float64
		want   string
	}{
		{0, "overlay=1242-w:38"},
		{4.0 / 3, "overlay=1082-w:38"}, // inside the pillarbox
		{2.39, "overlay=1242-w:130"},   // below the letterbox bar
	} {
		args := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, Watermark: wm, Aspect: c.aspect}), " ")
		if !strings.Contains(args, c.want) {
			t.Errorf("%.2f: missing %q in %s", c.aspect, c.want, args)
		}
	}
	args := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, Watermark: wm}), " ")
	if strings.Contains(args, "fade") || strings.Contains(args, "-loop") {
		t.Errorf("logo fades in without LogoFadeIn: %s", args)
	}
	args = strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, Watermark: wm, LogoFadeIn: true}), " ")
	for _, want := range []string{"-loop 1 -framerate 25 -t 1.800 -i logo.png", "fade=t=in:st=0.30:d=1.00:alpha=1[wm]"} {
		if !strings.Contains(args, want) {
			t.Errorf("fade-in: missing %q in %s", want, args)
		}
	}
	// before a slate: repeated in the filter, faded in and out over the last second of 60s
	args = strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, Watermark: wm, LogoFadeIn: true, LogoFadeOut: true}), " ")
	for _, want := range []string{"-i logo.png", "loop=loop=-1:size=1,setpts=N/25/TB,fade=t=in:st=0.30:d=1.00:alpha=1,fade=t=out:st=59.00:d=1.00:alpha=1[wm]"} {
		if !strings.Contains(args, want) {
			t.Errorf("fade-out: missing %q in %s", want, args)
		}
	}
	if strings.Contains(args, "-loop 1") {
		t.Errorf("fade-out: logo looped as input: %s", args)
	}
}

// TestCreditsSegment43 shrinks 4:3 credits over a grey panel: the pillarbox
// bars are black until the credits, then only the picture shrinks, centred in
// the box, with the panel beside it.
func TestCreditsSegment43(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x480:r=25:d=10", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "10", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	page := filepath.Join(dir, "page.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=gray:s=1280x720", "-frames:v", "1", page)
	list, err := creditsList(dir, &CreditsPanel{Pages: []string{page}}, 3*time.Second, 6*time.Second, []time.Duration{2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	args := buildCreditsArgs(movie, 2*time.Second, 6*time.Second, 0, 0, 0, 1, 2, list, "", Options{Mode: "full", NormalizeRes: 720, Aspect: 4.0 / 3})
	out := filepath.Join(dir, "credits.ts")
	args[len(args)-1] = out
	ffmpeg(t, args[2:]...)
	for _, c := range []struct {
		frame int
		box   [4]int
		want  string // dark, grey or white
	}{
		{20, [4]int{40, 300, 60, 100}, "dark"},    // left bar before the credits
		{20, [4]int{600, 300, 80, 100}, "white"},  // the picture
		{100, [4]int{50, 150, 40, 100}, "grey"},   // left of the small picture, inside the box
		{100, [4]int{300, 150, 80, 100}, "white"}, // the small picture
		{100, [4]int{800, 500, 80, 100}, "grey"},  // the panel
	} {
		if got := level(t, out, c.frame, c.box); got != c.want {
			t.Errorf("frame %d at %v: %s, want %s", c.frame, c.box, got, c.want)
		}
	}
}

// level classifies the mean brightness of a box (x, y, w, h) in a frame.
func level(t *testing.T, file string, frame int, box [4]int) string {
	t.Helper()
	px, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", file, "-frames:v", "1",
		"-vf", fmt.Sprintf(`select=eq(n\,%d),crop=%d:%d:%d:%d,format=gray`, frame, box[2], box[3], box[0], box[1]),
		"-f", "rawvideo", "pipe:1").Output()
	if err != nil || len(px) == 0 {
		t.Fatalf("read frame %d: %v", frame, err)
	}
	sum := 0
	for _, p := range px {
		sum += int(p)
	}
	switch mean := sum / len(px); {
	case mean < 50:
		return "dark"
	case mean > 200:
		return "white"
	default:
		return "grey"
	}
}
