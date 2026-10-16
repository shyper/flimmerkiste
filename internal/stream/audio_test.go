package stream

import (
	"strings"
	"testing"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

func TestChooseStreamsReportsAudioCodec(t *testing.T) {
	streams := []mediaserver.Stream{
		{StreamType: 1, Index: 0, Codec: "h264"},
		{StreamType: 2, Index: 1, Codec: "ac3", LanguageCode: "eng", Default: true},
		{StreamType: 2, Index: 2, Codec: "dca", LanguageCode: "deu"},
	}
	v, a, codec := chooseStreams(streams, []string{"deu"})
	if v != 0 || a != 2 || codec != "dca" {
		t.Fatalf("got %d %d %q", v, a, codec)
	}
	if _, a, codec = chooseStreams(streams, []string{"fra"}); a != 1 || codec != "ac3" {
		t.Fatalf("fallback: got %d %q", a, codec)
	}
}

func TestNeedsAudioTranscode(t *testing.T) {
	for codec, want := range map[string]bool{"dca": true, "DTS": true, "truehd": true, "aac": false, "ac3": false, "eac3": false, "": false} {
		if got := needsAudioTranscode(codec); got != want {
			t.Errorf("%q: got %v", codec, got)
		}
	}
}

func TestAudioModeConvertsOnlyAudio(t *testing.T) {
	args := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 2, Options{Mode: "audio"}), " ")
	if !strings.Contains(args, "-c:v copy -c:a aac") || !strings.Contains(args, "-map 0:2") {
		t.Fatal(args)
	}
}

func TestFullModeOnGPU(t *testing.T) {
	sw := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720}), " ")
	if strings.Contains(sw, "vaapi") || !strings.Contains(sw, "libx264") {
		t.Fatal(sw)
	}
	for _, wm := range []*Watermark{nil, {Path: "logo.png", Position: "tr", Opacity: 80, Size: 10}} {
		gpu := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, VAAPI: "/dev/dri/renderD128", GPUEncoder: GPUEncoder{LowPower: true}, Watermark: wm}), " ")
		if strings.Contains(gpu, "-hwaccel") {
			t.Errorf("GPU decoding without GPUDecode: %s", gpu)
		}
		for _, want := range []string{"-init_hw_device vaapi=va:/dev/dri/renderD128 -filter_hw_device va", "format=nv12,hwupload[v]", "-c:v h264_vaapi -low_power 1"} {
			if !strings.Contains(gpu, want) {
				t.Errorf("missing %q in %s", want, gpu)
			}
		}
		if strings.Contains(gpu, "libx264") || strings.Index(gpu, "-init_hw_device") > strings.Index(gpu, "-i in.mkv") {
			t.Error(gpu)
		}
	}
	// Copy mode never touches the GPU, even when one is configured.
	dec := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "full", NormalizeRes: 720, VAAPI: "/dev/dri/renderD128", GPUDecode: true}), " ")
	if !strings.Contains(dec, "-filter_hw_device va -hwaccel vaapi -hwaccel_device va") {
		t.Errorf("GPU decoding missing: %s", dec)
	}
	if cp := strings.Join(buildArgs("in.mkv", 0, 60e9, 0, 0, 0, 1, Options{Mode: "copy", VAAPI: "/dev/dri/renderD128", GPUDecode: true}), " "); strings.Contains(cp, "vaapi") {
		t.Fatal(cp)
	}
}

func TestGPUEncoderRateControl(t *testing.T) {
	vbr := strings.Join(GPUEncoder{LowPower: true}.args(720), " ")
	cqp := strings.Join(GPUEncoder{LowPower: true, CQP: true}.args(720), " ")
	if !strings.Contains(vbr, "-rc_mode VBR -b:v 3M") || strings.Contains(vbr, "-qp") {
		t.Error(vbr)
	}
	// Drivers offering only CQP reject any bitrate option.
	if !strings.Contains(cqp, "-rc_mode CQP -qp 23") || strings.Contains(cqp, "-b:v") || strings.Contains(cqp, "maxrate") {
		t.Error(cqp)
	}
}

func TestEncoderError(t *testing.T) {
	out := "[ac3 @ 0x1] error decoding the audio block\n[h264_vaapi @ 0x2] Driver does not support VBR RC mode (supported modes: CQP).\nError while filtering: Invalid argument\n"
	if got := encoderError(out); !strings.Contains(got, "does not support VBR") {
		t.Fatal(got)
	}
	if got := encoderError("a\nlast line"); got != "last line" {
		t.Fatal(got)
	}
}
