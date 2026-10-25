package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/store"
)

// captionReach is how far ahead a trailer's title is looked up in the guides.
// It stays below a week, so "Saturday at 8:15 PM" is never ambiguous.
const captionReach = 6 * 24 * time.Hour

// renderCaption returns the lower third PNG for a trailer program ("" when
// captions are off): the title, and when it airs on one of the channels or
// when it comes to cinemas.
func (s *Server) renderCaption(ctx context.Context, c *store.Channel, prog *store.Program, height int) (string, error) {
	if s.store.Setting(ctx, store.SetTrailerCaption, "0") != "1" {
		return "", nil
	}
	t, err := s.store.GetTrailer(ctx, prog.RatingKey)
	if err != nil {
		return "", err
	}
	now := s.now()
	o := logo.CaptionOptions{Height: height, Accent: c.Color, Kicker: s.captionKicker(ctx, c, t, now)}
	var card logo.CardOptions
	m := t.Media(0)
	s.describeText(s.lang(ctx), &card, m)
	o.Title, o.Sub = card.Title, card.Subtitle

	key := strings.Join([]string{fmt.Sprint(height), o.Accent, o.Kicker, o.Title, o.Sub, m.Thumb, "v6"}, "|")
	sum := sha1.Sum([]byte(key))
	path := filepath.Join(s.cfg.DataDir, "cache", "caption-"+hex.EncodeToString(sum[:8])+".png")
	if keepCached(path) {
		return path, nil
	}
	o.Poster = s.fetchPoster(ctx, m)
	data, err := logo.RenderCaption(o)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// captionKicker: "Today at 10:00 PM" (this channel), "Tomorrow at 8:15 PM on
// Comedies" (another one), or for Discover titles "Coming to theaters · November 5".
func (s *Server) captionKicker(ctx context.Context, c *store.Channel, t *store.Trailer, now time.Time) string {
	l := s.lang(ctx)
	if t.Origin != store.OriginLibrary {
		headline, date := releaseText(l, t, now)
		if date == "" {
			return headline
		}
		return i18n.T(l, "caption.sep", headline, date)
	}
	if t.Advertised() == "" {
		return ""
	}
	if at, ok := s.nextAiring(ctx, c.ID, t, now); ok {
		return airText(l, at, now)
	}
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		return ""
	}
	var best time.Time
	name := ""
	for _, other := range chans {
		if other.ID == c.ID || !other.Enabled {
			continue
		}
		if at, ok := s.nextAiring(ctx, other.ID, t, now); ok && (best.IsZero() || at.Before(best)) {
			best, name = at, other.Name
		}
	}
	if best.IsZero() {
		return ""
	}
	return i18n.T(l, "air.onChannel", airText(l, best, now), name)
}

// nextAiring returns when the title a library trailer advertises starts next
// on a channel (within captionReach); for a show, any of its episodes.
func (s *Server) nextAiring(ctx context.Context, channelID int64, t *store.Trailer, now time.Time) (time.Time, bool) {
	progs, err := s.store.ProgramsBetween(ctx, channelID, now, now.Add(captionReach))
	if err != nil {
		return time.Time{}, false
	}
	for _, p := range progs {
		if !p.IsMain() || p.Start <= now.UnixMilli() {
			continue
		}
		if t.ShowKey == "" && p.RatingKey == t.MediaKey {
			return p.StartTime(), true
		}
		if t.ShowKey != "" {
			if m := s.lib.Get(ctx, p.RatingKey); m != nil && m.ShowKey == t.ShowKey {
				return p.StartTime(), true
			}
		}
	}
	return time.Time{}, false
}
