package stream

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
)

// The Raspberry Pi encoder takes frames from memory: no VAAPI device, no
// upload to the GPU, and the stream headers at every keyframe.
func TestV4L2Args(t *testing.T) {
	opts := Options{Mode: "full", NormalizeRes: 720, VAAPI: "/dev/video11", GPUEncoder: GPUEncoder{V4L2: true}, GPUDecode: true}
	args := strings.Join(buildArgs("in.mkv", time.Minute, time.Hour, 0, 10*time.Second, 0, 1, opts), " ")
	for _, want := range []string{"-c:v h264_v4l2m2m", "-b:v 3M", "-g 50", "-bsf:v dump_extra=freq=keyframe", "format=yuv420p[v]"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	for _, not := range []string{"vaapi", "hwupload", "-hwaccel", "libx264"} {
		if strings.Contains(args, not) {
			t.Errorf("unexpected %q in %s", not, args)
		}
	}
	// the same in the credits graph
	credits := strings.Join(buildCreditsArgs("in.mkv", 0, time.Minute, 0, 10*time.Second, 0, 1, 1, "list.txt", "", opts), " ")
	if !strings.Contains(credits, "h264_v4l2m2m") || strings.Contains(credits, "hwupload") || strings.Contains(credits, "vaapi") {
		t.Errorf("credits: %s", credits)
	}
	// VAAPI keeps uploading to the GPU
	opts.GPUEncoder = GPUEncoder{}
	opts.VAAPI = "/dev/dri/renderD128"
	if args := strings.Join(buildArgs("in.mkv", 0, time.Hour, 0, 0, 0, 1, opts), " "); !strings.Contains(args, "hwupload") || !strings.Contains(args, "h264_vaapi") {
		t.Errorf("vaapi: %s", args)
	}
}

// fakeFFmpeg writes a script standing in for ffmpeg: VAAPI fails, V4L2
// succeeds unless v4l2ok is false.
func fakeFFmpeg(t *testing.T, v4l2ok bool) string {
	t.Helper()
	exit := "0"
	if !v4l2ok {
		exit = "1"
	}
	p := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\ncase \"$*\" in\n*h264_v4l2m2m*) echo '[h264_v4l2m2m] Could not find a valid device' >&2; exit " + exit + ";;\n" +
		"*) echo '[h264_vaapi] No VA display found' >&2; exit 1;;\nesac\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProbeFallsBackToV4L2(t *testing.T) {
	dir := t.TempDir()
	render, video := filepath.Join(dir, "renderD128"), filepath.Join(dir, "video11")
	for _, f := range []string{render, video} {
		os.WriteFile(f, nil, 0o666)
	}
	// a Raspberry Pi: a render node without VAAPI, and the V4L2 encoder
	v := &VAAPI{Device: render, V4L2: video, FFmpeg: fakeFFmpeg(t, true)}
	ok, status := v.Available(context.Background())
	if !ok || !v.Encoder().V4L2 || v.Path() != video || !strings.Contains(status.In(i18n.EN), "V4L2, Raspberry Pi") {
		t.Fatalf("v4l2: %v %s %+v %s", ok, status.In(i18n.EN), v.Encoder(), v.Path())
	}
	if dec, why := v.Decode(context.Background()); dec || !strings.Contains(why.In(i18n.EN), "only encodes") {
		t.Errorf("decode: %v %s", dec, why.In(i18n.EN))
	}

	// both fail: software, and the status names both
	v = &VAAPI{Device: render, V4L2: video, FFmpeg: fakeFFmpeg(t, false)}
	ok, status = v.Available(context.Background())
	if ok || !strings.Contains(status.In(i18n.EN), "No VA display") || !strings.Contains(status.In(i18n.EN), "V4L2: exit status 1") {
		t.Errorf("none: %v %s", ok, status.In(i18n.EN))
	}

	// no V4L2 device: only the VAAPI status, as before
	v = &VAAPI{Device: render, V4L2: filepath.Join(dir, "video99"), FFmpeg: fakeFFmpeg(t, true)}
	if ok, status = v.Available(context.Background()); ok || strings.Contains(status.In(i18n.EN), "V4L2") {
		t.Errorf("without device: %v %s", ok, status.In(i18n.EN))
	}
}

// NVIDIA: frames from memory like the Raspberry Pi, with NVENC's own options.
func TestNVENCArgs(t *testing.T) {
	opts := Options{Mode: "full", NormalizeRes: 1080, VAAPI: "/dev/nvidia0", GPUEncoder: GPUEncoder{NVENC: true}, GPUDecode: true}
	args := strings.Join(buildArgs("in.mkv", 0, time.Hour, 0, 10*time.Second, 0, 1, opts), " ")
	for _, want := range []string{"-c:v h264_nvenc", "-preset p4", "-rc vbr", "-b:v 6M", "-maxrate 8M", "format=yuv420p[v]"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	for _, not := range []string{"vaapi", "hwupload", "-hwaccel", "libx264"} {
		if strings.Contains(args, not) {
			t.Errorf("unexpected %q in %s", not, args)
		}
	}
}

// fakeFFmpegWith fails every encoder except those named in ok.
func fakeFFmpegWith(t *testing.T, ok ...string) string {
	t.Helper()
	script := "#!/bin/sh\ncase \"$*\" in\n"
	for _, enc := range ok {
		script += "*" + enc + "*) exit 0;;\n"
	}
	script += "*h264_nvenc*) echo '[h264_nvenc] Cannot load libcuda.so.1' >&2; exit 1;;\n" +
		"*) echo '[h264_vaapi] No VA display found' >&2; exit 1;;\nesac\n"
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProbeOrder(t *testing.T) {
	dir := t.TempDir()
	render, nvidia, video := filepath.Join(dir, "renderD128"), filepath.Join(dir, "nvidia0"), filepath.Join(dir, "video11")
	for _, f := range []string{render, nvidia, video} {
		os.WriteFile(f, nil, 0o666)
	}
	// NVIDIA card: NVENC after VAAPI, before the Raspberry Pi encoder
	v := &VAAPI{Device: render, NVIDIA: nvidia, V4L2: video, FFmpeg: fakeFFmpegWith(t, "h264_nvenc", "h264_v4l2m2m")}
	ok, status := v.Available(context.Background())
	if !ok || !v.Encoder().NVENC || v.Path() != nvidia || !strings.Contains(status.In(i18n.EN), "NVENC, NVIDIA") {
		t.Fatalf("nvenc: %v %s %+v", ok, status.In(i18n.EN), v.Encoder())
	}
	if dec, why := v.Decode(context.Background()); dec || !strings.Contains(why.In(i18n.EN), "NVIDIA") {
		t.Errorf("decode: %v %s", dec, why.In(i18n.EN))
	}
	// NVENC not usable (no driver passed through): the next one, and the status says why
	v = &VAAPI{Device: render, NVIDIA: nvidia, V4L2: video, FFmpeg: fakeFFmpegWith(t, "h264_v4l2m2m")}
	if ok, _ := v.Available(context.Background()); !ok || !v.Encoder().V4L2 {
		t.Errorf("fallback: %+v", v.Encoder())
	}
	v = &VAAPI{Device: render, NVIDIA: nvidia, FFmpeg: fakeFFmpegWith(t)}
	if ok, status := v.Available(context.Background()); ok || !strings.Contains(status.In(i18n.EN), "NVENC: exit status 1: [h264_nvenc] Cannot load libcuda.so.1") {
		t.Errorf("none: %v %s", ok, status.In(i18n.EN))
	}
}
