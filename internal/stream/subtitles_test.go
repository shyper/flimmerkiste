package stream

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

func TestForcedSubtitle(t *testing.T) {
	audio := mediaserver.Stream{StreamType: 2, Index: 1, LanguageCode: "deu"}
	full := mediaserver.Stream{StreamType: 3, Index: 2, Codec: "pgs", LanguageCode: "deu"}
	forcedEng := mediaserver.Stream{StreamType: 3, Index: 3, Codec: "pgs", LanguageCode: "eng", Forced: true}
	forcedDeu := mediaserver.Stream{StreamType: 3, Index: 4, Codec: "pgs", LanguageCode: "deu", Forced: true}
	byTitle := mediaserver.Stream{StreamType: 3, Index: 5, Codec: "subrip", LanguageCode: "deu", TrackTitle: "Deutsch (Erzwungen)"}
	external := mediaserver.Stream{StreamType: 3, Codec: "srt", LanguageCode: "deu", Forced: true, Key: "/library/streams/77"}
	externalImage := mediaserver.Stream{StreamType: 3, Codec: "pgs", LanguageCode: "deu", Forced: true, Key: "/library/streams/78"}
	cases := []struct {
		name    string
		streams []mediaserver.Stream
		audio   int
		want    int // index, -1 = none, -2 = the external track
	}{
		{"forced in audio language", []mediaserver.Stream{audio, full, forcedEng, forcedDeu}, 1, 4},
		{"only full subtitles", []mediaserver.Stream{audio, full}, 1, -1},
		{"forced in another language", []mediaserver.Stream{audio, forcedEng}, 1, -1},
		{"recognised by track title", []mediaserver.Stream{audio, byTitle}, 1, 5},
		{"external text file", []mediaserver.Stream{audio, external}, 1, -2},
		{"external picture file is not supported", []mediaserver.Stream{audio, externalImage}, 1, -1},
		{"unknown audio language, one forced track", []mediaserver.Stream{forcedEng}, -1, 3},
		{"unknown audio language, several forced tracks", []mediaserver.Stream{forcedEng, forcedDeu}, -1, -1},
	}
	for _, c := range cases {
		got := forcedSubtitle(c.streams, c.audio)
		switch {
		case c.want == -1 && got != nil:
			t.Errorf("%s: got %+v, want none", c.name, got)
		case c.want == -2 && (got == nil || got.Key == ""):
			t.Errorf("%s: got %+v, want the external track", c.name, got)
		case c.want >= 0 && (got == nil || got.Index != c.want || got.Key != ""):
			t.Errorf("%s: got %+v, want index %d", c.name, got, c.want)
		}
	}
}

func TestSubtitleFilter(t *testing.T) {
	got := subtitleFilter(&Subtitles{Index: -1, File: "/data/a:b,c'd.srt"}, "0:0", "sub", 90*time.Second)
	want := `[0:0]setpts=PTS+90.000/TB,subtitles=filename=/data/a\\:b\,c\\\'d.srt,setpts=PTS-90.000/TB[sub]`
	if got != want {
		t.Errorf("text:\n got %s\nwant %s", got, want)
	}
	if got := subtitleFilter(&Subtitles{Index: 3}, "0:0", "sub", 0); got != "[0:0][0:3]overlay=x=(W-w)/2:y=H-h:eof_action=pass[sub]" {
		t.Errorf("picture: %s", got)
	}
}

// TestBurnInSubtitles runs ffmpeg with both kinds of subtitles on a black video
// and checks the frames: a white box (PGS) and white text (SRT) appear exactly
// while the subtitle is due, also when playback starts mid-file.
func TestBurnInSubtitles(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	dir := t.TempDir()
	sup := filepath.Join(dir, "forced.sup")
	if err := os.WriteFile(sup, pgsBox(640, 360, 220, 280, 200, 40, 4*time.Second, 7*time.Second), 0o644); err != nil {
		t.Fatal(err)
	}
	movie := filepath.Join(dir, "movie.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "color=black:s=640x360:r=25:d=10", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-i", sup, "-map", "0:v", "-map", "1:a", "-map", "2:s", "-t", "10", "-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-c:s", "copy", "-disposition:s:0", "forced", movie)
	srt := filepath.Join(dir, "forced, de.srt") // comma: exercises the filter escaping
	os.WriteFile(srt, []byte("1\n00:00:04,000 --> 00:00:07,000\nIIIIIIIIIIII\n"), 0o644)

	for _, c := range []struct {
		name string
		sub  *Subtitles
		box  [4]int // area checked in the 1280x720 output
	}{
		{"pgs", &Subtitles{Index: 2}, [4]int{460, 580, 380, 40}},
		{"srt", &Subtitles{Index: -1, File: srt}, [4]int{560, 650, 160, 50}},
	} {
		// starts 3s in: the subtitle is due from 1s to 4s of the output
		opts := Options{Mode: "full", NormalizeRes: 720, Subtitles: c.sub}
		args := buildArgs(movie, 3*time.Second, 5*time.Second, 0, 5*time.Second, 0, 1, opts)
		out := filepath.Join(dir, c.name+".ts")
		args[len(args)-1] = out
		ffmpeg(t, args[2:]...)
		for _, at := range []struct {
			frame   int // 25 fps
			visible bool
		}{{10, false}, {62, true}, {115, false}} {
			if got := brightIn(t, out, at.frame, c.box); got != at.visible {
				t.Errorf("%s at frame %d: visible = %v, want %v", c.name, at.frame, got, at.visible)
			}
		}
	}
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	// a hanging ffmpeg fails the test instead of the whole run running into its timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", append([]string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}, args...)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, b)
	}
}

// brightIn reports whether a frame has bright pixels inside box (x, y, w, h).
// Frames are picked by number: seeking in MPEG-TS is not frame accurate.
func brightIn(t *testing.T, file string, frame int, box [4]int) bool {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", file, "-frames:v", "1",
		"-vf", fmt.Sprintf(`select=eq(n\,%d),crop=%d:%d:%d:%d,format=gray`, frame, box[2], box[3], box[0], box[1]),
		"-f", "rawvideo", "pipe:1")
	px, err := cmd.Output()
	if err != nil || len(px) == 0 {
		t.Fatalf("read frame %d: %v", frame, err)
	}
	n := 0
	for _, p := range px {
		if p > 180 {
			n++
		}
	}
	return n > len(px)/50
}

// pgsBox builds a PGS (Blu-ray) subtitle stream showing a white box from start to end.
func pgsBox(w, h, x, y, bw, bh int, start, end time.Duration) []byte {
	var out bytes.Buffer
	seg := func(pts time.Duration, typ byte, data []byte) {
		out.WriteString("PG")
		binary.Write(&out, binary.BigEndian, uint32(pts.Seconds()*90000))
		binary.Write(&out, binary.BigEndian, uint32(0))
		out.WriteByte(typ)
		binary.Write(&out, binary.BigEndian, uint16(len(data)))
		out.Write(data)
	}
	be16 := func(v int) []byte { return []byte{byte(v >> 8), byte(v)} }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	window := cat([]byte{1, 0}, be16(x), be16(y), be16(bw), be16(bh))

	clear := func(at time.Duration, num int) {
		seg(at, 0x16, cat(be16(w), be16(h), []byte{0x10}, be16(num), []byte{0, 0, 0, 0}))
		seg(at, 0x17, window)
		seg(at, 0x80, nil)
	}
	// an empty display set at 0, so the stream starts with the video
	clear(0, 0)
	// display set 1: the box
	seg(start, 0x16, cat(be16(w), be16(h), []byte{0x10}, be16(1), []byte{0x80, 0, 0, 1}, be16(0), []byte{0, 0}, be16(x), be16(y)))
	seg(start, 0x17, window)
	seg(start, 0x14, []byte{0, 0, 1, 235, 128, 128, 255}) // palette entry 1: opaque white
	var rle []byte
	for i := 0; i < bh; i++ {
		rle = append(rle, 0, 0xC0|byte(bw>>8), byte(bw), 1, 0, 0) // bw pixels of colour 1, end of line
	}
	n := len(rle) + 4
	seg(start, 0x15, cat(be16(0), []byte{0, 0xC0, byte(n >> 16), byte(n >> 8), byte(n)}, be16(bw), be16(bh), rle))
	seg(start, 0x80, nil)
	// display set 2: an empty composition clears it
	clear(end, 2)
	return out.Bytes()
}
