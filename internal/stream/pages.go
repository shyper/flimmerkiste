package stream

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

// Long "up next" slates turn pages: the slate itself, what the coming title is
// about, the weather and the news (CardPagesFunc). Each page stands for
// pageEvery, blending into the next one; the slate ends on whatever page it is
// on, fading to black. Like the single slate, everything is made of short clips
// encoded once (per page and minute of the clock) and copied into the stream.
const (
	// pagedMin: shorter slates keep a single page.
	pagedMin = 20 * time.Second
	// pageEvery: how long a page stands, including the blend into the next one.
	pageEvery = 15 * time.Second
	// pageFade: the blend between two pages, and the fade to black at the end.
	pageFade = time.Second
	// pageLastMin: the last page stands at least this long before the fade to black.
	pageLastMin = 4 * time.Second
	// pagesAhead: clips needed within this time after the initial burst are
	// made before the stream starts, the others in the background, long before
	// they play.
	pagesAhead = 20 * time.Second
)

// CardPagesFunc returns the pictures of the pages of a slate (the first is the
// slate itself); a single one means no pages.
type CardPagesFunc func(ctx context.Context, ch *store.Channel, prog *store.Program, height int) ([]string, error)

// pageSegment is a stretch of a paged slate, from its start on.
type pageSegment struct {
	kind       segKind
	page, next int // page shown; next: the page blended in (segFade)
	from, to   time.Duration
}

type segKind int

const (
	segPage  segKind = iota // a page standing
	segFade                 // page blending into next
	segBlack                // page fading to black
)

// pageTimeline plans a slate of total length with n pages from its start:
// pages in turn, every pageEvery a blend into the next one, and a fade to black
// at the end. The last page stands at least pageLastMin.
func pageTimeline(total time.Duration, n int) []pageSegment {
	var out []pageSegment
	end := total - pageFade // where the fade to black begins
	page, t := 0, time.Duration(0)
	for n > 1 && t+pageEvery+pageLastMin <= end {
		next := (page + 1) % n
		out = append(out,
			pageSegment{kind: segPage, page: page, from: t, to: t + pageEvery - pageFade},
			pageSegment{kind: segFade, page: page, next: next, from: t + pageEvery - pageFade, to: t + pageEvery})
		page, t = next, t+pageEvery
	}
	out = append(out, pageSegment{kind: segPage, page: page, from: t, to: end},
		pageSegment{kind: segBlack, page: page, from: end, to: total})
	return out
}

// pagedClip is one clip of a paged slate in play order.
type pagedClip struct {
	path string
	at   time.Duration // when it first plays, from the start of the stream
	make func(ctx context.Context) error
}

// pagedCardList writes the ffconcat list of a paged slate that started at
// start and has length left; the clips ffmpeg reads at once (burst) and those
// of the pagesAhead after it are made now.
func (s *Streamer) pagedCardList(ctx context.Context, pages []string, start time.Time, total, length, burst time.Duration, height int, log *slog.Logger) (string, error) {
	offset := total - length // tuned in during the slate
	var entries []concatEntry
	var clips []pagedClip
	known := map[string]bool{}
	need := func(path string, at time.Duration, mk func(ctx context.Context) error) {
		if !known[path] {
			known[path] = true
			clips = append(clips, pagedClip{path, at, mk})
		}
	}
	clockAt := func(t time.Duration) string {
		if s.Clock == nil {
			return ""
		}
		return s.clockText(start.Add(t).Local())
	}
	// a page standing from a to b (times from the slate's start), cut where the minute changes
	standing := func(page int, a, b time.Duration) {
		cuts := append(minuteBreaks(start.Add(a), b-a), b-a)
		from := time.Duration(0)
		for _, cut := range cuts {
			text := clockAt(a + from)
			path := s.pageClipPath(pages[page], text)
			png := pages[page]
			need(path, a+from-offset, func(ctx context.Context) error {
				_, err := s.pageClip(ctx, png, path, text, height)
				return err
			})
			for at := from; at < cut; at += cardClipLength {
				entries = append(entries, concatEntry{path, min(cardClipLength, cut-at), true})
			}
			from = cut
		}
	}
	for _, seg := range pageTimeline(total, len(pages)) {
		if seg.to <= offset {
			continue
		}
		from := max(seg.from, offset)
		if seg.kind == segPage || from > seg.from {
			// tuned in during a blend: the page it leads to stands instead
			page := seg.page
			if seg.kind == segFade {
				page = seg.next
			}
			standing(page, from, seg.to)
			continue
		}
		text := clockAt(seg.from)
		var path string
		var mk func(ctx context.Context) error
		if seg.kind == segFade {
			a, b := pages[seg.page], pages[seg.next]
			path = s.blendClipPath("fade", a+"|"+b+"|"+text)
			mk = func(ctx context.Context) error { return s.blendClip(ctx, path, a, b, text, height) }
		} else {
			a := pages[seg.page]
			path = s.blendClipPath("black", a+"|"+text)
			mk = func(ctx context.Context) error { return s.blendClip(ctx, path, a, "", text, height) }
		}
		need(path, seg.from-offset, mk)
		entries = append(entries, concatEntry{path, seg.to - seg.from, true})
	}

	var later []pagedClip
	for _, c := range clips {
		if c.at >= burst+pagesAhead {
			later = append(later, c)
			continue
		}
		if err := c.make(ctx); err != nil {
			return "", err
		}
	}
	if len(later) > 0 {
		go func() {
			for _, c := range later {
				if ctx.Err() != nil {
					return
				}
				if err := c.make(ctx); err != nil && ctx.Err() == nil {
					log.Warn("slate page clip", "err", err)
				}
			}
		}()
	}
	return writeConcat(s.CacheDir, entries)
}

// pageClipPath names the clip of a page with the time (like the single slate).
func (s *Streamer) pageClipPath(png, text string) string {
	if text == "" {
		return strings.TrimSuffix(png, ".png") + "-page.ts"
	}
	return strings.TrimSuffix(png, ".png") + "-" + strings.ReplaceAll(text, ":", "") + clockClipSuffix
}

// pageClip makes the 2s clip of a page standing, with the time if text is set.
func (s *Streamer) pageClip(ctx context.Context, png, path, text string, height int) (string, error) {
	if text == "" {
		return s.encodeCardClip(ctx, path, []string{"-i", png}, "[0:v]"+cardVideoFilter+"[v]", true, cardClipLength)
	}
	return s.cardClockClip(ctx, png, text, height)
}

// blendClipPath names a blend clip by what it shows.
func (s *Streamer) blendClipPath(kind, key string) string {
	sum := sha1.Sum([]byte(key + "|" + clockClipSuffix))
	return filepath.Join(s.CacheDir, "slate-"+kind+"-"+hex.EncodeToString(sum[:8])+".ts")
}

// blendClip makes the pageFade clip blending page a into page b, or into black
// without b, with the time at the top right (fading out with the page).
func (s *Streamer) blendClip(ctx context.Context, path, a, b, text string, height int) error {
	inputs := []string{"-framerate", "25", "-loop", "1", "-i", a}
	frame := "[0:v]format=rgba[a]"
	cur := "a"
	n := 1
	if b != "" {
		inputs = append(inputs, "-framerate", "25", "-loop", "1", "-i", b)
		frame += fmt.Sprintf(";[1:v]format=rgba,fade=t=in:st=0:d=%s:alpha=1[b];[a][b]overlay[ab]", secs(pageFade))
		cur, n = "ab", 2
	}
	if text != "" && s.Clock != nil {
		clock, err := s.Clock(ctx, text, height)
		if err != nil {
			return err
		}
		right, top := CardClockMargin(height)
		inputs = append(inputs, "-i", clock)
		frame += fmt.Sprintf(";[%s][%d:v]overlay=x=W-w-%d:y=%d[c]", cur, n, right, top)
		cur = "c"
	}
	if b == "" {
		frame += fmt.Sprintf(";[%s]fade=t=out:st=0:d=%s[f]", cur, secs(pageFade))
		cur = "f"
	}
	frame += fmt.Sprintf(";[%s]format=yuv420p,fps=25[v]", cur)
	_, err := s.encodeCardClip(ctx, path, inputs, frame, true, pageFade)
	return err
}
