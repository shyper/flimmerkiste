package server

// Weather and news slates: on Jellyfin and Emby they fill breaks next to the
// "coming to theaters" slates, so a long break is not the same three slates
// over and over; on every platform they are pages of long "up next" slates
// (see pages.go). The weather comes from Open-Meteo for one place per
// installation, the news are the headlines of
// RSS or Atom feeds the owner enters. Both are fetched in the background and
// drawn when the slate airs, so they are current.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/news"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/weather"
)

const (
	weatherInterval = time.Hour
	newsInterval    = 15 * time.Minute
	// newsMaxAge: older headlines are not shown.
	newsMaxAge = 36 * time.Hour
	// newsPerSlate: headlines on a slate in two columns (fewer if long ones need the room).
	newsPerSlate = 6
	// newsPages: at most this many slates of different headlines.
	newsPages = 5
	maxFeeds  = 8
)

// weatherClient reads Bright Sky (tests point it elsewhere).
var weatherClient = &weather.Client{}

type infoState struct {
	mu          sync.Mutex
	weather     *weather.Report
	weatherErr  string
	weatherLast time.Time
	news        []news.Headline
	newsErr     string
	newsLast    time.Time
	running     map[string]bool
	waiting     map[string]chan struct{} // a slate's fetch of missing data, which others wait for
}

// weatherPlace is the place of the weather slates (nil = off).
func (s *Server) weatherPlace(ctx context.Context) *weather.Place {
	v := s.store.Setting(ctx, store.SetWeatherPlace, "")
	if v == "" {
		return nil
	}
	var p weather.Place
	if json.Unmarshal([]byte(v), &p) != nil || p.Name == "" {
		return nil
	}
	return &p
}

// newsFeeds are the feeds of the news slates.
func (s *Server) newsFeeds(ctx context.Context) []string {
	return splitFeeds(s.store.Setting(ctx, store.SetNewsFeeds, ""))
}

func splitFeeds(v string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' || r == ' ' || r == ',' }) {
		if u, err := url.Parse(f); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && len(out) < maxFeeds {
			out = append(out, f)
		}
	}
	return out
}

// maybeInfo refreshes weather and news when they are due.
func (s *Server) maybeInfo(ctx context.Context) {
	s.info.mu.Lock()
	wDue, nDue := time.Since(s.info.weatherLast) > weatherInterval, time.Since(s.info.newsLast) > newsInterval
	s.info.mu.Unlock()
	if wDue && s.weatherPlace(ctx) != nil {
		s.startInfo("weather")
	}
	if nDue && len(s.newsFeeds(ctx)) > 0 {
		s.startInfo("news")
	}
}

// startInfo fetches weather or news in the background unless running.
func (s *Server) startInfo(kind string) bool {
	s.info.mu.Lock()
	if s.info.running[kind] {
		s.info.mu.Unlock()
		return false
	}
	if s.info.running == nil {
		s.info.running = map[string]bool{}
	}
	s.info.running[kind] = true
	s.info.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.fetchInfo(ctx, kind)
		s.info.mu.Lock()
		s.info.running[kind] = false
		s.info.mu.Unlock()
	}()
	return true
}

func (s *Server) fetchInfo(ctx context.Context, kind string) {
	l := s.lang(ctx)
	now := s.now()
	if kind == "weather" {
		var rep *weather.Report
		var err error
		place := s.store.Setting(ctx, store.SetWeatherPlace, "")
		if p := s.weatherPlace(ctx); p == nil {
			err = i18n.E("weather.noPlace")
		} else if rep, err = weatherClient.Load(ctx, *p, 5, now); err != nil {
			err = i18n.E("weather.load", err)
		}
		if place != s.store.Setting(ctx, store.SetWeatherPlace, "") {
			return // the place changed meanwhile
		}
		s.info.mu.Lock()
		s.info.weatherLast, s.info.weatherErr = now, ""
		if err != nil {
			s.info.weatherErr = i18n.Message(l, err)
			s.log.Warn("weather", "err", err)
		} else {
			s.info.weather = rep
		}
		s.info.mu.Unlock()
		return
	}
	setting := s.store.Setting(ctx, store.SetNewsFeeds, "")
	feeds := splitFeeds(setting)
	var all [][]news.Headline
	var errs []string
	for _, f := range feeds {
		hs, err := news.Fetch(ctx, f)
		if err != nil {
			host := f
			if u, err := url.Parse(f); err == nil {
				host = u.Host
			}
			errs = append(errs, i18n.T(l, "news.feedFailed", host, err))
			s.log.Warn("news feed", "url", f, "err", err)
			continue
		}
		all = append(all, hs)
	}
	merged := news.Merge(all, now, newsMaxAge)
	if setting != s.store.Setting(ctx, store.SetNewsFeeds, "") {
		return // the feeds changed meanwhile
	}
	s.info.mu.Lock()
	s.info.newsLast, s.info.newsErr = now, strings.Join(errs, "; ")
	if len(feeds) == 0 {
		s.info.newsErr = i18n.T(l, "news.noFeeds")
	}
	if len(merged) > 0 || len(feeds) == 0 {
		s.info.news = merged
	}
	s.info.mu.Unlock()
}

// infoData returns the current weather or news, fetching them first if none
// were fetched yet (a slate airs right after a restart).
func (s *Server) infoData(ctx context.Context, kind string) (*weather.Report, []news.Headline) {
	s.info.mu.Lock()
	rep, hs := s.info.weather, s.info.news
	if (kind == "weather" && rep != nil) || (kind == "news" && hs != nil) {
		s.info.mu.Unlock()
		return rep, hs
	}
	// one fetch for all streams that start at once
	done, fetching := s.info.waiting[kind]
	if !fetching {
		if s.info.waiting == nil {
			s.info.waiting = map[string]chan struct{}{}
		}
		done = make(chan struct{})
		s.info.waiting[kind] = done
	}
	s.info.mu.Unlock()
	if fetching {
		select {
		case <-done:
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	} else {
		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		s.fetchInfo(fctx, kind)
		cancel()
		s.info.mu.Lock()
		delete(s.info.waiting, kind)
		s.info.mu.Unlock()
		close(done)
	}
	s.info.mu.Lock()
	rep, hs = s.info.weather, s.info.news
	s.info.mu.Unlock()
	return rep, hs
}

// infoVersion names the data an info slate shows, for the slate cache; it
// fetches missing data first, so a slate drawn from it gets its own key.
// Without data the "up next" slate stands in, which depends on the program.
func (s *Server) infoVersion(ctx context.Context, prog *store.Program) string {
	if strings.Contains(prog.Note, "weather") {
		if s.weatherPlace(ctx) != nil {
			s.infoData(ctx, "weather")
		}
		place := s.store.Setting(ctx, store.SetWeatherPlace, "")
		s.info.mu.Lock()
		defer s.info.mu.Unlock()
		if s.info.weather == nil || place == "" {
			return fmt.Sprint("none|", prog.Start)
		}
		return fmt.Sprint(s.info.weather.Fetched.UnixMilli(), place)
	}
	feeds := s.store.Setting(ctx, store.SetNewsFeeds, "")
	if feeds != "" {
		s.infoData(ctx, "news")
	}
	s.info.mu.Lock()
	defer s.info.mu.Unlock()
	if len(s.info.news) == 0 || feeds == "" {
		return fmt.Sprint("none|", prog.Start)
	}
	return fmt.Sprintf("%d|%d|%s", s.info.newsLast.UnixMilli(), prog.Start, feeds)
}

func (s *Server) infoHeader(ctx context.Context, c *store.Channel, height int, headline string) logo.InfoHeader {
	o := logo.InfoHeader{Width: height * 16 / 9, Height: height, Accent: c.Color, Headline: headline}
	if path, err := s.logoFile(c); err == nil {
		o.Logo = decodeFile(path)
	}
	return o
}

// infoPNG draws a weather or news slate; without data it falls back to the
// "up next" slate.
func (s *Server) infoPNG(ctx context.Context, c *store.Channel, prog *store.Program, height int) ([]byte, error) {
	if strings.HasPrefix(prog.Note, store.InfoPrefix+"weather") {
		if o, ok := s.weatherOptions(ctx, c, prog, height); ok {
			return logo.RenderWeather(o)
		}
	} else if o, ok := s.newsOptions(ctx, c, prog, height); ok {
		return logo.RenderNews(o)
	}
	return s.cardPNG(ctx, c, s.nextMain(ctx, c.ID, prog.StopTime()), height)
}

// fahrenheit: temperatures in °F and wind in mph for the United States.
func (s *Server) fahrenheit(ctx context.Context) bool {
	return s.tmdbRegion(ctx) == "US"
}

func (s *Server) weatherOptions(ctx context.Context, c *store.Channel, prog *store.Program, height int) (logo.WeatherOptions, bool) {
	rep, _ := s.infoData(ctx, "weather")
	p := s.weatherPlace(ctx)
	if rep == nil || p == nil {
		return logo.WeatherOptions{}, false
	}
	return weatherSlate(s.lang(ctx), s.infoHeader(ctx, c, height, ""), rep, p.Name, prog.Note == store.InfoPrefix+"weather-days",
		s.fahrenheit(ctx), prog.StartTime()), true
}

// weatherSlate fills a weather slate: now and the coming hours, or the next days.
func weatherSlate(l i18n.Lang, h logo.InfoHeader, rep *weather.Report, place string, days, fahrenheit bool, at time.Time) logo.WeatherOptions {
	at = at.Local()
	temp := func(c float64) string {
		if fahrenheit {
			c = c*9/5 + 32
		}
		return fmt.Sprintf("%d°", int(math.Round(c)))
	}
	rain := func(p int) string {
		if p < 0 {
			return ""
		}
		return i18n.T(l, "weather.rain", p)
	}
	cond := func(icon string) string {
		switch icon {
		case "wind", "rain":
			return i18n.T(l, "weather."+icon+".icon")
		case "":
			return ""
		}
		return i18n.T(l, "weather."+icon)
	}
	o := logo.WeatherOptions{InfoHeader: h}
	o.Credit = i18n.T(l, "weather.credit")
	if days {
		o.Headline, o.Line = i18n.T(l, "weather.outlook"), place
		for _, d := range rep.Days(at.AddDate(0, 0, 1), 4) {
			o.Slots = append(o.Slots, logo.WeatherSlot{Label: i18n.Weekday(l, d.Date.Weekday()), Sub: i18n.Date(l, d.Date, false),
				Icon: d.Icon, Temp: temp(d.Max), Low: temp(d.Min), Rain: rain(d.RainChance)})
		}
		return o
	}
	o.Headline = i18n.T(l, "weather.headline")
	o.Line = place + " · " + i18n.T(l, "weather.asOf", i18n.Clock(l, rep.Now.Time.Local()))
	o.Now = logo.WeatherSlot{Label: cond(rep.Now.Icon), Icon: rep.Now.Icon, Temp: temp(rep.Now.Temperature)}
	if fahrenheit {
		o.NowDetail = i18n.T(l, "weather.windMph", int(math.Round(rep.Now.Wind*0.621371)))
	} else {
		o.NowDetail = i18n.T(l, "weather.wind", int(math.Round(rep.Now.Wind)))
	}
	for t := at.Truncate(time.Hour).Add(3 * time.Hour); len(o.Slots) < 4; t = t.Add(3 * time.Hour) {
		hr, ok := rep.At(t)
		if !ok {
			break
		}
		hour := fmt.Sprint(hr.Time.Local().Hour())
		if l != i18n.DE {
			hour = hr.Time.Local().Format("3 PM")
		}
		o.Slots = append(o.Slots, logo.WeatherSlot{Label: i18n.T(l, "weather.hour", hour), Icon: hr.Icon, Temp: temp(hr.Temperature), Rain: rain(hr.RainChance)})
	}
	return o
}

func (s *Server) newsOptions(ctx context.Context, c *store.Channel, prog *store.Program, height int) (logo.NewsOptions, bool) {
	_, hs := s.infoData(ctx, "news")
	if len(hs) == 0 {
		return logo.NewsOptions{}, false
	}
	s.info.mu.Lock()
	fetched := s.info.newsLast
	s.info.mu.Unlock()
	return newsSlate(s.lang(ctx), s.infoHeader(ctx, c, height, ""), hs, fetched, prog), true
}

// newsSlate fills a news slate with one page of headlines: slates of the same
// break (news-1, news-2) and of later breaks show different pages.
func newsSlate(l i18n.Lang, h logo.InfoHeader, hs []news.Headline, fetched time.Time, prog *store.Program) logo.NewsOptions {
	o := logo.NewsOptions{InfoHeader: h}
	o.Headline = i18n.T(l, "news.headline")
	o.Line = i18n.T(l, "news.asOf", i18n.Clock(l, fetched.Local()))
	pages := min(newsPages, (len(hs)+newsPerSlate-1)/newsPerSlate)
	idx := 0
	fmt.Sscanf(strings.TrimPrefix(prog.Note, store.InfoPrefix+"news-"), "%d", &idx)
	page := (int(prog.Start/60000) + idx) % max(1, pages)
	// the last page shows the last full set, overlapping the one before
	from := max(0, min(page*newsPerSlate, len(hs)-newsPerSlate))
	for _, hl := range hs[from:min(len(hs), from+newsPerSlate)] {
		meta := hl.Source
		if !hl.Published.IsZero() {
			meta += " · " + i18n.Clock(l, hl.Published.Local())
		}
		o.Items = append(o.Items, logo.NewsItem{Title: hl.Title, Meta: meta})
	}
	return o
}

// infoStatus is what the settings show about weather and news.
func (s *Server) infoStatus(ctx context.Context) map[string]any {
	s.info.mu.Lock()
	defer s.info.mu.Unlock()
	w := map[string]any{"running": s.info.running["weather"], "error": s.info.weatherErr}
	if !s.info.weatherLast.IsZero() {
		w["last"] = s.info.weatherLast.Unix()
	}
	n := map[string]any{"running": s.info.running["news"], "error": s.info.newsErr, "count": len(s.info.news)}
	if !s.info.newsLast.IsZero() {
		n["last"] = s.info.newsLast.Unix()
	}
	return map[string]any{"weather": w, "news": n}
}

// handlePlaces looks places up for the weather.
func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeJSON(w, http.StatusOK, []weather.Place{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	places, err := weatherClient.Search(ctx, q, string(s.lang(ctx)))
	if err != nil {
		s.failErr(w, r, http.StatusBadGateway, i18n.E("weather.load", err))
		return
	}
	if places == nil {
		places = []weather.Place{}
	}
	writeJSON(w, http.StatusOK, places)
}

// handleInfoRefresh fetches weather and news now.
func (s *Server) handleInfoRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	started := false
	if s.weatherPlace(ctx) != nil {
		started = s.startInfo("weather") || started
	}
	if len(s.newsFeeds(ctx)) > 0 {
		started = s.startInfo("news") || started
	}
	if !started && s.weatherPlace(ctx) == nil && len(s.newsFeeds(ctx)) == 0 {
		s.failErr(w, r, http.StatusBadRequest, errors.New(i18n.T(s.lang(ctx), "news.noFeeds")))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": started})
}

// infoSettingsChanged fetches what was switched on and re-plans the breaks
// beyond the stable window, so the slates join (or leave) them.
func (s *Server) infoSettingsChanged(kinds map[string]bool) {
	ctx := context.Background()
	s.info.mu.Lock()
	if kinds["weather"] {
		s.info.weather, s.info.weatherErr, s.info.weatherLast = nil, "", time.Time{}
	}
	if kinds["news"] {
		s.info.news, s.info.newsErr, s.info.newsLast = nil, "", time.Time{}
	}
	s.info.mu.Unlock()
	s.maybeInfo(ctx)
	if s.platform(ctx) == "plex" {
		return // the slates only join breaks on Jellyfin and Emby
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		chans, err := s.store.ListChannels(ctx)
		if err != nil {
			return
		}
		from := time.Now().Add(stableWindow)
		for _, c := range chans {
			if cfg, err := schedule.ParseConfig(c.Config); err == nil && schedule.ShowsSlates(cfg) {
				if err := s.sched.Replan(ctx, c.ID, from); err != nil {
					s.log.Warn("re-plan", "channel", c.Name, "err", err)
				}
			}
		}
	}()
}
