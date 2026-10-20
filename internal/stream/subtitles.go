package stream

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// Forced subtitles translate the foreign-language parts of a film. Plex does not
// show subtitle tracks of a live stream, so in full mode they are burned into
// the picture.

// Subtitles is the forced subtitle track of the current item.
type Subtitles struct {
	Index int    // input stream index of an embedded picture-based track (PGS, VobSub, DVB), -1 if File is set
	File  string // local text subtitle file (SRT, ASS …)
	Temp  bool   // File was downloaded from the server and is removed after use
}

var imageSubCodecs = map[string]bool{"pgs": true, "hdmv_pgs_subtitle": true, "vobsub": true, "dvd_subtitle": true,
	"dvdsub": true, "dvb_subtitle": true, "dvbsub": true, "xsub": true}

var textSubCodecs = map[string]bool{"srt": true, "subrip": true, "ass": true, "ssa": true, "mov_text": true,
	"webvtt": true, "vtt": true, "smi": true, "sami": true, "text": true}

func isForced(st mediaserver.Stream) bool {
	if st.Forced {
		return true
	}
	t := strings.ToLower(st.TrackTitle)
	return strings.Contains(t, "forced") || strings.Contains(t, "erzwungen")
}

// forcedSubtitle picks the forced subtitle track in the language of the chosen
// audio track. Without a known audio language only a single forced track is used.
// Text tracks count only as files (Key set): the servers do not hand out text
// tracks inside a video, the subtitle scripts save them as files instead.
// embedded reports a forced text track inside the video that would have been
// used but has no such file yet.
func forcedSubtitle(streams []mediaserver.Stream, audioIdx int) (sub *mediaserver.Stream, embedded bool) {
	lang := audioLanguage(streams, audioIdx)
	var cands []mediaserver.Stream
	for _, st := range streams {
		if st.StreamType != 3 || !isForced(st) {
			continue
		}
		codec := strings.ToLower(st.Codec)
		switch {
		case textSubCodecs[codec] && st.Key != "", imageSubCodecs[codec] && st.Key == "":
			cands = append(cands, st)
		case textSubCodecs[codec] && (lang == "" || strings.EqualFold(st.LanguageCode, lang)):
			embedded = true
		}
	}
	if lang == "" {
		if len(cands) == 1 {
			return &cands[0], false
		}
		return nil, embedded && len(cands) == 0
	}
	for i := range cands {
		if strings.EqualFold(cands[i].LanguageCode, lang) {
			return &cands[i], false
		}
	}
	return nil, embedded
}

// audioLanguage is the language code of the audio track with input index audioIdx.
func audioLanguage(streams []mediaserver.Stream, audioIdx int) string {
	for _, st := range streams {
		if st.StreamType == 2 && st.Index == audioIdx {
			return st.LanguageCode
		}
	}
	return ""
}

// localForcedSubtitle looks next to a video that is read directly for the file
// a subtitle script saved ("Movie.de.forced.srt") in language lang. It finds
// the file before the server has picked it up, or when its download fails.
func localForcedSubtitle(video, lang string) string {
	if lang == "" {
		return ""
	}
	dir := filepath.Dir(video)
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video)) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), base)
		if !ok || e.IsDir() {
			continue
		}
		ext := filepath.Ext(rest)
		if !strings.EqualFold(ext, ".srt") && !strings.EqualFold(ext, ".ass") {
			continue
		}
		code, kind, _ := strings.Cut(strings.TrimSuffix(rest, ext), ".")
		if strings.EqualFold(kind, "forced") && store.LanguageRank(code, []string{lang}) == 0 {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// prepareSubtitles turns the chosen track into something ffmpeg can burn in:
// embedded picture subtitles are overlaid directly, text subtitle files are
// downloaded from the server (the caller removes the file). nil = skip.
func (s *Streamer) prepareSubtitles(ctx context.Context, srv mediaserver.Server, st *mediaserver.Stream, log *slog.Logger) *Subtitles {
	codec := strings.ToLower(st.Codec)
	if st.Key == "" && imageSubCodecs[codec] {
		return &Subtitles{Index: st.Index}
	}
	u := srv.SubtitleURL(*st)
	if u == "" {
		return nil
	}
	f, err := os.CreateTemp(s.CacheDir, "sub-*."+codec)
	if err != nil {
		log.Warn("forced subtitles skipped", "err", err)
		return nil
	}
	f.Close()
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := fetchFile(dctx, u, f.Name()); err != nil {
		os.Remove(f.Name())
		log.Warn("forced subtitles skipped", "err", mediaserver.Redact(err.Error()))
		return nil
	}
	return &Subtitles{Index: -1, File: f.Name(), Temp: true}
}

// subtitleFilter burns the subtitles into video stream in and names the result out.
// offset is where playback starts: text subtitles are timed against the file,
// while the frames start at 0 after seeking.
func subtitleFilter(sub *Subtitles, in, out string, offset time.Duration) string {
	if sub.File != "" {
		shift := "0"
		if offset > time.Second {
			shift = secs(offset)
		}
		return fmt.Sprintf("[%s]setpts=PTS+%s/TB,subtitles=filename=%s,setpts=PTS-%s/TB[%s]", in, shift, escapeFilterArg(sub.File), shift, out)
	}
	// Picture subtitles cover the frame of the original disc. On cropped films that
	// canvas is taller than the video; aligned at the bottom the subtitles stay visible.
	return fmt.Sprintf("[%s][0:%d]overlay=x=(W-w)/2:y=H-h:eof_action=pass[%s]", in, sub.Index, out)
}

// escapeFilterArg escapes a value for a filter option inside -filter_complex
// (option level, then filtergraph level).
func escapeFilterArg(v string) string {
	opt := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `:`, `\:`).Replace(v)
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, `[`, `\[`, `]`, `\]`, `,`, `\,`, `;`, `\;`).Replace(opt)
}
