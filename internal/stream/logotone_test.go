package stream

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogoToneFilter: gray and white recolor the logo before the opacity; the
// original keeps the filter as before.
func TestLogoToneFilter(t *testing.T) {
	cases := map[string]string{
		"":      "format=rgba,colorchannelmixer=aa=0.70[wm]",
		"color": "format=rgba,colorchannelmixer=aa=0.70[wm]",
		"gray":  "format=rgba,colorchannelmixer=rr=0.2126:rg=0.7152:rb=0.0722:gr=0.2126:gg=0.7152:gb=0.0722:br=0.2126:bg=0.7152:bb=0.0722,colorchannelmixer=aa=0.70[wm]",
		"white": "format=rgba,lutrgb=r=255:g=255:b=255,colorchannelmixer=aa=0.70[wm]",
	}
	for tone, want := range cases {
		got := watermarkFilter(&Watermark{Position: "tr", Opacity: 70, Size: 10, Tone: tone}, 1280, "1:v", "")
		if !strings.HasSuffix(got, want) {
			t.Errorf("tone %q: %s, want suffix %s", tone, got, want)
		}
	}
}

// TestLogoToneE2E: the burned-in logo is red, gray or white, and its
// transparent part stays transparent in every tone.
func TestLogoToneE2E(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=1280x720:r=25:d=2", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	// red on the left half, transparent on the right
	logo := filepath.Join(dir, "logo.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=red:s=200x100,format=rgba,geq=r=255:g=0:b=0:a='if(lt(X,100),255,0)'", "-frames:v", "1", logo)
	// logo at 38:38, 128x64: left half x 38–102, right half x 102–166
	rgb := func(ts, crop string) [3]int {
		px, err := exec.Command("ffmpeg", "-v", "error", "-i", ts, "-vf", "crop="+crop+",scale=1:1",
			"-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
		if err != nil || len(px) < 3 {
			t.Fatalf("%s: %d bytes, %v", ts, len(px), err)
		}
		px = px[len(px)-3:] // last frame
		return [3]int{int(px[0]), int(px[1]), int(px[2])}
	}
	near := func(got, want [3]int) bool {
		for i := range got {
			if d := got[i] - want[i]; d < -24 || d > 24 {
				return false
			}
		}
		return true
	}
	for tone, want := range map[string][3]int{"color": {255, 0, 0}, "gray": {54, 54, 54}, "white": {255, 255, 255}} {
		args := buildArgs(movie, 0, time.Second+time.Second/2, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720,
			Watermark: &Watermark{Path: logo, Position: "tl", Opacity: 100, Size: 10, Tone: tone}})
		ts := filepath.Join(dir, tone+".ts")
		args[len(args)-1] = ts
		ffmpeg(t, args...)
		if got := rgb(ts, "40:40:50:50"); !near(got, want) {
			t.Errorf("%s: logo %v, want about %v", tone, got, want)
		}
		if got := rgb(ts, "40:40:114:50"); !near(got, [3]int{0, 0, 0}) {
			t.Errorf("%s: transparent part %v, want black", tone, got)
		}
	}
}
