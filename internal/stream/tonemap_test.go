package stream

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

func TestToneMapFilters(t *testing.T) {
	x := toneMapFilters("smpte2084", true, true)
	if !strings.HasPrefix(x, ",setparams=color_primaries=bt2020:color_trc=smpte2084:") || !strings.Contains(x, "tonemapx=tonemap=bt2390") {
		t.Errorf("tonemapx: %s", x)
	}
	if z := toneMapFilters("ARIB-STD-B67", false, true); !strings.Contains(z, "color_trc=arib-std-b67") || !strings.Contains(z, "tonemap=tonemap=mobius") {
		t.Errorf("zscale: %s", z)
	}
	if n := toneMapFilters("smpte2084", false, false); n != "" {
		t.Errorf("without filters: %s", n)
	}
	hdr := mediaserver.Stream{StreamType: mediaserver.StreamVideo, Index: 0, ColorTransfer: "smpte2084"}
	streams := []mediaserver.Stream{{StreamType: mediaserver.StreamAudio, Index: 1}, hdr}
	if st := videoStream(streams, 0); st == nil || !st.HDR() {
		t.Errorf("video stream: %+v", st)
	}
	if (mediaserver.Stream{ColorTransfer: "bt709"}).HDR() {
		t.Error("SDR taken for HDR")
	}
}

// The tone mapping follows the scaling, in the film and in the credits picture.
func TestToneMapArgs(t *testing.T) {
	tm := toneMapFilters("smpte2084", true, false)
	opts := Options{Mode: "full", NormalizeRes: 720, ToneMap: tm}
	args := strings.Join(buildArgs("in.mkv", 0, time.Hour, 0, 10*time.Second, 0, 1, opts), " ")
	if !strings.Contains(args, "setsar=1"+tm+videoPad+",format=yuv420p[v]") {
		t.Errorf("film: %s", args)
	}
	credits := strings.Join(buildCreditsArgs("in.mkv", 0, time.Minute, 0, 10*time.Second, 0, 1, 1, "list.txt", "", opts), " ")
	if !strings.Contains(credits, "setsar=1"+tm+"[full]") {
		t.Errorf("credits: %s", credits)
	}
	opts.Mode = "copy"
	if args := strings.Join(buildArgs("in.mkv", 0, time.Hour, 0, 0, 0, 1, opts), " "); strings.Contains(args, "tonemap") {
		t.Errorf("copy mode keeps HDR as it is: %s", args)
	}
}

// TestToneMapPicture streams HDR10 and HLG clips of a red picture in full mode:
// tone mapped, red stays a saturated red; without, it turns pale brown.
func TestToneMapPicture(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_E2E") != "1" {
		t.Skip("set FLIMMERKISTE_E2E=1 to run")
	}
	s := &Streamer{FFmpeg: "ffmpeg", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if s.toneMap(context.Background(), "smpte2084") == "" {
		t.Skip("this ffmpeg has neither tonemapx nor zscale")
	}
	dir := t.TempDir()
	for _, c := range []struct{ name, trc, zscale string }{{"hdr10", "smpte2084", "smpte2084"}, {"hlg", "arib-std-b67", "arib-std-b67"}} {
		src := filepath.Join(dir, c.name+".mkv")
		ffmpeg(t, "-f", "lavfi", "-i", "color=c=0xC81E1E:s=1920x1080:r=25:d=2", "-vf",
			"zscale=t="+c.zscale+":p=bt2020:m=bt2020nc:tin=bt709:pin=bt709:min=bt709:npl=100,format=yuv420p10le",
			"-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error:colorprim=bt2020:transfer="+c.trc+":colormatrix=bt2020nc", src)
		for _, tm := range []string{s.toneMap(context.Background(), c.trc), ""} {
			opts := Options{Mode: "full", NormalizeRes: 720, ToneMap: tm}
			args := buildArgs(src, 0, time.Second, 0, 10*time.Second, 0, -1, opts)
			out, err := exec.Command("ffmpeg", args...).Output()
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			ts := filepath.Join(dir, c.name+".ts")
			os.WriteFile(ts, out, 0o644)
			raw, err := exec.Command("ffmpeg", "-v", "error", "-i", ts, "-frames:v", "1", "-vf", "scale=1:1:flags=area", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
			if err != nil || len(raw) < 3 {
				t.Fatalf("%s: frame: %v", c.name, err)
			}
			// saturation: how far red stands out (the source: 200 against 30)
			r, g, b := int(raw[0]), int(raw[1]), int(raw[2])
			sat := r - max(g, b)
			if tm != "" && sat < 100 {
				t.Errorf("%s tone mapped: rgb(%d,%d,%d), want a saturated red", c.name, r, g, b)
			}
			if tm == "" && sat > 70 {
				t.Errorf("%s without tone mapping is expected to look pale, got rgb(%d,%d,%d)", c.name, r, g, b)
			}
			t.Logf("%s tone mapped=%v: rgb(%d,%d,%d)", c.name, tm != "", r, g, b)
		}
	}
}
