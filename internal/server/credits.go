package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/stream"
)

// defaultCredits is the end credits setting of channels set to "default".
func (s *Server) defaultCredits(ctx context.Context) string {
	if s.store.Setting(ctx, store.SetCredits, "off") == "squeeze" {
		return "squeeze"
	}
	return "off"
}

// creditsMode resolves a channel's end credits setting; "default" follows the global one.
func (s *Server) creditsMode(ctx context.Context, mode string) string {
	if mode == "default" {
		return s.defaultCredits(ctx)
	}
	return mode
}

const (
	// highlightReach: highlights start at most this long after the credits.
	highlightReach = 3 * time.Hour
	// highlightMax: pages of highlights at most.
	highlightMax = 4
	// creditsFadeFrames: frames of the blend between two pages (at stream.CreditsFPS).
	creditsFadeFrames = 12
)

// highlight is a program on another channel worth announcing.
type highlight struct {
	ch    *store.Channel
	prog  store.Program
	m     *store.Media
	score float64
}

// highlights picks programs starting on other channels within highlightReach
// after at: preferably one per channel, ordered by start.
func (s *Server) highlights(ctx context.Context, c *store.Channel, at time.Time) []highlight {
	return pickHighlights(s.highlightCandidates(ctx, c, at), highlightMax)
}

// highlightCandidates returns the programs starting on other channels within
// highlightReach after at, scored.
func (s *Server) highlightCandidates(ctx context.Context, c *store.Channel, at time.Time) []highlight {
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil
	}
	var all []highlight
	for _, other := range chans {
		if other.ID == c.ID || !other.Enabled {
			continue
		}
		if cfg, err := schedule.ParseConfig(other.Config); err != nil || cfg.FillerOnly(other.Kind) {
			continue
		}
		for _, p := range s.mainPrograms(ctx, other.ID, at, at.Add(highlightReach)) {
			if p.Start < at.UnixMilli() || p.Start > at.Add(highlightReach).UnixMilli() {
				continue
			}
			if m := s.lib.Get(ctx, p.RatingKey); m != nil {
				all = append(all, highlight{ch: other, prog: p, m: m, score: highlightScore(m, p.StartTime(), at)})
			}
		}
	}
	return all
}

// pickHighlights takes the best scored programs, one per channel first, and
// orders them by start.
func pickHighlights(all []highlight, n int) []highlight {
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	var out []highlight
	taken := map[int]bool{}
	seen := map[int64]bool{}
	for round := 0; round < 2 && len(out) < n; round++ {
		for i, h := range all {
			if len(out) == n {
				break
			}
			if taken[i] || (round == 0 && seen[h.ch.ID]) {
				continue
			}
			taken[i], seen[h.ch.ID] = true, true
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].prog.Start < out[j].prog.Start })
	return out
}

// highlightScore: movies before single episodes, well rated and new titles,
// prime time, and the sooner the better.
func highlightScore(m *store.Media, start, at time.Time) float64 {
	score := m.Rating / 2
	if m.Type == "movie" {
		score += 3
	}
	if m.AddedAt > 0 && at.Sub(time.Unix(m.AddedAt, 0)) < 30*24*time.Hour {
		score += 2
	}
	if h := start.Local().Hour(); h >= 19 && h <= 21 {
		score += 1.5
	}
	return score - start.Sub(at).Hours()*0.5
}

// creditsOptions describes the panel for credits at at, followed on c by the
// next main program from nextAfter on.
func (s *Server) creditsOptions(ctx context.Context, c *store.Channel, nextAfter, at time.Time, height int) (logo.CreditsOptions, string) {
	w := height * 16 / 9
	l := s.lang(ctx)
	o := logo.CreditsOptions{Width: w, Height: height, Video: stream.CreditsVideo(w, height), Accent: c.Color,
		Headline: i18n.T(l, "card.headline"), Kicker: i18n.T(l, "credits.kicker")}
	key := fmt.Sprintf("%d|%d|%s|%s|%s|%s|v2", c.ID, height, c.Logo, s.autoLogoVersion(c), c.Color, l) // v2: logos without margin
	if next := s.nextMain(ctx, c.ID, nextAfter); next != nil {
		if m := s.lib.Get(ctx, next.RatingKey); m != nil {
			// always the start time, also when it follows right away
			o.When = i18n.T(l, "card.at", i18n.Clock(l, next.StartTime().Local()))
			var card logo.CardOptions
			s.describeText(l, &card, m)
			o.Title, o.Subtitle = card.Title, card.Subtitle
			key += fmt.Sprintf("|%s|%s", o.When, m.RatingKey)
		}
	}
	for _, h := range s.highlights(ctx, c, at) {
		var card logo.CardOptions
		s.describeText(l, &card, h.m)
		o.Highlights = append(o.Highlights, logo.Highlight{
			When:  i18n.T(l, "credits.slot", i18n.Clock(l, h.prog.StartTime().Local()), h.ch.Name),
			Title: card.Title, Subtitle: card.Subtitle, Summary: h.m.Summary,
		})
		key += fmt.Sprintf("|%d:%s:%d:%s:%s", h.ch.ID, h.m.RatingKey, h.prog.Start, h.ch.Logo, s.autoLogoVersion(h.ch))
	}
	return o, key
}

// loadCreditsImages adds logos and posters to the panel options.
func (s *Server) loadCreditsImages(ctx context.Context, c *store.Channel, at time.Time, o *logo.CreditsOptions) {
	if path, err := s.logoFile(c); err == nil {
		o.Logo = decodeFile(path)
	}
	hs := s.highlights(ctx, c, at)
	for i := range o.Highlights {
		if i >= len(hs) {
			break
		}
		if path, err := s.logoFile(hs[i].ch); err == nil {
			o.Highlights[i].Logo = decodeFile(path)
		}
		o.Highlights[i].Poster = s.fetchPoster(ctx, hs[i].m)
	}
}

// renderCredits renders the frames around the shrunk end credits of prog,
// which start at at, and caches them in a directory of their own.
func (s *Server) renderCredits(ctx context.Context, c *store.Channel, prog *store.Program, at time.Time, video, picture image.Rectangle, height int) (*stream.CreditsPanel, error) {
	o, key := s.creditsOptions(ctx, c, prog.StopTime(), at, height)
	o.Video, o.Picture = video, picture
	key += fmt.Sprintf("|%v|%v|v1", video, picture)
	return s.cachedPanel("credits-", key, func() ([]*image.RGBA, error) {
		s.loadCreditsImages(ctx, c, at, &o)
		return logo.RenderCredits(o)
	})
}

const (
	// creditsCardScale: the card over credits that are not shrunk, relative to
	// the caption of trailers.
	creditsCardScale = 0.8
	// creditsCardFilms: films on other channels on that card.
	creditsCardFilms = 3
)

// creditsCardPages describes the small card over credits at at that are not
// shrunk: the next title on c, then films on other channels.
func (s *Server) creditsCardPages(ctx context.Context, c *store.Channel, nextAfter, at time.Time, height int) ([]logo.CaptionOptions, string) {
	var pages []logo.CaptionOptions
	l := s.lang(ctx)
	key := fmt.Sprintf("%d|%d|%s|v1", c.ID, height, l)
	page := func(ch *store.Channel, kicker string, m *store.Media) {
		var card logo.CardOptions
		s.describeText(l, &card, m)
		pages = append(pages, logo.CaptionOptions{Height: height, Scale: creditsCardScale, Accent: ch.Color, Kicker: kicker, Title: card.Title})
		key += fmt.Sprintf("|%s|%s|%s", ch.Color, kicker, m.RatingKey)
	}
	if next := s.nextMain(ctx, c.ID, nextAfter); next != nil {
		if m := s.lib.Get(ctx, next.RatingKey); m != nil {
			page(c, i18n.T(l, "card.at", i18n.Clock(l, next.StartTime().Local())), m)
		}
	}
	var films []highlight
	for _, h := range s.highlightCandidates(ctx, c, at) {
		if h.m.Type == "movie" {
			films = append(films, h)
		}
	}
	for _, h := range pickHighlights(films, creditsCardFilms) {
		page(h.ch, i18n.T(l, "credits.slot", i18n.Clock(l, h.prog.StartTime().Local()), h.ch.Name), h.m)
	}
	return pages, key
}

// renderCreditsCard renders the pages of the small card over the credits of
// prog starting at at, cached like the panel of shrunk credits.
func (s *Server) renderCreditsCard(ctx context.Context, c *store.Channel, prog *store.Program, at time.Time, height int) (*stream.CreditsPanel, error) {
	pages, key := s.creditsCardPages(ctx, c, prog.StopTime(), at, height)
	if len(pages) == 0 {
		return nil, nil
	}
	return s.cachedPanel("creditscard-", key, func() ([]*image.RGBA, error) {
		return logo.RenderCaptionPages(pages)
	})
}

// cachedPanel returns the frames rendered by render, with fades between them,
// from a cache directory named after prefix and key (rendering them once).
func (s *Server) cachedPanel(prefix, key string, render func() ([]*image.RGBA, error)) (*stream.CreditsPanel, error) {
	sum := sha1.Sum([]byte(key))
	dir := filepath.Join(s.cfg.DataDir, "cache", prefix+hex.EncodeToString(sum[:8]))
	manifest := filepath.Join(dir, "panel.json")
	if data, err := os.ReadFile(manifest); err == nil {
		var p stream.CreditsPanel
		if json.Unmarshal(data, &p) == nil {
			now := time.Now()
			_ = os.Chtimes(dir, now, now) // still in use: keep it from purgeCache
			return &p, nil
		}
	}
	frames, err := render()
	if err != nil {
		return nil, err
	}
	// Rendered into a temporary directory, so two viewers never see half a panel.
	tmp, err := os.MkdirTemp(filepath.Dir(dir), prefix+"tmp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	write := func(name string, img image.Image) (string, error) {
		var buf bytes.Buffer
		if err := enc.Encode(&buf, img); err != nil {
			return "", err
		}
		return filepath.Join(dir, name), os.WriteFile(filepath.Join(tmp, name), buf.Bytes(), 0o644)
	}
	p := &stream.CreditsPanel{}
	for i, f := range frames {
		path, err := write(fmt.Sprintf("page-%d.png", i), f)
		if err != nil {
			return nil, err
		}
		p.Pages = append(p.Pages, path)
	}
	if len(frames) > 1 {
		for i, f := range frames {
			next := frames[(i+1)%len(frames)]
			var fade []string
			for k := 1; k <= creditsFadeFrames; k++ {
				path, err := write(fmt.Sprintf("fade-%d-%02d.png", i, k), logo.Blend(f, next, float64(k)/(creditsFadeFrames+1)))
				if err != nil {
					return nil, err
				}
				fade = append(fade, path)
			}
			p.Fades = append(p.Fades, fade)
		}
	}
	data, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(tmp, "panel.json"), data, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if _, statErr := os.Stat(manifest); statErr == nil {
			return p, nil // rendered by another viewer meanwhile
		}
		return nil, err
	}
	return p, nil
}

// renderClock returns the clock PNG for a time text, cached on disk.
func (s *Server) renderClock(_ context.Context, text string, height int) (string, error) {
	name := fmt.Sprintf("clock-%d-%s-v2.png", height, strings.ReplaceAll(text, ":", ""))
	path := filepath.Join(s.cfg.DataDir, "cache", name)
	if keepCached(path) {
		return path, nil
	}
	data, err := logo.RenderClock(text, height, stream.ClockPad(height))
	if err != nil {
		return "", err
	}
	return path, writeFileAtomic(path, data)
}

// handleCreditsPreview renders the first page of the credits panel for the
// editor, with a placeholder where the credits play.
func (s *Server) handleCreditsPreview(w http.ResponseWriter, r *http.Request) {
	c := s.channelFromPath(w, r)
	if c == nil {
		return
	}
	ctx := r.Context()
	now := time.Now()
	o, _ := s.creditsOptions(ctx, c, now, now, 720)
	s.loadCreditsImages(ctx, c, now, &o)
	frames, err := logo.RenderCredits(o)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	img := frames[0]
	draw.Draw(img, o.Video, image.NewUniform(color.RGBA{44, 47, 60, 255}), image.Point{}, draw.Src)
	if data, err := logo.RenderClock(i18n.Clock(s.lang(r.Context()), now), o.Height, stream.ClockPad(o.Height)); err == nil {
		if clock, err := png.Decode(bytes.NewReader(data)); err == nil {
			// placed like the overlay in the stream (stream.buildCreditsArgs)
			pad, b := stream.ClockPad(o.Height), clock.Bounds()
			at := image.Pt(o.Width-b.Dx()-(o.Video.Min.X-pad), o.Video.Min.Y-pad)
			draw.Draw(img, b.Add(at), clock, image.Point{}, draw.Over)
		}
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_ = png.Encode(w, img)
}
