package stream

import (
	"context"
	"fmt"
	"image"
	"os"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// End credits (Options.Credits, full mode only): the picture shrinks to the top
// left corner and the rest of the screen shows what follows on this channel
// and, page by page, highlights on the others.
const (
	// creditsLead: the credits segment starts this long before the credits, so
	// the burned-in logo can fade out first.
	creditsLead = 2 * time.Second
	// creditsZoom: how long the picture takes to shrink.
	creditsZoom = time.Second
	// creditsWait: how long the credits segment waits for its panel; the
	// client's buffer covers it.
	creditsWait = 4 * time.Second
	// creditsMin: shorter credits stay as they are.
	creditsMin = 20 * time.Second
	// CreditsPage: how long each highlight stays.
	CreditsPage = 8 * time.Second
	// CreditsFPS is the rate of the fade frames between pages.
	CreditsFPS = 25
	// creditsFadeOut: at their end the credits (picture, panel and sound)
	// fade to black this long, then stay black for creditsBlack, so whatever
	// follows does not cut into them.
	creditsFadeOut = 1500 * time.Millisecond
	creditsBlack   = 500 * time.Millisecond
)

// Credits that are not shrunk (not marked final, a scene follows them) get a
// small card instead, as do titles without them (see cardCredits) (Options.CardOverlay): what follows on this channel, then
// films on the others, page by page.
const (
	// cardDelay: the card slides in this long after the credits begin.
	cardDelay = 2 * time.Second
	// cardEnd: it has faded out this long before they end.
	cardEnd = time.Second
	// cardCreditsMin: shorter credits do not count for the card.
	cardCreditsMin = 30 * time.Second
	// cardCreditsReach: credits ending earlier before the end of the title do
	// not count for the card (a longer scene follows).
	cardCreditsReach = 3 * time.Minute
	// cardFallback: without such credits the card shows over the last minute.
	cardFallback = time.Minute
	// cardLead: a title whose card comes later than this is played in two
	// parts, the second one starting this long before the card (see cardSplit).
	cardLead = 10 * time.Second
)

// cardSplit returns where a title played from offset for length is split so
// that only its second part carries the card starting at from (false: one
// part). Its pages held for most of a film before turning stall ffmpeg 7.
func cardSplit(from, offset, length time.Duration) (time.Duration, bool) {
	at := from - cardLead
	if at-offset < cardLead || offset+length-at < 5*time.Second {
		return 0, false
	}
	return at, true
}

// CardOverlay is the small card over credits: an ffconcat list of its pages
// (transparent, with the margin of logo.CaptionPad), shown from From to To
// seconds of output time.
type CardOverlay struct {
	List     string
	From, To float64
}

// CreditsCardFunc renders the pages of the small card over the credits of prog
// starting at at, for a frame of the given height; all pages have the same size.
type CreditsCardFunc func(ctx context.Context, ch *store.Channel, prog *store.Program, at time.Time, height int) (*CreditsPanel, error)

// CreditsPanel holds the rendered frames around the shrunk credits.
type CreditsPanel struct {
	Pages []string   // one full frame per page
	Fades [][]string // Fades[i]: frames blending page i into the next one (CreditsFPS)
}

// CreditsFunc renders the panel for the credits of prog starting at at; video
// is the box the credits play in, picture the part of it their picture fills
// (smaller for 4:3 or wide films).
type CreditsFunc func(ctx context.Context, ch *store.Channel, prog *store.Program, at time.Time, video, picture image.Rectangle, height int) (*CreditsPanel, error)

// ClockFunc returns a PNG of the time text (e.g. "20:15") for a frame of the
// given height: the text right-aligned on a canvas the same size for every
// time, with a transparent margin of ClockPad.
type ClockFunc func(ctx context.Context, text string, height int) (string, error)

// ClockPad is the transparent margin around the clock (room for its shadow).
func ClockPad(height int) int { return height / 60 }

// CreditsVideo returns where the credits play in a frame of w × h: 46% of the
// width, with the margin of the watermark.
func CreditsVideo(w, h int) image.Rectangle {
	m := w * 3 / 100
	vw := w * 46 / 100 / 2 * 2
	vh := vw * h / w / 2 * 2
	return image.Rect(m, m, m+vw, m+vh)
}

// creditsStart returns where the final credits begin in a file of the given
// length (false without credits worth shrinking). A post-credits scene keeps
// earlier credits full size: only credits running to the end count.
func creditsStart(markers []mediaserver.Marker, length time.Duration) (time.Duration, bool) {
	for _, mk := range markers {
		if mk.Type != "credits" {
			continue
		}
		start, end := time.Duration(mk.StartTimeOffset)*time.Millisecond, time.Duration(mk.EndTimeOffset)*time.Millisecond
		if !mk.Final && (length <= 0 || length-end > 10*time.Second) {
			continue
		}
		if length > 0 {
			end = length
		}
		if end-start >= creditsMin {
			return start, true
		}
	}
	return 0, false
}

// cardCredits returns where the small card goes in a title of the given
// length whose credits are not shrunk (see creditsStart): over the last marked
// credits of at least cardCreditsMin that end within cardCreditsReach of the
// end, otherwise over the last minute.
func cardCredits(markers []mediaserver.Marker, length time.Duration) (start, end time.Duration, ok bool) {
	if length <= 2*cardFallback {
		return 0, 0, false
	}
	for _, mk := range markers {
		a, b := time.Duration(mk.StartTimeOffset)*time.Millisecond, time.Duration(mk.EndTimeOffset)*time.Millisecond
		if mk.Type == "credits" && b-a >= cardCreditsMin && length-b <= cardCreditsReach && (!ok || a > start) {
			start, end, ok = a, b, true
		}
	}
	if !ok {
		return length - cardFallback, length, true
	}
	return start, end, ok
}

// concatEntry is a still shown for a while in an ffconcat list, or with cut
// the first d of a clip (copied, so the rest of the clip has to be dropped).
type concatEntry struct {
	path string
	d    time.Duration
	cut  bool
}

// writeConcat writes entries as an ffconcat list. The duration of the last
// entry is ignored by ffmpeg, so it comes once more.
func writeConcat(dir string, entries []concatEntry) (string, error) {
	quote := func(p string) string { return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'" }
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, e := range entries {
		directive := "duration"
		if e.cut {
			directive = "outpoint"
		}
		fmt.Fprintf(&b, "file %s\n%s %s\n", quote(e.path), directive, secs(e.d))
	}
	if n := len(entries); n > 0 {
		fmt.Fprintf(&b, "file %s\n", quote(entries[n-1].path))
	}
	f, err := os.CreateTemp(dir, "creditslist-*.ffconcat")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

// minuteBreaks returns when the minute changes within total after start, as
// offsets from start.
func minuteBreaks(start time.Time, total time.Duration) []time.Duration {
	var out []time.Duration
	for t := start.Truncate(time.Minute).Add(time.Minute); t.Sub(start) < total; t = t.Add(time.Minute) {
		out = append(out, t.Sub(start))
	}
	return out
}

// splitAt cuts entries at the given offsets (sorted). ffmpeg then emits a frame
// there, so overlays that change at that moment (the clock) show up on time.
func splitAt(entries []concatEntry, breaks []time.Duration) []concatEntry {
	var out []concatEntry
	at := time.Duration(0)
	for _, e := range entries {
		end := at + e.d
		for len(breaks) > 0 && breaks[0] <= at {
			breaks = breaks[1:]
		}
		for len(breaks) > 0 && breaks[0] < end {
			out = append(out, concatEntry{e.path, breaks[0] - at, false})
			at, breaks = breaks[0], breaks[1:]
		}
		out = append(out, concatEntry{e.path, end - at, false})
		at = end
	}
	return out
}

// creditsList writes an ffconcat list showing the panel for length: the first
// page until first, then each page for CreditsPage, blended into the next. The
// list is cut at breaks (see splitAt).
func creditsList(dir string, p *CreditsPanel, first, length time.Duration, breaks []time.Duration) (string, error) {
	var entries []concatEntry
	frame := time.Second / CreditsFPS
	total := length + 10*time.Second // the list has to outlast the segment
	page, at := 0, time.Duration(0)
	hold := first + CreditsPage
	if len(p.Pages) < 2 {
		hold = total
	}
	for at < total {
		entries = append(entries, concatEntry{p.Pages[page], hold, false})
		at += hold
		if len(p.Pages) < 2 {
			break
		}
		var fades []string
		if page < len(p.Fades) {
			fades = p.Fades[page]
		}
		for _, fr := range fades {
			entries = append(entries, concatEntry{fr, frame, false})
		}
		fade := time.Duration(len(fades)) * frame
		at += fade
		page = (page + 1) % len(p.Pages)
		hold = max(CreditsPage-fade, time.Second)
	}
	if len(p.Pages) >= 2 {
		entries = append(entries, concatEntry{p.Pages[page], CreditsPage, false})
	}
	return writeConcat(dir, splitAt(entries, breaks))
}

// clockList writes an ffconcat list with the time from start on for length,
// switching on the minute (in the schedule's time, which the viewer sees).
func (s *Streamer) clockList(ctx context.Context, start time.Time, length time.Duration, height int) (string, error) {
	total := length + 10*time.Second // the list has to outlast the segment
	var entries []concatEntry
	from := time.Duration(0)
	for _, to := range append(minuteBreaks(start, total), total) {
		path, err := s.Clock(ctx, s.clockText(start.Add(from).Local()), height)
		if err != nil {
			return "", err
		}
		entries = append(entries, concatEntry{path, to - from, false})
		from = to
	}
	return writeConcat(s.CacheDir, entries)
}

// prefixed returns s after sep, or "" for an empty s.
func prefixed(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

// creditsEase is the progress of the zoom at output time t (0 → 1, eased out),
// with the credits starting at second at.
func creditsEase(at float64) string {
	return fmt.Sprintf("(1-pow(1-min(1,max(0,(t-%.3f)/%.3f)),3))", at, creditsZoom.Seconds())
}

// buildCreditsArgs plays a title from offset with the credits starting at at
// seconds (output time; negative when they are already running): the logo
// fades out, then the picture shrinks onto the panel frames of list, with the
// time of clock (a list too, "" = none) at the top right. At the end
// everything fades to black (creditsFadeOut).
func buildCreditsArgs(input string, offset, length, tsOffset, burst time.Duration, videoIdx, audioIdx int, at float64, list, clock string, opts Options) []string {
	gpu := opts.VAAPI != ""
	args := sourceArgs(input, offset, burst, gpu, opts)
	args = append(args, "-f", "concat", "-safe", "0", "-i", list)
	wmIn := 2
	if clock != "" {
		args = append(args, "-f", "concat", "-safe", "0", "-i", clock)
		wmIn = 3
	}
	wm := opts.Watermark != nil && opts.Watermark.Path != "" && at > 0
	if wm {
		// looped until it has faded out, so the fade can run
		args = append(args, "-loop", "1", "-framerate", "25", "-t", secs(time.Duration((at+0.5)*float64(time.Second))), "-i", opts.Watermark.Path)
	}
	args = append(args, "-t", secs(length))

	w, h := frameSize(opts)
	videoIn := "0:v:0"
	if videoIdx >= 0 {
		videoIn = fmt.Sprintf("0:%d", videoIdx)
	}
	var parts []string
	if opts.Subtitles != nil {
		parts = append(parts, subtitleFilter(opts.Subtitles, videoIn, "sub", offset))
		videoIn = "sub"
	}
	// Only the picture shrinks, without letterbox or pillarbox bars: pic is
	// where it lies in the frame, small where it ends up in the credits box.
	pic := pictureRect(w, h, opts.Aspect)
	video := CreditsVideo(w, h)
	small := fitRect(video, float64(pic.Dx())/float64(pic.Dy()))
	// The picture ends up small and the panel is still: a fast scaler and the
	// fastest encoder preset are enough (about a third less CPU). The video of
	// many files starts a little after their audio; its first frame fills the
	// gap, or the panel would show on its own there.
	parts = append(parts, fmt.Sprintf("[%s]fps=%d:start_time=0,scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1%s[full]",
		videoIn, CreditsFPS, pic.Dx(), pic.Dy(), pic.Dx(), pic.Dy(), opts.ToneMap))
	cur := "full"
	if wm {
		fade := min(at, creditsLead.Seconds())
		parts = append(parts,
			watermarkFilter(opts.Watermark, w, fmt.Sprintf("%d:v", wmIn), fmt.Sprintf(",fade=t=out:st=%.3f:d=%.3f:alpha=1", at-fade, fade)),
			"[full][wm]"+watermarkOverlay(opts.Watermark, w, image.Rect(0, 0, pic.Dx(), pic.Dy()))+":eof_action=pass[wmd]")
		cur = "wmd"
	}
	e := creditsEase(at)
	// the fade to black at the end (shorter if tuned in just before it)
	fadeOut := ""
	if black := length - creditsBlack; black > 0 {
		d := min(creditsFadeOut, black)
		fadeOut = fmt.Sprintf("fade=t=out:st=%s:d=%s", secs(black-d), secs(d))
	}
	// The panel and the clock change rarely: both are combined and converted
	// while they are still single stills, and only then repeated at the frame
	// rate. The clock sits on the panel, so it appears as the picture shrinks,
	// aligned with the panel's right margin and the top of the credits.
	panel := fmt.Sprintf("[1:v]scale=%d:%d,setsar=1", w, h)
	if clock != "" {
		pad := ClockPad(h)
		parts = append(parts, panel+"[pg]", "[2:v]format=rgba[clock]")
		panel = fmt.Sprintf("[pg][clock]overlay=x=W-w-%d:y=%d", video.Min.X-pad, video.Min.Y-pad)
	}
	if pic != image.Rect(0, 0, w, h) && at > 0 {
		// bars stay black until the picture shrinks (the list is cut there)
		panel += fmt.Sprintf(",drawbox=c=black:t=fill:enable='lt(t,%.3f)'", at)
	}
	parts = append(parts,
		fmt.Sprintf("%s,format=yuv420p,fps=%d[panel]", panel, CreditsFPS),
		fmt.Sprintf("[%s]scale=w='trunc((%d-(%d)*%s)/2)*2':h=-2:eval=frame:flags=fast_bilinear[small]", cur, pic.Dx(), pic.Dx()-small.Dx(), e),
		// rounded: overlay cuts off the fraction, and the eased position only
		// reaches its last pixel at the very end, so the picture would sit a
		// little off and then jump into place
		fmt.Sprintf("[panel][small]overlay=x='round(%d+(%d)*%s)':y='round(%d+(%d)*%s)':eval=frame", pic.Min.X, small.Min.X-pic.Min.X, e, pic.Min.Y, small.Min.Y-pic.Min.Y, e)+
			prefixed(",", fadeOut)+encodeTail(gpu, opts.GPUEncoder))
	args = append(args, "-filter_complex", strings.Join(parts, ";"), "-map", "[v]")
	args = append(args, encodeArgs(opts, gpu, "ultrafast")...)
	// the picture needs no padding: the panel goes on, with its last frame
	audio := audioPad
	if fadeOut != "" {
		audio += ",a" + fadeOut // afade, same timing
	}
	args = append(args, "-filter:a", audio)
	return append(args, outputArgs(audioIdx, tsOffset)...)
}
