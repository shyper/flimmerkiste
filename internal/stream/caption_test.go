package stream

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCaptionWindows(t *testing.T) {
	s := time.Second
	cases := []struct {
		offset, length time.Duration
		want           [][2]float64
	}{
		{0, 90 * s, [][2]float64{{1.5, 88.5}}},        // whole trailer
		{0, 12 * s, [][2]float64{{1.5, 10.5}}},        // short trailer
		{0, 4 * s, nil},                               // too short
		{30 * s, 60 * s, [][2]float64{{-28.5, 58.5}}}, // joined mid-trailer: from the start of the stream
		{0, 5 * s, nil},                               // too short
		{87 * s, 3 * s, nil},                          // joined in the last seconds
	}
	for _, c := range cases {
		if got := captionWindows(c.offset, c.length); !reflect.DeepEqual(got, c.want) {
			t.Errorf("offset %v length %v: got %v, want %v", c.offset, c.length, got, c.want)
		}
	}
}

func TestCaptionArgs(t *testing.T) {
	opts := Options{Mode: "full", NormalizeRes: 720, Caption: "cap.png"}
	args := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, opts), " ")
	for _, want := range []string{"-loop 1 -framerate 25 -t 60.000 -i cap.png", "[1:v]format=rgba,split=1[cap0]",
		"enable='between(t,1.50,58.50)'", "[capd0]null,format=yuv420p[v]"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	// with the logo the caption is the third input and goes on top of it
	opts.Watermark = &Watermark{Path: "logo.png", Position: "tr", Opacity: 70, Size: 14}
	args = strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, opts), " ")
	if !strings.Contains(args, "[2:v]format=rgba,split") || !strings.Contains(args, "[wmd][cap0f]overlay") {
		t.Error(args)
	}
	// copy mode cannot draw anything
	opts.Mode = "copy"
	if args = strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, opts), " "); strings.Contains(args, "cap.png") {
		t.Error(args)
	}
}
