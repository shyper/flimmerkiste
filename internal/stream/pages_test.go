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

func TestPageTimeline(t *testing.T) {
	str := func(segs []pageSegment) string {
		var b []string
		for _, s := range segs {
			kind := []string{"page", "fade", "black"}[s.kind]
			if s.kind == segFade {
				kind += fmt.Sprint(">", s.next)
			}
			b = append(b, fmt.Sprintf("%s%d %v-%v", kind, s.page, s.from.Seconds(), s.to.Seconds()))
		}
		return strings.Join(b, ", ")
	}
	for _, c := range []struct {
		total time.Duration
		n     int
		want  string
	}{
		{30 * time.Second, 4, "page0 0-7, fade>10 7-8, page1 8-15, fade>21 15-16, page2 16-23, fade>32 23-24, page3 24-29, black3 29-30"},
		{30 * time.Second, 3, "page0 0-7, fade>10 7-8, page1 8-15, fade>21 15-16, page2 16-23, fade>02 23-24, page0 24-29, black0 29-30"},
		// the last page stands at least 4s: no blend at 16s
		{20 * time.Second, 2, "page0 0-7, fade>10 7-8, page1 8-19, black1 19-20"},
		{20 * time.Second, 1, "page0 0-19, black0 19-20"},
	} {
		if got := str(pageTimeline(c.total, c.n)); got != c.want {
			t.Errorf("%v/%d:\n got %s\nwant %s", c.total, c.n, got, c.want)
		}
	}
}

// TestPlayPagedCard streams a 30s slate with three pages (red, green, blue)
// and checks the colours: pages in turn, back to the first, black at the end;
// tuned in during a blend, the page it leads to.
func TestPlayPagedCard(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	var pages []string
	for _, c := range []string{"red", "green", "blue"} {
		p := filepath.Join(dir, "page-"+c+".png")
		ffmpeg(t, "-f", "lavfi", "-i", "color="+c+":s=1280x720", "-frames:v", "1", p)
		pages = append(pages, p)
	}
	clockPNG := filepath.Join(dir, "clock.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=60x30", "-frames:v", "1", clockPNG)
	s := &Streamer{FFmpeg: "ffmpeg", CacheDir: dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Card:      func(context.Context, *store.Channel, *store.Program, int) (string, error) { return pages[0], nil },
		CardPages: func(context.Context, *store.Channel, *store.Program, int) ([]string, error) { return pages, nil },
		Clock:     func(context.Context, string, int) (string, error) { return clockPNG, nil }}
	start := time.Now().Truncate(time.Minute).Add(10 * time.Second) // no minute change within the slate
	prog := &store.Program{RatingKey: store.CardKey, Kind: store.KindCard, Start: start.UnixMilli(), Stop: start.Add(30 * time.Second).UnixMilli()}

	play := func(length time.Duration) string {
		rec := httptest.NewRecorder()
		out := &flushWriter{w: rec, rc: http.NewResponseController(rec)}
		if err := s.playCard(context.Background(), out, &store.Channel{}, prog, length, 0, 10*time.Second, Options{NormalizeRes: 720}, s.Log); err != nil {
			t.Fatal(err)
		}
		ts := filepath.Join(dir, fmt.Sprintf("out-%d.ts", length/time.Second))
		os.WriteFile(ts, rec.Body.Bytes(), 0o644)
		return ts
	}
	// the average colour of every frame, as "r g b" rounded to quarters
	frames := func(ts string) []string {
		raw, err := exec.Command("ffmpeg", "-v", "error", "-i", ts, "-vf", "scale=1:1:flags=area", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		q := func(v byte) int { return (int(v) + 32) / 64 }
		var out []string
		for i := 0; i+2 < len(raw); i += 3 {
			out = append(out, fmt.Sprint(q(raw[i]), q(raw[i+1]), q(raw[i+2])))
		}
		return out
	}
	full := frames(play(30 * time.Second))
	if len(full) != 750 {
		t.Fatalf("%d frames, want 30s at 25 fps", len(full))
	}
	for _, c := range []struct {
		at   float64
		want string
	}{{3, "4 0 0"}, {11, "0 2 0"}, {19, "0 0 4"}, {26, "4 0 0"}, {29.5, "2 0 0"}, {29.96, "0 0 0"}} {
		if got := full[int(c.at*25)]; got != c.want {
			t.Errorf("at %vs: %s, want %s", c.at, got, c.want)
		}
	}
	// tuned in at 15.5s, during the blend from green to blue
	late := frames(play(14500 * time.Millisecond))
	if got := late[5]; got != "0 0 4" {
		t.Errorf("tuned in during a blend: %s, want blue", got)
	}
}
