package stream

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

// HDR sources (PQ, HLG) look pale and grey when "full" mode squeezes them into
// an SDR picture as they are. They are tone mapped to BT.709 instead: with
// tonemapx of jellyfin-ffmpeg (fast, SIMD) or, with another ffmpeg, the slower
// zscale and tonemap filters. The source is tagged first, since files do not
// always carry their colour properties.

// toneMapPeak: the SDR picture's peak brightness in nits.
const toneMapPeak = 100

// toneMapFilters returns the filters for a source with the given transfer
// ("smpte2084", "arib-std-b67"), "," first; "" if ffmpeg has none of them.
func toneMapFilters(transfer string, tonemapx, zscale bool) string {
	tag := fmt.Sprintf("setparams=color_primaries=bt2020:color_trc=%s:colorspace=bt2020nc", strings.ToLower(transfer))
	switch {
	case tonemapx:
		return fmt.Sprintf(",%s,tonemapx=tonemap=bt2390:desat=0:peak=%d:t=bt709:m=bt709:p=bt709:format=yuv420p", tag, toneMapPeak)
	case zscale:
		return fmt.Sprintf(",%s,zscale=t=linear:npl=%d,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=mobius:desat=0,"+
			"zscale=t=bt709:m=bt709:r=tv,format=yuv420p", tag, toneMapPeak)
	}
	return ""
}

// toneMap returns the tone mapping filters for a source, as the ffmpeg in use
// can run them (its filters are looked up once).
func (s *Streamer) toneMap(ctx context.Context, transfer string) string {
	s.filtersOnce.Do(func() {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, s.FFmpeg, "-hide_banner", "-filters").Output()
		if err != nil {
			s.Log.Warn("listing ffmpeg filters", "err", err)
			return
		}
		has := func(name string) bool { return strings.Contains(string(out), " "+name+" ") }
		s.tonemapx, s.zscale = has("tonemapx"), has("zscale") && has("tonemap")
		if !s.tonemapx && !s.zscale {
			s.Log.Warn("this ffmpeg cannot tone map HDR; HDR titles look pale in full mode (jellyfin-ffmpeg can)")
		}
	})
	return toneMapFilters(transfer, s.tonemapx, s.zscale)
}

// videoStream returns the video track the stream plays.
func videoStream(streams []mediaserver.Stream, videoIdx int) *mediaserver.Stream {
	for i, st := range streams {
		if st.StreamType == mediaserver.StreamVideo && (videoIdx < 0 || st.Index == videoIdx) {
			return &streams[i]
		}
	}
	return nil
}
