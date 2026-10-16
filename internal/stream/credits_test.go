package stream

import (
	"context"
	"image"
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

func TestCreditsStart(t *testing.T) {
	length := 100 * time.Minute
	ms := func(d time.Duration) int64 { return d.Milliseconds() }
	for _, c := range []struct {
		name    string
		markers []mediaserver.Marker
		want    time.Duration
		ok      bool
	}{
		{"none", nil, 0, false},
		{"intro only", []mediaserver.Marker{{Type: "intro", StartTimeOffset: 0, EndTimeOffset: 60000}}, 0, false},
		{"final", []mediaserver.Marker{{Type: "credits", StartTimeOffset: ms(95 * time.Minute), EndTimeOffset: ms(length), Final: true}}, 95 * time.Minute, true},
		// credits, a post-credits scene, the rest of the credits: only the last part shrinks
		{"post-credits scene", []mediaserver.Marker{
			{Type: "credits", StartTimeOffset: ms(90 * time.Minute), EndTimeOffset: ms(94 * time.Minute)},
			{Type: "credits", StartTimeOffset: ms(96 * time.Minute), EndTimeOffset: ms(length), Final: true},
		}, 96 * time.Minute, true},
		{"not flagged but at the end", []mediaserver.Marker{{Type: "credits", StartTimeOffset: ms(97 * time.Minute), EndTimeOffset: ms(length - 3*time.Second)}}, 97 * time.Minute, true},
		{"too short", []mediaserver.Marker{{Type: "credits", StartTimeOffset: ms(length - 10*time.Second), EndTimeOffset: ms(length), Final: true}}, 0, false},
	} {
		got, ok := creditsStart(c.markers, length)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestCardCredits(t *testing.T) {
	length := 100 * time.Minute
	ms := func(d time.Duration) int64 { return d.Milliseconds() }
	last := length - time.Minute // without fitting credits: the last minute
	for _, c := range []struct {
		name       string
		markers    []mediaserver.Marker
		start, end time.Duration
		ok         bool
	}{
		{"none", nil, last, length, true},
		{"intro only", []mediaserver.Marker{{Type: "intro", StartTimeOffset: 0, EndTimeOffset: 60000}}, last, length, true},
		// not final, a scene follows: the card runs over the credits
		{"scene after", []mediaserver.Marker{{Type: "credits", StartTimeOffset: ms(95 * time.Minute), EndTimeOffset: ms(98 * time.Minute)}},
			95 * time.Minute, 98 * time.Minute, true},
		{"last of two", []mediaserver.Marker{
			{Type: "credits", StartTimeOffset: ms(96 * time.Minute), EndTimeOffset: ms(97 * time.Minute)},
			{Type: "credits", StartTimeOffset: ms(98 * time.Minute), EndTimeOffset: ms(99 * time.Minute)},
		}, 98 * time.Minute, 99 * time.Minute, true},
		{"last one too short", []mediaserver.Marker{
			{Type: "credits", StartTimeOffset: ms(96 * time.Minute), EndTimeOffset: ms(97 * time.Minute)},
			{Type: "credits", StartTimeOffset: ms(99 * time.Minute), EndTimeOffset: ms(99*time.Minute + 20*time.Second)},
		}, 96 * time.Minute, 97 * time.Minute, true},
		{"too short", []mediaserver.Marker{{Type: "credits", StartTimeOffset: ms(97 * time.Minute), EndTimeOffset: ms(97*time.Minute + 25*time.Second)}}, last, length, true},
		{"too far from the end", []mediaserver.Marker{{Type: "credits", StartTimeOffset: ms(90 * time.Minute), EndTimeOffset: ms(96*time.Minute + 50*time.Second)}}, last, length, true},
		{"just within reach", []mediaserver.Marker{{Type: "credits", StartTimeOffset: ms(90 * time.Minute), EndTimeOffset: ms(97 * time.Minute)}}, 90 * time.Minute, 97 * time.Minute, true},
	} {
		start, end, ok := cardCredits(c.markers, length)
		if start != c.start || end != c.end || ok != c.ok {
			t.Errorf("%s: got %v %v %v, want %v %v %v", c.name, start, end, ok, c.start, c.end, c.ok)
		}
	}
	if _, _, ok := cardCredits(nil, 90*time.Second); ok {
		t.Error("card on a title of 90s")
	}
}

func TestCardOverlayArgs(t *testing.T) {
	card := &CardOverlay{List: "card.ffconcat", From: 6, To: 27}
	opts := Options{Mode: "full", NormalizeRes: 720, CardOverlay: card, Watermark: &Watermark{Path: "logo.png", Position: "tr", Opacity: 70, Size: 14}}
	args := strings.Join(buildArgs("in.mkv", 0, 30*time.Second, 0, 0, 0, 1, opts), " ")
	for _, want := range []string{"-f concat -safe 0 -i card.ffconcat", "[2:v]fps=25[card]", "[card]format=rgba", "between(t,6.00,27.00)"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	// a trailer's caption takes precedence
	opts.Caption = "caption.png"
	if args := strings.Join(buildArgs("in.mkv", 0, 30*time.Second, 0, 0, 0, 1, opts), " "); strings.Contains(args, "card.ffconcat") {
		t.Errorf("card besides the caption: %s", args)
	}
}

func TestCreditsList(t *testing.T) {
	dir := t.TempDir()
	p := &CreditsPanel{Pages: []string{"/p/0.png", "/p/1.png"}, Fades: [][]string{{"/p/f0a.png", "/p/f0b.png"}, {"/p/f1a.png", "/p/f1b.png"}}}
	list, err := creditsList(dir, p, 3*time.Second, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(list)
	text := string(data)
	if !strings.HasPrefix(text, "ffconcat version 1.0\nfile '/p/0.png'\nduration 11.000\nfile '/p/f0a.png'\nduration 0.040\nfile '/p/f0b.png'\nduration 0.040\nfile '/p/1.png'\nduration 7.920\n") {
		t.Fatalf("unexpected list:\n%s", text)
	}
	var total time.Duration
	for _, line := range strings.Split(text, "\n") {
		if d, ok := strings.CutPrefix(line, "duration "); ok {
			v, _ := time.ParseDuration(d + "s")
			total += v
		}
	}
	if total < 40*time.Second {
		t.Errorf("the list has to outlast the segment, got %v", total)
	}

	// one page, cut where the clock changes
	single, err := creditsList(dir, &CreditsPanel{Pages: []string{"/p/0.png"}}, 0, time.Minute, []time.Duration{20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(single)
	if want := "ffconcat version 1.0\nfile '/p/0.png'\nduration 20.000\nfile '/p/0.png'\nduration 50.000\nfile '/p/0.png'\n"; string(data) != want {
		t.Errorf("single page:\n%s", data)
	}
}

func TestSplitAt(t *testing.T) {
	in := []concatEntry{{"a", 10 * time.Second, false}, {"b", 40 * time.Millisecond, false}, {"c", 70 * time.Second, false}}
	got := splitAt(in, []time.Duration{0, 5 * time.Second, 10 * time.Second, 20 * time.Second, 75 * time.Second, 90 * time.Second})
	want := []concatEntry{{"a", 5 * time.Second, false}, {"a", 5 * time.Second, false}, {"b", 40 * time.Millisecond, false},
		{"c", 9960 * time.Millisecond, false}, {"c", 55 * time.Second, false}, {"c", 5040 * time.Millisecond, false}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %v, want %v", i, got[i], want[i])
		}
	}
	if b := minuteBreaks(time.Date(2026, 10, 14, 20, 14, 30, 0, time.Local), 2*time.Minute); len(b) != 2 || b[0] != 30*time.Second || b[1] != 90*time.Second {
		t.Errorf("minute breaks: %v", b)
	}
}

func TestClockList(t *testing.T) {
	var texts []string
	s := &Streamer{CacheDir: t.TempDir(), Clock: func(_ context.Context, text string, _ int) (string, error) {
		texts = append(texts, text)
		return "/c/" + strings.ReplaceAll(text, ":", "") + ".png", nil
	}}
	start := time.Date(2026, 10, 14, 20, 14, 30, 0, time.Local)
	list, err := s.clockList(context.Background(), start, 90*time.Second, 720)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(list)
	want := "ffconcat version 1.0\nfile '/c/2014.png'\nduration 30.000\nfile '/c/2015.png'\nduration 60.000\n" +
		"file '/c/2016.png'\nduration 10.000\nfile '/c/2016.png'\n"
	if string(data) != want {
		t.Errorf("clock list:\n%s", data)
	}
	if strings.Join(texts, " ") != "20:14 20:15 20:16" {
		t.Errorf("rendered %v", texts)
	}
}

func TestCreditsArgs(t *testing.T) {
	opts := Options{Mode: "full", NormalizeRes: 720, Watermark: &Watermark{Path: "logo.png", Position: "tr", Opacity: 70, Size: 14}}
	args := strings.Join(buildCreditsArgs("in.mkv", 90*time.Second, 60*time.Second, time.Hour, 0, 0, 1, 2, "list.ffconcat", "", opts), " ")
	for _, want := range []string{"-ss 90.000", "-f concat -safe 0 -i list.ffconcat", "-loop 1 -framerate 25 -t 2.500 -i logo.png",
		"fade=t=out:st=0.000:d=2.000:alpha=1", "eval=frame", "-output_ts_offset 3600.000", "-map 0:1"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	// joined during the credits: no logo, the picture is small right away
	args = strings.Join(buildCreditsArgs("in.mkv", 95*time.Second, 55*time.Second, 0, 0, 0, 1, -3, "list.ffconcat", "", opts), " ")
	if strings.Contains(args, "logo.png") {
		t.Errorf("logo during the credits: %s", args)
	}
}

// TestCreditsSegment runs ffmpeg with the credits filter on a white video over
// a black panel: full size before the credits, then only in the top left corner.
func TestCreditsSegment(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x360:r=25:d=10", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "10", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	page := filepath.Join(dir, "page.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=1280x720", "-frames:v", "1", page)
	logo := filepath.Join(dir, "logo.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=red:s=200x100", "-frames:v", "1", logo)
	p := &CreditsPanel{Pages: []string{page, page}, Fades: [][]string{{page}, {page}}}
	list, err := creditsList(dir, p, 3*time.Second, 6*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	// credits from 4s, the segment from 2s: 2s full size, 1s zoom, then small
	opts := Options{Mode: "full", NormalizeRes: 720, Watermark: &Watermark{Path: logo, Position: "tr", Opacity: 100, Size: 14}}
	args := buildCreditsArgs(movie, 2*time.Second, 6*time.Second, 0, 0, 0, 1, 2, list, "", opts)
	out := filepath.Join(dir, "credits.ts")
	args[len(args)-1] = out
	ffmpeg(t, args[2:]...)
	video := CreditsVideo(1280, 720)
	inside := [4]int{video.Min.X + 20, video.Min.Y + 20, 200, 100}
	outside := [4]int{900, 500, 200, 100}
	for _, at := range []struct {
		frame          int
		inside, beside bool
	}{{20, true, true}, {100, true, false}, {140, true, false}} {
		if got := brightIn(t, out, at.frame, inside); got != at.inside {
			t.Errorf("frame %d: picture in the corner = %v, want %v", at.frame, got, at.inside)
		}
		if got := brightIn(t, out, at.frame, outside); got != at.beside {
			t.Errorf("frame %d: picture beside the corner = %v, want %v", at.frame, got, at.beside)
		}
	}
}

// TestCreditsZoomSettles follows the picture through the zoom: its corner only
// moves one way, and once the picture has its final size it stays put (it used
// to jump a couple of pixels into place at the very end).
func TestCreditsZoomSettles(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	for _, tc := range []struct {
		name, size string
		aspect     float64
	}{{"16:9", "640x360", 0}, {"4:3", "480x360", 4.0 / 3}, {"scope", "860x360", 2.39}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			movie := filepath.Join(dir, "movie.mkv")
			ffmpeg(t, "-f", "lavfi", "-i", "color=white:s="+tc.size+":r=25:d=5", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
				"-t", "5", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
			page := filepath.Join(dir, "page.png")
			ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=1280x720", "-frames:v", "1", page)
			list, err := creditsList(dir, &CreditsPanel{Pages: []string{page}}, 3*time.Second, 3*time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			// credits after 1s: zoom from frame 25 to 50
			args := buildCreditsArgs(movie, 0, 3*time.Second, 0, 0, 0, 1, 1, list, "", Options{Mode: "full", NormalizeRes: 720, Aspect: tc.aspect})
			out := filepath.Join(dir, "credits.ts")
			args[len(args)-1] = out
			ffmpeg(t, args[2:]...)
			raw, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", out, "-frames:v", "70",
				"-f", "rawvideo", "-pix_fmt", "gray", "pipe:1").Output()
			if err != nil {
				t.Fatal(err)
			}
			const w, h = 1280, 720
			var boxes []image.Rectangle
			for f := 0; (f+1)*w*h <= len(raw); f++ {
				px := raw[f*w*h : (f+1)*w*h]
				b := image.Rectangle{Min: image.Pt(w, h)} // not image.Rect, which sorts the corners
				for y := range h {
					for x := range w {
						if px[y*w+x] > 128 {
							b.Min.X, b.Min.Y = min(b.Min.X, x), min(b.Min.Y, y)
							b.Max.X, b.Max.Y = max(b.Max.X, x+1), max(b.Max.Y, y+1)
						}
					}
				}
				boxes = append(boxes, b)
			}
			if len(boxes) < 60 {
				t.Fatalf("only %d frames", len(boxes))
			}
			first, last := boxes[0], boxes[len(boxes)-1]
			for i := 1; i < len(boxes); i++ {
				a, b := boxes[i-1], boxes[i]
				if (b.Min.X-a.Min.X)*(last.Min.X-first.Min.X) < 0 || (b.Min.Y-a.Min.Y)*(last.Min.Y-first.Min.Y) < 0 {
					t.Errorf("frame %d: corner moves back, %v → %v", i, a.Min, b.Min)
				}
				if a.Size() == last.Size() && a != b {
					t.Errorf("frame %d: picture moves after reaching its size, %v → %v", i, a, b)
				}
			}
		})
	}
}

// TestServeCreditsCard streams a title whose credits are not final (a scene
// follows): the picture stays full size and the small card shows over the
// credits only.
func TestServeCreditsCard(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=640x360:r=25:d=150", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "150", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	page := filepath.Join(dir, "card.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=300x120", "-frames:v", "1", page)

	mux := http.NewServeMux()
	mux.HandleFunc("/library/metadata/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","Media":[{"Part":[{"key":"/library/parts/1","Stream":[
			{"streamType":1,"index":0,"codec":"h264"},{"streamType":2,"index":1,"codec":"aac"}]}]}],
			"Marker":[{"type":"credits","startTimeOffset":100000,"endTimeOffset":136000}]}]}}`)
	})
	mux.HandleFunc("/library/parts/1", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, movie) })
	plexSrv := httptest.NewServer(mux)
	defer plexSrv.Close()

	// tuned in 2:10 after the start: the card is there (credits from 1:40), until
	// it has faded out shortly before the scene at 2:16
	start := time.Now().Add(-130 * time.Second)
	prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(150 * time.Second).UnixMilli(), RatingKey: "1"}
	rendered := make(chan time.Time, 1)
	s := &Streamer{
		FFmpeg: "ffmpeg",
		Server: func(context.Context) (mediaserver.Server, error) {
			return plex.NewBackend(&plex.Server{BaseURL: plexSrv.URL}, nil), nil
		},
		Program: func(context.Context, int64, time.Time) (*store.Program, error) { return prog, nil },
		Media: func(context.Context, string) (*store.Media, error) {
			return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", DurationMs: 150000, PartKey: "/library/parts/1"}, nil
		},
		Credits: func(context.Context, *store.Channel, *store.Program, time.Time, image.Rectangle, image.Rectangle, int) (*CreditsPanel, error) {
			t.Error("credits shrunk although a scene follows")
			return nil, nil
		},
		CreditsCard: func(_ context.Context, _ *store.Channel, _ *store.Program, at time.Time, _ int) (*CreditsPanel, error) {
			rendered <- at
			return &CreditsPanel{Pages: []string{page}}, nil
		},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		CacheDir: dir,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx)
	s.Serve(rec, req, &store.Channel{ID: 1, Name: "Test"}, Options{Mode: "full", NormalizeRes: 720, Credits: true})

	select {
	case at := <-rendered:
		if d := at.Sub(start.Add(100 * time.Second)); d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("card rendered for %v, want the credits start", at)
		}
	default:
		t.Fatal("card was not rendered")
	}
	out := filepath.Join(dir, "out.ts")
	os.WriteFile(out, rec.Body.Bytes(), 0o644)
	if left, _ := filepath.Glob(filepath.Join(dir, "creditslist-*")); len(left) != 0 {
		t.Errorf("play list not removed: %v", left)
	}
	// placed like the caption of trailers: bottom left
	card := [4]int{80, 600, 100, 50}
	for _, at := range []struct {
		frame int
		shown bool
	}{{25, true}, {75, true}, {150, false}, {200, false}} {
		if got := brightIn(t, out, at.frame, card); got != at.shown {
			t.Errorf("frame %d: card shown = %v, want %v", at.frame, got, at.shown)
		}
	}
}

// TestCreditsSegmentLateVideo: the video of many files starts a little after
// their audio. Until its first frame the panel must not show on its own.
func TestCreditsSegmentLateVideo(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	// picture and sound made separately and joined without encoding: ffmpeg 8
	// sometimes hangs when it encodes with -itsoffset
	video, audio := filepath.Join(dir, "video.mkv"), filepath.Join(dir, "audio.mka")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x360:r=25:d=10", "-c:v", "libx264", "-preset", "ultrafast", video)
	ffmpeg(t, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-t", "10", "-c:a", "aac", audio)
	ffmpeg(t, "-i", audio, "-itsoffset", "0.2", "-i", video, "-map", "1:v", "-map", "0:a", "-c", "copy", movie)
	page := filepath.Join(dir, "page.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=1280x720", "-frames:v", "1", page)
	list, err := creditsList(dir, &CreditsPanel{Pages: []string{page}}, 3*time.Second, 6*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := buildCreditsArgs(movie, 0, 6*time.Second, 0, 0, 0, 1, 2, list, "", Options{Mode: "full", NormalizeRes: 720})
	out := filepath.Join(dir, "credits.ts")
	args[len(args)-1] = out
	ffmpeg(t, args[2:]...)
	for frame := range 8 {
		if got := level(t, out, frame, [4]int{900, 500, 200, 100}); got != "white" {
			t.Errorf("frame %d beside the corner: %s, want white (the picture, not the panel)", frame, got)
		}
	}
}

// TestServeCredits streams a title tuned into shortly before its credits: Plex
// reports the credits marker, the title plays in two parts and ends shrunk.
func TestServeCredits(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=640x360:r=25:d=30", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "30", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", movie)
	page := filepath.Join(dir, "page.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=1280x720", "-frames:v", "1", page)
	clock := filepath.Join(dir, "clock.png")
	ffmpeg(t, "-f", "lavfi", "-i", "color=white:s=120x60", "-frames:v", "1", clock)

	mux := http.NewServeMux()
	mux.HandleFunc("/library/metadata/1", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("includeMarkers") != "1" {
			t.Error("markers not requested")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","Media":[{"Part":[{"key":"/library/parts/1","Stream":[
			{"streamType":1,"index":0,"codec":"h264"},{"streamType":2,"index":1,"codec":"aac"}]}]}],
			"Marker":[{"type":"credits","startTimeOffset":8000,"endTimeOffset":30000,"final":true}]}]}}`)
	})
	mux.HandleFunc("/library/parts/1", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, movie) })
	plexSrv := httptest.NewServer(mux)
	defer plexSrv.Close()

	// tuned in 4s after the start: 2s as usual, then the credits segment (from 8s)
	start := time.Now().Add(-4 * time.Second)
	prog := &store.Program{ChannelID: 1, Start: start.UnixMilli(), Stop: start.Add(30 * time.Second).UnixMilli(), RatingKey: "1"}
	rendered := make(chan time.Time, 1)
	s := &Streamer{
		FFmpeg: "ffmpeg",
		Server: func(context.Context) (mediaserver.Server, error) {
			return plex.NewBackend(&plex.Server{BaseURL: plexSrv.URL}, nil), nil
		},
		Program: func(context.Context, int64, time.Time) (*store.Program, error) { return prog, nil },
		Media: func(context.Context, string) (*store.Media, error) {
			return &store.Media{RatingKey: "1", Type: "movie", Title: "Film", DurationMs: 30000, PartKey: "/library/parts/1"}, nil
		},
		Credits: func(_ context.Context, _ *store.Channel, _ *store.Program, at time.Time, video, _ image.Rectangle, _ int) (*CreditsPanel, error) {
			rendered <- at
			return &CreditsPanel{Pages: []string{page}}, nil
		},
		Clock:    func(context.Context, string, int) (string, error) { return clock, nil },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		CacheDir: dir,
	}
	// 10s of output are needed; the buffer delivers most of it at once, the
	// rest in real time (more on a busy machine)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/stream/1", nil).WithContext(ctx)
	s.Serve(rec, req, &store.Channel{ID: 1, Name: "Test"}, Options{Mode: "full", NormalizeRes: 720, Credits: true})

	select {
	case at := <-rendered:
		if d := at.Sub(start.Add(8 * time.Second)); d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("panel rendered for %v, want the credits start", at)
		}
	default:
		t.Fatal("panel was not rendered")
	}
	out := filepath.Join(dir, "out.ts")
	os.WriteFile(out, rec.Body.Bytes(), 0o644)
	if left, _ := filepath.Glob(filepath.Join(dir, "creditslist-*")); len(left) != 0 {
		t.Errorf("play list not removed: %v", left)
	}
	// The output starts about 4s into the title: full size until the credits
	// (4s in), small from 5s on.
	video := CreditsVideo(1280, 720)
	inside := [4]int{video.Min.X + 20, video.Min.Y + 20, 200, 100}
	outside := [4]int{900, 500, 200, 100}
	clockBox := [4]int{1280 - video.Min.X - 80, video.Min.Y + 10, 60, 30} // inside the white clock
	if !brightIn(t, out, 250, clockBox) {
		t.Error("no clock at the top right")
	}
	for _, at := range []struct {
		frame  int
		beside bool
	}{{10, true}, {75, true}, {150, false}, {250, false}} {
		if !brightIn(t, out, at.frame, inside) {
			t.Errorf("frame %d: no picture in the corner", at.frame)
		}
		if got := brightIn(t, out, at.frame, outside); got != at.beside {
			t.Errorf("frame %d: picture beside the corner = %v, want %v", at.frame, got, at.beside)
		}
	}
}
