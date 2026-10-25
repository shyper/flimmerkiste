package stream

import (
	"context"
	"fmt"
	"github.com/shyper/flimmerkiste/internal/i18n"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// VAAPI is the GPU encoder used for "full" normalisation: VAAPI (Intel, AMD)
// or, when that is not usable, NVENC (NVIDIA) or the V4L2 encoder of a
// Raspberry Pi 4. It is probed once with short test encodes; without it
// everything runs in software.
type VAAPI struct {
	Device string // render node, e.g. /dev/dri/renderD128
	NVIDIA string // NVIDIA device (/dev/nvidia0), "" = none
	V4L2   string // V4L2 encoder device (Raspberry Pi 4: /dev/video11), "" = none
	FFmpeg string

	once   sync.Once
	ok     bool
	enc    GPUEncoder
	status i18n.Text

	decode       bool // decoding on the GPU is fast enough (see testDecode)
	decodeStatus i18n.Text
}

// GPUEncoder holds the h264_vaapi settings a GPU accepted. Drivers differ:
// some only have the fixed-function encoder, some only constant quality (CQP).
// NVENC (NVIDIA) and V4L2 (Raspberry Pi 4) are other encoders instead; both
// take frames from memory, VAAPI from the GPU.
type GPUEncoder struct {
	LowPower bool // fixed-function encoder (VDENC)
	CQP      bool // constant quality instead of a bitrate
	NVENC    bool
	V4L2     bool
}

// VAAPI reports whether the encoder is the VAAPI one (frames uploaded to the GPU).
func (e GPUEncoder) VAAPI() bool { return !e.NVENC && !e.V4L2 }

func (e GPUEncoder) String() string {
	switch {
	case e.NVENC:
		return "NVENC"
	case e.V4L2:
		return "V4L2"
	}
	s := map[bool]string{true: "low power", false: "full power"}[e.LowPower]
	return s + map[bool]string{true: ", CQP", false: ", VBR"}[e.CQP]
}

// args returns the video encoder options for output height h.
func (e GPUEncoder) args(h int) []string {
	if e.NVENC {
		return []string{"-c:v", "h264_nvenc", "-preset", "p4", "-rc", "vbr", "-b:v", map[int]string{720: "3M", 1080: "6M"}[h],
			"-maxrate", maxrate(h), "-bufsize", bufsize(h), "-g", "50", "-profile:v", "high"}
	}
	if e.V4L2 {
		// The encoder sends the stream headers only once; repeated at every
		// keyframe, Plex can start anywhere in the stream (tuning in).
		return []string{"-c:v", "h264_v4l2m2m", "-b:v", map[int]string{720: "3M", 1080: "6M"}[h], "-g", "50",
			"-bsf:v", "dump_extra=freq=keyframe"}
	}
	args := []string{"-c:v", "h264_vaapi", "-low_power", map[bool]string{true: "1", false: "0"}[e.LowPower]}
	if e.CQP {
		args = append(args, "-rc_mode", "CQP", "-qp", "23")
	} else {
		args = append(args, "-rc_mode", "VBR", "-b:v", map[int]string{720: "3M", 1080: "6M"}[h],
			"-maxrate", maxrate(h), "-bufsize", bufsize(h))
	}
	return append(args, "-g", "50", "-profile:v", "high")
}

func maxrate(h int) string { return map[int]string{720: "4M", 1080: "8M"}[h] }
func bufsize(h int) string { return map[int]string{720: "8M", 1080: "16M"}[h] }

// probeFrames: length of the test encode (4 s of 720p25).
const probeFrames = 100

// minSpeed: the GPU must encode at least this many times faster than real
// time, otherwise software is used (slow GPU paths exist, e.g. shader-based
// encoding on small Intel chips).
const minSpeed = 1.5

// Available reports whether GPU encoding works and why (not).
func (v *VAAPI) Available(ctx context.Context) (bool, i18n.Text) {
	if v == nil {
		return false, i18n.M("gpu.notSetUp")
	}
	v.once.Do(func() {
		// the result is kept for good: a client hanging up must not cut the probe short
		ctx := context.WithoutCancel(ctx)
		v.ok, v.enc, v.status = v.probe(ctx)
		// NVIDIA cards and the Raspberry Pi often have a render node too, but no VAAPI driver
		for _, alt := range []struct {
			device string
			enc    GPUEncoder
			ok     string
		}{{v.NVIDIA, GPUEncoder{NVENC: true}, "gpu.okNVENC"}, {v.V4L2, GPUEncoder{V4L2: true}, "gpu.okV4L2"}} {
			if v.ok || alt.device == "" {
				continue
			}
			if _, err := os.Stat(alt.device); err != nil {
				continue
			}
			if ok, status := v.probeMemory(ctx, alt.enc, alt.device, alt.ok); ok {
				v.ok, v.enc, v.status = true, alt.enc, status
			} else {
				v.status = i18n.M("%s; %s", v.status, status)
			}
		}
		if v.ok && v.enc.VAAPI() {
			v.decode, v.decodeStatus = v.probeDecode(ctx)
		}
	})
	return v.ok, v.status
}

// Decode reports whether the GPU should also decode, and why (not). Small
// chips may decode barely in real time once frames are copied back for the
// overlays, while the CPU is many times faster (J3455: ~1x against ~12x).
func (v *VAAPI) Decode(ctx context.Context) (bool, i18n.Text) {
	if ok, _ := v.Available(ctx); !ok {
		return false, i18n.M("gpu.none")
	}
	switch {
	case v.enc.NVENC:
		return false, i18n.M("gpu.decodeNVENC")
	case v.enc.V4L2:
		return false, i18n.M("gpu.decodeV4L2")
	}
	return v.decode, v.decodeStatus
}

// Path is the device the stream encodes on.
func (v *VAAPI) Path() string {
	switch {
	case v.enc.NVENC:
		return v.NVIDIA
	case v.enc.V4L2:
		return v.V4L2
	}
	return v.Device
}

// minDecodeSpeed: GPU decoding (1080p H.264, frames copied back) must be at
// least this many times faster than real time, so it keeps up next to encoding.
const minDecodeSpeed = 4

// decodeSeconds: length of the decode test video.
const decodeSeconds = 4

func (v *VAAPI) probeDecode(ctx context.Context) (bool, i18n.Text) {
	speed, err := v.testDecode(ctx)
	switch {
	case err != nil:
		return false, i18n.M("gpu.decodeCPUFailed", err)
	case speed < minDecodeSpeed:
		return false, i18n.M("gpu.decodeCPUSlow", speed)
	}
	return true, i18n.M("gpu.decodeGPU", speed)
}

// testDecode decodes a generated 1080p H.264 clip on the GPU, copying the
// frames back to memory as streaming does, and returns the speed (1 = real time).
func (v *VAAPI) testDecode(ctx context.Context) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	f, err := os.CreateTemp("", "flimmerkiste-decode-*.mkv")
	if err != nil {
		return 0, err
	}
	f.Close()
	defer os.Remove(f.Name())
	gen := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=s=1920x1080:r=25:d=%d", decodeSeconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", f.Name()}
	if out, err := exec.CommandContext(ctx, v.FFmpeg, gen...).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("test video: %v: %s", err, strings.TrimSpace(string(out)))
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-init_hw_device", "vaapi=va:" + v.Device, "-hwaccel", "vaapi", "-hwaccel_device", "va",
		"-i", f.Name(), "-f", "null", "-"}
	started := time.Now()
	if out, err := exec.CommandContext(ctx, v.FFmpeg, args...).CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return 0, i18n.E("gpu.timeout")
		}
		return 0, fmt.Errorf("%v: %s", err, encoderError(string(out)))
	}
	return decodeSeconds / time.Since(started).Seconds(), nil
}

// Encoder returns the settings chosen by the probe.
func (v *VAAPI) Encoder() GPUEncoder {
	if v == nil {
		return GPUEncoder{}
	}
	return v.enc
}

func (v *VAAPI) probe(ctx context.Context) (bool, GPUEncoder, i18n.Text) {
	if _, err := os.Stat(v.Device); err != nil {
		return false, GPUEncoder{}, i18n.M("gpu.noDevice", v.Device)
	}
	f, err := os.OpenFile(v.Device, os.O_RDWR, 0)
	if err != nil {
		return false, GPUEncoder{}, i18n.M("gpu.noAccess", v.Device)
	}
	f.Close()
	// Intel chips offer two encoders and not every driver offers a bitrate
	// mode; which combination exists and is fast differs per generation, so
	// they are tried with exactly the options used for streaming.
	var problems []any
	for _, enc := range []GPUEncoder{{LowPower: true}, {LowPower: true, CQP: true}, {}, {CQP: true}} {
		fps, err := v.testEncode(ctx, enc)
		switch {
		case err != nil:
			problems = append(problems, i18n.M("gpu.encFailed", enc.String(), err))
		case fps < 25*minSpeed:
			problems = append(problems, i18n.M("gpu.tooSlow", enc.String(), fps))
		default:
			return true, enc, i18n.M("gpu.ok", enc.String(), fps, v.Device)
		}
	}
	return false, GPUEncoder{}, i18n.M(strings.TrimSuffix(strings.Repeat("%s; ", len(problems)), "; "), problems...)
}

// probeMemory tries an encoder that takes frames from memory (NVENC, V4L2)
// with the options used for streaming.
func (v *VAAPI) probeMemory(ctx context.Context, enc GPUEncoder, device, okKey string) (bool, i18n.Text) {
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		if enc.V4L2 {
			return false, i18n.M("gpu.noAccessV4L2", device)
		}
		return false, i18n.M("gpu.noAccessNVENC", device)
	}
	f.Close()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=s=1280x720:r=25:d=%d", probeFrames/25), "-vf", "format=yuv420p"}
	args = append(args, enc.args(720)...)
	args = append(args, "-f", "null", "-")
	started := time.Now()
	if out, err := exec.CommandContext(ctx, v.FFmpeg, args...).CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return false, i18n.M("gpu.encFailed", enc.String(), i18n.E("gpu.timeout"))
		}
		return false, i18n.M("gpu.encFailed", enc.String(), fmt.Errorf("%v: %s", err, encoderError(string(out))))
	}
	fps := probeFrames / time.Since(started).Seconds()
	if fps < 25*minSpeed {
		return false, i18n.M("gpu.tooSlow", enc.String(), fps)
	}
	return true, i18n.M(okKey, fps, device)
}

// testEncode encodes generated 720p video on the GPU and returns frames per second.
func (v *VAAPI) testEncode(ctx context.Context, enc GPUEncoder) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-init_hw_device", "vaapi=va:" + v.Device, "-filter_hw_device", "va",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=s=1280x720:r=25:d=%d", probeFrames/25),
		"-vf", "format=nv12,hwupload"}
	args = append(args, enc.args(720)...)
	args = append(args, "-f", "null", "-")
	started := time.Now()
	out, err := exec.CommandContext(ctx, v.FFmpeg, args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return 0, i18n.E("gpu.timeout")
		}
		return 0, fmt.Errorf("%v: %s", err, encoderError(string(out)))
	}
	return probeFrames / time.Since(started).Seconds(), nil
}

// encoderError picks the line naming the cause from ffmpeg's output, e.g.
// "Driver does not support VBR RC mode", instead of the generic last line.
func encoderError(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		if strings.Contains(l, "h264_vaapi") || strings.Contains(l, "VAAPI") || strings.Contains(l, "va_") || strings.Contains(l, "v4l2") || strings.Contains(l, "nvenc") || strings.Contains(l, "CUDA") {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
