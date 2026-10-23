package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/stream"
)

// mergeFillers hides filler clips and slates from guides: the preceding main
// program is extended to cover them, like TV guides do with ad breaks.
func mergeFillers(progs []store.Program) []store.Program {
	out := make([]store.Program, 0, len(progs))
	for _, p := range progs {
		if p.IsMain() {
			out = append(out, p)
			continue
		}
		if n := len(out); n > 0 && out[n-1].ChannelID == p.ChannelID && out[n-1].Stop <= p.Start+1000 {
			out[n-1].Stop = p.Stop
		}
	}
	return out
}

// mainPrograms loads programs around a window, including the main program a
// leading filler belongs to, and merges the fillers away.
func (s *Server) mainPrograms(ctx context.Context, channelID int64, from, to time.Time) []store.Program {
	progs, err := s.store.ProgramsBetween(ctx, channelID, from.Add(-6*time.Hour), to)
	if err != nil {
		return nil
	}
	merged := mergeFillers(progs)
	out := merged[:0]
	for _, p := range merged {
		if p.Stop > from.UnixMilli() {
			out = append(out, p)
		}
	}
	return out
}

// nextMain returns the next main program starting at or after t.
func (s *Server) nextMain(ctx context.Context, channelID int64, t time.Time) *store.Program {
	progs, err := s.store.ProgramsBetween(ctx, channelID, t, t.Add(12*time.Hour))
	if err != nil {
		return nil
	}
	for i := range progs {
		if progs[i].IsMain() && progs[i].Start >= t.UnixMilli()-1000 {
			return &progs[i]
		}
	}
	return nil
}

// logoFile returns a local PNG/JPG of the channel logo (rendering the automatic one if needed).
func (s *Server) logoFile(c *store.Channel) (string, error) {
	if file, _ := s.activeLogo(c); file != "" {
		return filepath.Join(s.cfg.DataDir, "logos", file), nil
	}
	if _, err := s.renderLogoCached(c); err != nil {
		return "", err
	}
	return filepath.Join(s.cfg.DataDir, "cache", "logo-"+s.autoLogoVersion(c)+".png"), nil
}

func decodeFile(path string) image.Image {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

// renderCard builds the slate for a card program and caches it on disk: "up
// next", or after a trailer the promo naming when the advertised title airs.
func (s *Server) renderCard(ctx context.Context, c *store.Channel, prog *store.Program, height int) (string, error) {
	key := fmt.Sprintf("%d|%d|%s|%s|%s|%s|v2", c.ID, height, c.Logo, s.autoLogoVersion(c), c.Color, s.lang(ctx))
	var next *store.Program
	promo, fault := prog.RatingKey == store.PromoKey, prog.RatingKey == store.FaultKey
	if promo {
		key += "|promo|" + prog.Note
		if strings.HasPrefix(prog.Note, store.InfoPrefix) {
			key += "|" + s.infoVersion(ctx, prog)
		}
	} else if fault {
		key += "|fault|" + prog.Note
	}
	if !promo {
		next = s.nextMain(ctx, c.ID, prog.StopTime())
	}
	if next != nil {
		key += fmt.Sprintf("|%s|%d", next.RatingKey, next.Start)
	}
	sum := sha1.Sum([]byte(key))
	path := filepath.Join(s.cfg.DataDir, "cache", "card-"+hex.EncodeToString(sum[:8])+".png")
	if keepCached(path) {
		return path, nil
	}
	var data []byte
	var err error
	switch {
	case promo:
		data, err = s.promoPNG(ctx, c, prog, height)
	case fault:
		data, err = s.faultPNG(ctx, c, prog, next, height)
	default:
		data, err = s.cardPNG(ctx, c, next, height)
	}
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// writeFileAtomic writes a cache file under a temporary name and renames it
// into place, so a parallel stream or request never reads half a file.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp" + randomHex(4)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Server) cardPNG(ctx context.Context, c *store.Channel, next *store.Program, height int) ([]byte, error) {
	return logo.RenderCard(s.cardOptions(ctx, c, next, height))
}

// cardOptions describes the "up next" slate.
func (s *Server) cardOptions(ctx context.Context, c *store.Channel, next *store.Program, height int) logo.CardOptions {
	l := s.lang(ctx)
	o := s.cardBase(c, height, i18n.T(l, "card.headline"))
	if next != nil {
		if m := s.lib.Get(ctx, next.RatingKey); m != nil {
			o.When = i18n.T(l, "card.at", i18n.Clock(l, next.StartTime().Local()))
			if next.Start-s.now().UnixMilli() < 90_000 {
				o.When = i18n.T(l, "card.now")
			}
			s.describe(ctx, &o, m)
		}
	}
	return o
}

// faultPNG renders the slate for a title that cannot be played: what is
// missing, and when and with what the channel continues.
func (s *Server) faultPNG(ctx context.Context, c *store.Channel, prog, next *store.Program, height int) ([]byte, error) {
	return logo.RenderCard(s.faultOptions(ctx, c, prog, next, height))
}

func (s *Server) faultOptions(ctx context.Context, c *store.Channel, prog, next *store.Program, height int) logo.CardOptions {
	// technical difficulties / what is missing / NEXT TITLE / when it starts
	l := s.lang(ctx)
	o := s.cardBase(c, height, i18n.T(l, "fault.headline"))
	o.When = i18n.T(l, "fault.unavailable")
	if m := s.lib.Get(ctx, prog.Note); m != nil {
		o.When = i18n.T(l, "fault.titleMissing", m.DisplayTitle())
	}
	o.Title = i18n.T(l, "fault.back")
	if next != nil {
		if m := s.lib.Get(ctx, next.RatingKey); m != nil {
			o.Title = m.DisplayTitle()
			o.Subtitle = i18n.T(l, "fault.continueAt", i18n.Clock(l, next.StartTime().Local()))
			o.Poster = s.fetchPoster(ctx, m)
		}
	}
	return o
}

func (s *Server) cardBase(c *store.Channel, height int, headline string) logo.CardOptions {
	o := logo.CardOptions{Width: height * 16 / 9, Height: height, Accent: c.Color, Headline: headline}
	if path, err := s.logoFile(c); err == nil {
		o.Logo = decodeFile(path)
	}
	return o
}

// describe fills title, subtitle and poster of a slate.
func (s *Server) describe(ctx context.Context, o *logo.CardOptions, m *store.Media) {
	s.describeText(s.lang(ctx), o, m)
	o.Poster = s.fetchPoster(ctx, m)
}

// describeText fills title and subtitle ("1985 · Adventure, Comedy").
func (s *Server) describeText(l i18n.Lang, o *logo.CardOptions, m *store.Media) {
	o.Title = m.Title
	if m.Type == "episode" {
		o.Title = m.ShowTitle
		o.Subtitle = i18n.T(l, "card.episode", m.Season, m.Episode, m.Title)
	} else {
		var parts []string
		if m.Year > 0 {
			parts = append(parts, fmt.Sprint(m.Year))
		}
		if len(m.Tags.Genre) > 0 {
			parts = append(parts, strings.Join(m.Tags.Genre[:min(2, len(m.Tags.Genre))], ", "))
		}
		o.Subtitle = strings.Join(parts, " · ")
	}
}

// promoPNG renders the slate after a trailer: when the advertised program airs
// on this channel, or when a Discover title is released.
func (s *Server) promoPNG(ctx context.Context, c *store.Channel, prog *store.Program, height int) ([]byte, error) {
	if strings.HasPrefix(prog.Note, store.InfoPrefix) {
		return s.infoPNG(ctx, c, prog, height)
	}
	return logo.RenderCard(s.promoOptions(ctx, c, prog, height))
}

func (s *Server) promoOptions(ctx context.Context, c *store.Channel, prog *store.Program, height int) logo.CardOptions {
	l := s.lang(ctx)
	if strings.HasPrefix(prog.Note, store.TrailerPrefix) {
		o := s.cardBase(c, height, i18n.T(l, "promo.soon"))
		if t, err := s.store.GetTrailer(ctx, prog.Note); err == nil {
			o.Headline, o.When = releaseText(l, t, s.now())
			s.describe(ctx, &o, t.Media(0))
			if t.Origin == store.OriginTMDb {
				s.describeSlate(ctx, l, &o, t)
			}
		}
		return o
	}
	o := s.cardBase(c, height, i18n.T(l, "promo.onChannel", c.Name))
	if ms, err := strconv.ParseInt(prog.Note, 10, 64); err == nil {
		if target, err := s.store.ProgramAt(ctx, c.ID, time.UnixMilli(ms)); err == nil {
			if m := s.lib.Get(ctx, target.RatingKey); m != nil {
				o.When = airText(l, target.StartTime(), s.now())
				s.describe(ctx, &o, m)
			}
		}
	}
	return o
}

// airText: "Today at 10:00 PM", "Tomorrow at 8:15 PM", "Saturday at 8:15 PM".
func airText(l i18n.Lang, at, now time.Time) string {
	at, now = at.Local(), now.Local()
	clock := i18n.Clock(l, at)
	y1, m1, d1 := now.Date()
	day := time.Date(y1, m1, d1, 0, 0, 0, 0, time.Local)
	switch {
	case at.Before(day.AddDate(0, 0, 1)):
		return i18n.T(l, "air.today", clock)
	case at.Before(day.AddDate(0, 0, 2)):
		return i18n.T(l, "air.tomorrow", clock)
	}
	return i18n.T(l, "air.weekday", i18n.Weekday(l, at.Weekday()), clock)
}

// releaseText returns headline and date line for a Discover trailer.
func releaseText(l i18n.Lang, t *store.Trailer, now time.Time) (string, string) {
	rel, err := time.ParseInLocation("2006-01-02", t.Released, time.Local)
	if err != nil {
		return i18n.T(l, "release.soon"), ""
	}
	date := i18n.Date(l, rel, rel.Year() != now.Year())
	// Only the "Upcoming blockbusters" list says for sure that a title runs in cinemas.
	cinema := false
	for _, h := range t.Hubs {
		cinema = cinema || strings.Contains(h, "blockbuster")
	}
	switch {
	case rel.After(now) && t.Origin == store.OriginTMDb && t.Kind == "show" && t.Season > 0:
		return i18n.T(l, "release.newSeason"), i18n.T(l, "release.seasonFrom", t.Season, date)
	case rel.After(now) && t.Origin == store.OriginTMDb && t.Kind == "show":
		// TMDb lists shows by their first air date: a new show, not a new season
		return i18n.T(l, "release.newShow"), i18n.T(l, "release.from", date)
	case rel.After(now) && (cinema || t.Kind == "movie"):
		// A plain "From <date>" would read as if the film came to this server then;
		// Discover gives upcoming films their theatrical release date.
		return i18n.T(l, "release.cinemaSoon"), i18n.T(l, "release.cinemaFrom", date)
	case rel.After(now):
		return i18n.T(l, "release.soon"), i18n.T(l, "release.from", date)
	case cinema && now.Sub(rel) < 60*24*time.Hour:
		return i18n.T(l, "release.cinemaNow"), i18n.T(l, "release.since", date)
	}
	return i18n.T(l, "release.new"), i18n.T(l, "release.since", date)
}

// describeSlate adds what a TMDb slate shows: genres, rating and running time
// (movies), the summary, the backdrop and the source.
func (s *Server) describeSlate(ctx context.Context, l i18n.Lang, o *logo.CardOptions, t *store.Trailer) {
	var parts []string
	if len(t.Genres) > 0 {
		parts = append(parts, strings.Join(t.Genres[:min(2, len(t.Genres))], ", "))
	}
	if r := t.ContentRating; r != "" {
		if age, ok := strings.CutPrefix(r, "de/"); ok {
			r = i18n.T(l, "slate.rating", age)
		}
		parts = append(parts, r)
	}
	if t.RuntimeMin > 0 {
		parts = append(parts, i18n.T(l, "slate.runtime", t.RuntimeMin/60, t.RuntimeMin%60))
	}
	o.Subtitle = strings.Join(parts, " · ")
	o.Summary = t.Summary
	o.Credit = i18n.T(l, "slate.credit")
	if t.Art != "" {
		o.Backdrop = s.fetchImage(ctx, t.Art)
	}
}

func (s *Server) fetchPoster(ctx context.Context, m *store.Media) image.Image {
	path := m.Thumb
	if m.Type == "episode" && m.ShowThumb != "" {
		path = m.ShowThumb
	}
	if path == "" {
		return nil
	}
	u, err := s.imageURL(ctx, path, 480, 720)
	if err != nil {
		return nil
	}
	return s.fetchImage(ctx, u)
}

// fetchImage downloads and decodes a picture (nil on any problem).
func (s *Server) fetchImage(ctx context.Context, u string) image.Image {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil
	}
	return img
}

// watermark returns the burned-in logo settings for a channel, if enabled.
func (s *Server) watermark(c *store.Channel, enabled bool, position string, opacity, size int, tone string) *stream.Watermark {
	if !enabled {
		return nil
	}
	path, err := s.logoFile(c)
	if err != nil {
		s.log.Warn("watermark logo", "err", err)
		return nil
	}
	return &stream.Watermark{Path: path, Position: position, Opacity: opacity, Size: size, Tone: tone}
}

// handleCardPreview renders the slate for the editor preview.
func (s *Server) handleCardPreview(w http.ResponseWriter, r *http.Request) {
	c := s.channelFromPath(w, r)
	if c == nil {
		return
	}
	now := time.Now()
	next := s.nextMain(r.Context(), c.ID, now)
	data, err := s.cardPNG(r.Context(), c, next, 720)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	// with the time, placed like the overlay in the stream (stream.cardClockClip)
	card, err1 := png.Decode(bytes.NewReader(data))
	clockPNG, err2 := logo.RenderClock(i18n.Clock(s.lang(r.Context()), now), 720, stream.ClockPad(720))
	if err1 == nil && err2 == nil {
		if clock, err := png.Decode(bytes.NewReader(clockPNG)); err == nil {
			img := image.NewRGBA(card.Bounds())
			draw.Draw(img, img.Bounds(), card, image.Point{}, draw.Src)
			right, top := stream.CardClockMargin(720)
			b := clock.Bounds()
			draw.Draw(img, b.Add(image.Pt(img.Bounds().Dx()-b.Dx()-right, top)), clock, image.Point{}, draw.Over)
			var buf bytes.Buffer
			if png.Encode(&buf, img) == nil {
				data = buf.Bytes()
			}
		}
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// keepCached reports whether a cached file exists and marks it as used, so
// purgeCache (which goes by the modification time) keeps it while it is needed,
// e.g. a logo a running stream burns in.
func keepCached(path string) bool {
	now := time.Now()
	return os.Chtimes(path, now, now) == nil
}

// purgeCache removes rendered slates and stale logos that were not used for a while.
func (s *Server) purgeCache() {
	dir := filepath.Join(s.cfg.DataDir, "cache")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		maxAge := 30 * 24 * time.Hour
		switch {
		case strings.HasPrefix(e.Name(), "credits-"), strings.HasPrefix(e.Name(), "creditscard-"), strings.HasPrefix(e.Name(), "creditslist-"):
			// panels and cards of end credits (directories) and their play lists
			if time.Since(info.ModTime()) > 2*24*time.Hour {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
			continue
		case strings.HasPrefix(e.Name(), "card-"), strings.HasPrefix(e.Name(), "caption-"), strings.HasPrefix(e.Name(), "clock-"),
			strings.HasPrefix(e.Name(), "theme-"):
			maxAge = 2 * 24 * time.Hour
		case strings.HasPrefix(e.Name(), "clip-"):
			// prefetched clips are removed after use; this only catches leftovers of a crash
			maxAge = 6 * time.Hour
		}
		if time.Since(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// cardThemeMin: only longer slates play the theme music of the title they
// announce; shorter ones keep the usual music.
const cardThemeMin = 10 * time.Second

// cardAudio returns the audio for a slate ("" if music is disabled): the theme
// music of the announced title if Plex has one, otherwise the background music.
func (s *Server) cardAudio(ctx context.Context, c *store.Channel, prog *store.Program) (string, bool) {
	// the short slate after a trailer stays silent, like the cut back to the programme
	tmdbSlate := prog.RatingKey == store.PromoKey &&
		(strings.HasPrefix(prog.Note, store.TrailerPrefix+"tmdb-") || strings.HasPrefix(prog.Note, store.InfoPrefix))
	if s.store.Setting(ctx, store.SetCardMusic, "1") != "1" || prog.RatingKey == store.PromoKey && !tmdbSlate {
		return "", false
	}
	if theme := s.cardTheme(ctx, c, prog); theme != "" {
		return theme, true
	}
	customDir := filepath.Join(s.cfg.DataDir, "music")
	if entries, err := os.ReadDir(customDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				ext := strings.ToLower(filepath.Ext(e.Name()))
				if ext == ".mp3" || ext == ".m4a" || ext == ".aac" || ext == ".wav" || ext == ".ogg" || ext == ".flac" {
					return filepath.Join(customDir, e.Name()), false
				}
			}
		}
	}
	return s.streamer.DefaultMusicPath(), false
}

// cardTheme returns the theme music of the title a slate announces ("" = none):
// what comes next. Episodes use the theme of their show.
func (s *Server) cardTheme(ctx context.Context, c *store.Channel, prog *store.Program) string {
	if prog.StopTime().Sub(prog.StartTime()) <= cardThemeMin {
		return ""
	}
	if prog.RatingKey == store.FaultKey {
		return ""
	}
	target := s.nextMain(ctx, c.ID, prog.StopTime())
	if target == nil {
		return ""
	}
	m := s.lib.Get(ctx, target.RatingKey)
	if m == nil {
		return ""
	}
	key := m.RatingKey
	if m.ShowKey != "" {
		key = m.ShowKey
	}
	return s.themeFile(ctx, key)
}

// themeMaxAge: cached theme music (or the note that there is none) is fetched
// again after this, so themes added in Plex show up.
const themeMaxAge = 24 * time.Hour

// themeFile returns the theme music of a Plex item as a cached file ("" = none).
// Plex is asked briefly: the slate must not wait long for it.
func (s *Server) themeFile(ctx context.Context, ratingKey string) string {
	sum := sha1.Sum([]byte(ratingKey))
	path := filepath.Join(s.cfg.DataDir, "cache", "theme-"+hex.EncodeToString(sum[:8])+".mp3")
	cached := ""
	if info, err := os.Stat(path); err == nil {
		if info.Size() > 0 {
			cached = path
		}
		if time.Since(info.ModTime()) < themeMaxAge {
			return cached
		}
	}
	srv, err := s.mediaServer(ctx)
	if err != nil {
		return cached
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := srv.Theme(ctx, ratingKey)
	if err != nil && !errors.Is(err, mediaserver.ErrNotFound) {
		s.log.Warn("theme music", "ratingKey", ratingKey, "err", err)
		return cached
	}
	// an empty file notes that the item has no theme
	if err := writeFileAtomic(path, data); err != nil {
		return cached
	}
	if len(data) == 0 {
		return ""
	}
	return path
}
