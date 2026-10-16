package server

// Pages of long "up next" slates: the slate itself, what the coming title is
// about, the weather and the news, changing every few seconds (the stream
// blends them, see stream.Streamer.CardPages). Pages without content (no
// summary, no place, no feeds or no data yet) are left out.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/store"
)

// renderCardPages returns the pictures of the pages of an "up next" slate; the
// first is the slate itself.
func (s *Server) renderCardPages(ctx context.Context, c *store.Channel, prog *store.Program, height int) ([]string, error) {
	first, err := s.renderCard(ctx, c, prog, height)
	if err != nil || prog.RatingKey != store.CardKey {
		return []string{first}, err
	}
	next := s.nextMain(ctx, c.ID, prog.StopTime())
	if next == nil {
		return []string{first}, nil
	}
	m := s.lib.Get(ctx, next.RatingKey)
	if m == nil {
		return []string{first}, nil
	}
	pages := []string{first}
	l := s.lang(ctx)
	o := s.cardOptions(ctx, c, next, height)
	base := fmt.Sprintf("%d|%d|%s|%s|%s|%s|%s|%d", c.ID, height, c.Logo, s.autoLogoVersion(c), c.Color, l, m.RatingKey, next.Start)
	add := func(kind, version string, render func() ([]byte, error)) {
		path, err := s.cachedPage(kind, base+"|"+version, render)
		if err != nil {
			s.log.Warn("slate page", "page", kind, "err", err)
			return
		}
		pages = append(pages, path)
	}

	if strings.TrimSpace(m.Summary) != "" {
		add("about", m.Summary, func() ([]byte, error) {
			d := o
			d.Headline, d.When, d.Summary = "", "", m.Summary
			return logo.RenderDescription(d)
		})
	}

	// weather and news from what was fetched last: a slate does not wait for them
	footer := &logo.InfoFooter{
		Kicker: i18n.T(l, "caption.sep", i18n.T(l, "card.headline"), i18n.T(l, "card.at", i18n.Clock(l, next.StartTime().Local()))),
		Title:  o.Title,
	}
	s.info.mu.Lock()
	rep, hs, newsAt := s.info.weather, s.info.news, s.info.newsLast
	s.info.mu.Unlock()
	if p := s.weatherPlace(ctx); p != nil {
		if rep == nil {
			s.startInfo("weather")
		} else {
			add("weather", fmt.Sprint(rep.Fetched.UnixMilli(), p.Name, prog.Start), func() ([]byte, error) {
				wo := weatherSlate(l, s.infoHeader(ctx, c, height, ""), rep, p.Name, false, s.fahrenheit(ctx), prog.StartTime())
				wo.Footer = footer
				return logo.RenderWeather(wo)
			})
		}
	}
	if feeds := s.store.Setting(ctx, store.SetNewsFeeds, ""); feeds != "" {
		if len(hs) == 0 {
			s.startInfo("news")
		} else {
			add("news", fmt.Sprint(newsAt.UnixMilli(), feeds, prog.Start), func() ([]byte, error) {
				no := newsSlate(l, s.infoHeader(ctx, c, height, ""), hs, newsAt,
					&store.Program{Note: store.InfoPrefix + "news-1", Start: prog.Start})
				no.Footer = footer
				return logo.RenderNews(no)
			})
		}
	}
	return pages, nil
}

// cachedPage renders a page picture once per key.
func (s *Server) cachedPage(kind, key string, render func() ([]byte, error)) (string, error) {
	sum := sha1.Sum([]byte(kind + "|" + key + "|v1"))
	path := filepath.Join(s.cfg.DataDir, "cache", "page-"+kind+"-"+hex.EncodeToString(sum[:8])+".png")
	if keepCached(path) {
		return path, nil
	}
	data, err := render()
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}
