package stream

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// streamEnd is where the last packet of stream sel (v:0, a:0) of file ends, in seconds.
func streamEnd(t *testing.T, file, sel string) float64 {
	t.Helper()
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", sel, "-show_entries", "packet=pts_time,duration_time",
		"-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatal(err)
	}
	end := 0.0
	for _, line := range strings.Fields(string(b)) {
		f := strings.Split(strings.TrimSuffix(line, ","), ",")
		pts, _ := strconv.ParseFloat(f[0], 64)
		d := 0.0
		if len(f) > 1 {
			d, _ = strconv.ParseFloat(f[1], 64)
		}
		end = max(end, pts+d)
	}
	return end
}

// TestPadShorterStream plays files whose audio (or picture) ends seconds before
// the other: picture and sound of the output still end together, in the film
// and in the shrunk credits (Plex would close the gap, and the next titles
// would be that far out of sync).
func TestPadShorterStream(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	page := filepath.Join(dir, "page.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=1280x720", "-frames:v", "1", page)
	for _, tc := range []struct{ name, video, audio string }{
		{"audio ends early", "10", "7"},
		{"picture ends early", "7", "10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			movie := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".mkv")
			ffmpeg(t, "-f", "lavfi", "-i", "testsrc=s=640x360:r=25:d="+tc.video, "-f", "lavfi", "-i", "sine=r=48000:d="+tc.audio,
				"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "ac3", movie)
			list, err := creditsList(dir, &CreditsPanel{Pages: []string{page}}, 0, 6*time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			opts := Options{Mode: "full", NormalizeRes: 720}
			for _, part := range []struct {
				name string
				args []string
			}{
				{"film", buildArgs(movie, 4*time.Second, 6*time.Second, 100*time.Second, 0, 0, 1, opts)},
				{"credits", buildCreditsArgs(movie, 4*time.Second, 6*time.Second, 100*time.Second, 0, 0, 1, 1, list, "", opts)},
			} {
				out := filepath.Join(dir, "out.ts")
				part.args[len(part.args)-1] = out
				ffmpeg(t, part.args[2:]...)
				v, a := streamEnd(t, out, "v:0"), streamEnd(t, out, "a:0")
				// mpegts starts 1.4s in; the item lasts 6s
				if v < 107 || a < 107 || v-a > 0.2 || a-v > 0.2 {
					t.Errorf("%s: picture ends at %.2f, sound at %.2f (want both at about 107.4)", part.name, v, a)
				}
			}
		})
	}
}
