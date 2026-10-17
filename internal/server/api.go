package server

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/ai"
	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/jellyfin"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/plex"
	"github.com/shyper/flimmerkiste/internal/rules"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/stream"
	"github.com/shyper/flimmerkiste/internal/weather"
)

// ---- views ----

type mediaView struct {
	Key           string   `json:"key"`
	Type          string   `json:"type"`
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle,omitempty"`
	Year          int      `json:"year,omitempty"`
	DurationMs    int64    `json:"durationMs"`
	Summary       string   `json:"summary,omitempty"`
	Thumb         string   `json:"thumb"`
	Genres        []string `json:"genres,omitempty"`
	ContentRating string   `json:"contentRating,omitempty"`
	Rating        float64  `json:"rating,omitempty"`
	ShowKey       string   `json:"showKey,omitempty"`
	Watched       bool     `json:"watched"`
}

func viewOf(m *store.Media, withSummary bool) mediaView {
	v := mediaView{
		Key: m.RatingKey, Type: m.Type, Title: m.Title, Year: m.Year, DurationMs: m.DurationMs,
		Thumb: "/img/" + m.RatingKey + "/thumb", Genres: m.Tags.Genre, ContentRating: m.ContentRating,
		Rating: m.Rating, Watched: m.ViewCount > 0,
	}
	if m.Type == "episode" {
		v.Title = m.ShowTitle
		v.Subtitle = fmt.Sprintf("S%02dE%02d · %s", m.Season, m.Episode, m.Title)
		v.Thumb = "/img/" + m.RatingKey + "/show"
		v.ShowKey = m.ShowKey
	}
	if withSummary {
		v.Summary = m.Summary
	}
	return v
}

type programView struct {
	Start  int64     `json:"start"`
	Stop   int64     `json:"stop"`
	Note   string    `json:"note,omitempty"`
	Source string    `json:"source,omitempty"`
	Media  mediaView `json:"media"`
}

type channelView struct {
	*store.Channel
	LogoURL  string `json:"logoUrl"`
	AutoLogo bool   `json:"autoLogo"`
	// CustomLogoURL is the uploaded logo; a seasonal channel shows it outside its seasons.
	CustomLogoURL string       `json:"customLogoUrl,omitempty"`
	Now           *programView `json:"now,omitempty"`
	Next          *programView `json:"next,omitempty"`
	Error         string       `json:"error,omitempty"`
	Missing       []string     `json:"missing,omitempty"`
	// PlanChanged: the saved changes affect the programme; saving does not
	// re-plan, the editor offers it.
	PlanChanged bool `json:"planChanged,omitempty"`
}

func (s *Server) channelView(ctx context.Context, c *store.Channel, withNow bool) channelView {
	v := channelView{Channel: c, LogoURL: s.logoPath(ctx, c), AutoLogo: c.Logo == "", Error: i18n.Message(s.lang(ctx), s.sched.LastError(c.ID))}
	if c.Logo != "" {
		v.CustomLogoURL = "/logos/" + c.Logo
	}
	if withNow && s.fillerOnly(c) {
		now := time.Now()
		v.Now = &noInfoViews(s.lang(ctx), now, now)[0]
	} else if withNow {
		now := time.Now()
		progs := s.mainPrograms(ctx, c.ID, now, now.Add(12*time.Hour))
		for i := range progs {
			m := s.lib.Get(ctx, progs[i].RatingKey)
			if m == nil {
				continue
			}
			pv := &programView{Start: progs[i].Start, Stop: progs[i].Stop, Note: progs[i].Note, Source: progs[i].Source, Media: viewOf(m, false)}
			if v.Now == nil {
				v.Now = pv
			} else {
				v.Next = pv
				break
			}
		}
	}
	return v
}

// fillerOnly reports a channel that plays only trailers or clips (no titles of its own).
func (s *Server) fillerOnly(c *store.Channel) bool {
	cfg, err := schedule.ParseConfig(c.Config)
	return err == nil && cfg.FillerOnly(c.Kind)
}

// noInfoViews shows such a channel in the web UI like in the Plex guide.
func noInfoViews(l i18n.Lang, from, to time.Time) []programView {
	var out []programView
	for _, d := range noInfoBlocks(from, to) {
		out = append(out, programView{Start: d[0].UnixMilli(), Stop: d[1].UnixMilli(), Media: mediaView{Title: i18n.T(l, "guide.noInfo")}})
	}
	return out
}

// ---- meta ----

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	libs, _ := s.store.ListLibraries(ctx)
	l := s.reqLang(r)
	fields := make([]rules.FieldInfo, len(rules.Fields))
	for i, f := range rules.Fields {
		f.Label = i18n.T(l, "rules.field."+f.ID)
		if f.Unit != "" {
			f.Unit = i18n.T(l, "rules.unit."+f.ID)
		}
		fields[i] = f
	}
	styles := make([]logo.StyleInfo, len(logo.Styles))
	for i, st := range logo.Styles {
		st.Label, st.Hint = i18n.T(l, "logo.style."+st.ID), i18n.T(l, "logo.style."+st.ID+".hint")
		styles[i] = st
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":         userFrom(r),
		"language":     l,
		"fields":       fields,
		"logoStyles":   styles,
		"libraries":    libs,
		"aiEnabled":    s.store.Setting(ctx, store.SetAIProvider, "") != "",
		"serverName":   s.store.Setting(ctx, store.SetServerName, ""),
		"linked":       s.store.Setting(ctx, store.SetServerURL, "") != "",
		"platform":     s.platform(ctx),
		"platformName": platformName(s.platform(ctx)),
		// The editor shows what "Standard" currently means.
		"defaultTranscode": s.defaultTranscode(ctx),
		"defaultCredits":   s.defaultCredits(ctx),
		"horizon":          s.cfg.Horizon, // hours planned ahead
		"version":          Version,
		"tuner": map[string]string{
			"url": s.baseURL(r),
			"epg": s.baseURL(r) + "/epg.xml",
		},
	})
}

// ---- channels ----

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	out := make([]channelView, 0, len(chans))
	for _, c := range chans {
		out = append(out, s.channelView(ctx, c, true))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) channelFromPath(w http.ResponseWriter, r *http.Request) *store.Channel {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := s.store.GetChannel(r.Context(), id)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "api.channelNotFound")
		return nil
	}
	return c
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	c := s.channelFromPath(w, r)
	if c == nil {
		return
	}
	v := s.channelView(r.Context(), c, true)
	// Manual entries whose titles were deleted in Plex, so the editor can flag them.
	if cfg, err := schedule.ParseConfig(c.Config); err == nil {
		if catalog, err := s.lib.All(r.Context()); err == nil {
			v.Missing = cfg.MissingItems(c.Kind, catalog)
			// Years of entries saved before they were stored, for sorting in the editor.
			cfg.FillYears(c.Kind, catalog)
			if raw, err := json.Marshal(cfg); err == nil {
				cp := *c
				cp.Config = raw
				v.Channel = &cp
			}
		}
	}
	writeJSON(w, http.StatusOK, v)
}

type channelInput struct {
	Number  int             `json:"number"`
	Name    string          `json:"name"`
	Enabled *bool           `json:"enabled"`
	Kind    string          `json:"kind"`
	Color   string          `json:"color"`
	Config  json.RawMessage `json:"config"`
}

func (s *Server) applyChannelInput(ctx context.Context, c *store.Channel, in channelInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return i18n.E("api.nameMissing")
	}
	switch in.Kind {
	case schedule.KindManual, schedule.KindSmart, schedule.KindSeasonal, schedule.KindAI:
	default:
		return i18n.E("api.unknownKind")
	}
	cfg, err := schedule.ParseConfig(in.Config)
	if err != nil {
		return i18n.E("api.badConfig", err)
	}
	if err := cfg.Validate(in.Kind); err != nil {
		return err
	}
	if in.Number <= 0 {
		n, err := s.store.NextChannelNumber(ctx)
		if err != nil {
			return err
		}
		in.Number = n
	}
	if other, err := s.store.GetChannelByNumber(ctx, in.Number); err == nil && other.ID != c.ID {
		return i18n.E("api.numberTaken", in.Number, other.Name)
	}
	raw, _ := json.Marshal(cfg)
	c.Number, c.Name, c.Kind, c.Color, c.Config = in.Number, in.Name, in.Kind, in.Color, raw
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	return nil
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in channelInput
	if err := readJSON(r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	c := &store.Channel{Enabled: true}
	if err := s.applyChannelInput(ctx, c, in); err != nil {
		s.failErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.store.CreateChannel(ctx, c); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.regenerate(c.ID)
	writeJSON(w, http.StatusCreated, s.channelView(ctx, c, false))
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.channelFromPath(w, r)
	if c == nil {
		return
	}
	var in channelInput
	if err := readJSON(r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	before := string(c.Config)
	if err := s.applyChannelInput(ctx, c, in); err != nil {
		s.failErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.store.UpdateChannel(ctx, c); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.guideChanged(false) // name, number, logo or enabled may have changed
	v := s.channelView(ctx, c, false)
	if before != string(c.Config) {
		old, err1 := schedule.ParseConfig(json.RawMessage(before))
		cur, err2 := schedule.ParseConfig(c.Config)
		v.PlanChanged = err1 != nil || err2 != nil || !old.SamePlan(*cur)
	}
	writeJSON(w, http.StatusOK, v)
}

// regenerate rebuilds the future guide in the background (AI channels may take a while).
func (s *Server) regenerate(id int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.sched.Reset(ctx, id); err != nil {
			s.log.Warn("regenerate schedule", "channel", id, "err", err)
		}
	}()
}

func (s *Server) handleRegenerate(w http.ResponseWriter, r *http.Request) {
	if c := s.channelFromPath(w, r); c != nil {
		s.regenerate(c.ID)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleRegenerateAll re-plans every enabled channel one after another in the
// background, so several AI channels don't hit the model at once.
func (s *Server) handleRegenerateAll(w http.ResponseWriter, r *http.Request) {
	chans, err := s.store.ListChannels(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	var ids []int64
	for _, c := range chans {
		if c.Enabled {
			ids = append(ids, c.ID)
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		for _, id := range ids {
			if err := s.sched.Reset(ctx, id); err != nil {
				s.log.Warn("regenerate schedule", "channel", id, "err", err)
			}
		}
	}()
	writeJSON(w, http.StatusOK, map[string]int{"channels": len(ids)})
}

// handleChannelError returns the last planning problem; the editor polls it.
func (s *Server) handleChannelError(w http.ResponseWriter, r *http.Request) {
	if c := s.channelFromPath(w, r); c != nil {
		writeJSON(w, http.StatusOK, map[string]string{"error": i18n.Message(s.reqLang(r), s.sched.LastError(c.ID))})
	}
}

func (s *Server) handleDismissError(w http.ResponseWriter, r *http.Request) {
	if c := s.channelFromPath(w, r); c != nil {
		s.sched.ClearError(c.ID)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	c := s.channelFromPath(w, r)
	if c == nil {
		return
	}
	if c.Logo != "" {
		_ = os.Remove(filepath.Join(s.cfg.DataDir, "logos", c.Logo))
	}
	if err := s.store.DeleteChannel(r.Context(), c.ID); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.guideChanged(false)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUploadLogo(w http.ResponseWriter, r *http.Request) {
	c := s.channelFromPath(w, r)
	if c == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.fileMissing")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.fileUnreadable")
		return
	}
	// Stored as PNG and reduced in size, so Plex and the slates get a light, readable file.
	data, err = logo.NormalizeUpload(data)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.fileType")
		return
	}
	name := fmt.Sprintf("ch%d-%s.png", c.ID, randomHex(4))
	if err := os.WriteFile(filepath.Join(s.cfg.DataDir, "logos", name), data, 0o644); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if c.Logo != "" {
		_ = os.Remove(filepath.Join(s.cfg.DataDir, "logos", c.Logo))
	}
	c.Logo = name
	if err := s.store.UpdateChannel(r.Context(), c); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.guideChanged(false)
	writeJSON(w, http.StatusOK, s.channelView(r.Context(), c, false))
}

func (s *Server) handleDeleteLogo(w http.ResponseWriter, r *http.Request) {
	c := s.channelFromPath(w, r)
	if c == nil {
		return
	}
	if c.Logo != "" {
		_ = os.Remove(filepath.Join(s.cfg.DataDir, "logos", c.Logo))
		c.Logo = ""
		if err := s.store.UpdateChannel(r.Context(), c); err != nil {
			s.failErr(w, r, http.StatusInternalServerError, err)
			return
		}
		s.guideChanged(false)
	}
	writeJSON(w, http.StatusOK, s.channelView(r.Context(), c, false))
}

func (s *Server) handleLogoPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	num := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	data, err := logo.Render(logo.Options{Name: q.Get("name"), Style: q.Get("style"), Color: q.Get("color"), Hint: q.Get("hint"),
		Seed: num("seed"), Hue: num("hue"), Tagline: q.Get("tagline"), Number: num("number"), Holiday: q.Get("holiday"), Symbol: q.Get("symbol")})
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(data)
}

// ---- guide ----

func (s *Server) handleGuide(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	from := time.Now().Add(-30 * time.Minute)
	if ms, err := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64); err == nil {
		from = time.UnixMilli(ms)
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 48 {
		hours = 6
	}
	to := from.Add(time.Duration(hours) * time.Hour)
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		Channel  channelView   `json:"channel"`
		Programs []programView `json:"programs"`
	}
	out := []row{}
	for _, c := range chans {
		if !c.Enabled {
			continue
		}
		rw := row{Channel: s.channelView(ctx, c, false), Programs: []programView{}}
		if s.fillerOnly(c) {
			rw.Programs = noInfoViews(s.lang(ctx), from, to)
			out = append(out, rw)
			continue
		}
		progs := s.mainPrograms(ctx, c.ID, from, to)
		for _, p := range progs {
			if m := s.lib.Get(ctx, p.RatingKey); m != nil {
				rw.Programs = append(rw.Programs, programView{Start: p.Start, Stop: p.Stop, Note: p.Note, Source: p.Source, Media: viewOf(m, true)})
			}
		}
		out = append(out, rw)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.streamer.Sessions())
}

// ---- library ----

func (s *Server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.store.ListLibraries(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	type libView struct {
		store.Library
		Locations []libraryLocation `json:"locations"`
	}
	locs := s.libraryLocations(r.Context())
	out := []libView{}
	for _, l := range libs {
		out = append(out, libView{Library: l, Locations: locs[l.ID]})
	}
	// container: the editor offers to assign the mounted media folders
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out, "status": s.lib.Status(), "container": s.container})
}

func (s *Server) handlePutLibrary(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	if err := s.store.SetLibraryEnabled(r.Context(), r.PathValue("id"), in.Enabled); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.lib.Invalidate()
	// Titles of a (de)activated library appear on / disappear from channels like a sync change.
	keys := s.libraryKeys(r.Context(), r.PathValue("id"))
	changes := library.Changes{Added: map[string]bool{}, Removed: map[string]bool{}}
	if in.Enabled {
		changes.Added = keys
		s.lib.StartSync()
	} else {
		changes.Removed = keys
	}
	go s.applyLibraryChanges(changes)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleFullScan fetches the full metadata of every title of a library again
// instead of only the new and due ones.
func (s *Server) handleFullScan(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ResetDetails(r.Context(), r.PathValue("id")); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	// A sync already running may have passed this library; the next one picks it up.
	started := s.lib.StartSync()
	writeJSON(w, http.StatusOK, map[string]any{"started": started, "status": s.lib.Status()})
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	started := s.lib.StartSync()
	writeJSON(w, http.StatusOK, map[string]any{"started": started, "status": s.lib.Status()})
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.lib.Status())
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	items, err := s.lib.All(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rules.Facets(items, 400))
}

type showView struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Thumb    string `json:"thumb"`
	Episodes int    `json:"episodes"`
	Seasons  []int  `json:"seasons"`
}

// searchPage is the number of movies and of shows per search request; the picker
// loads further pages with offset while scrolling.
const searchPage = 60

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	items, err := s.lib.All(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	typ := r.URL.Query().Get("type")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	offset = max(offset, 0)
	inPage := func(i int) bool { return i >= offset && i < offset+searchPage }

	movies := []mediaView{}
	nMovies := 0
	shows := map[string]*showView{}
	showIndex := map[string]int{} // position of every matching show, also outside the page
	var showOrder []string
	for _, m := range items {
		if m.Type == "movie" && (typ == "" || typ == "movie") {
			if q == "" || strings.Contains(strings.ToLower(m.Title), q) {
				if inPage(nMovies) {
					movies = append(movies, viewOf(m, false))
				}
				nMovies++
			}
		}
		if m.Type == "episode" && (typ == "" || typ == "show") {
			if q == "" || strings.Contains(strings.ToLower(m.ShowTitle), q) {
				idx, seen := showIndex[m.ShowKey]
				if !seen {
					idx = len(showIndex)
					showIndex[m.ShowKey] = idx
				}
				if !inPage(idx) {
					continue
				}
				sv, ok := shows[m.ShowKey]
				if !ok {
					sv = &showView{Key: m.ShowKey, Type: "show", Title: m.ShowTitle, Year: m.Year, Thumb: "/img/" + m.RatingKey + "/show"}
					shows[m.ShowKey] = sv
					showOrder = append(showOrder, m.ShowKey)
				}
				sv.Episodes++
				if !containsInt(sv.Seasons, m.Season) {
					sv.Seasons = append(sv.Seasons, m.Season)
				}
			}
		}
	}
	outShows := make([]*showView, 0, len(showOrder))
	for _, k := range showOrder {
		sort.Ints(shows[k].Seasons)
		outShows = append(outShows, shows[k])
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": movies, "shows": outShows,
		"moreMovies": nMovies > offset+searchPage, "moreShows": len(showIndex) > offset+searchPage})
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	items, err := s.lib.All(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	key := r.PathValue("key")
	eps := []mediaView{}
	var list []*store.Media
	for _, m := range items {
		if m.Type == "episode" && m.ShowKey == key {
			list = append(list, m)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Season != list[j].Season {
			return list[i].Season < list[j].Season
		}
		return list[i].Episode < list[j].Episode
	})
	for _, m := range list {
		v := viewOf(m, false)
		v.Title = m.Title
		v.Subtitle = fmt.Sprintf("S%02dE%02d", m.Season, m.Episode)
		eps = append(eps, v)
	}
	writeJSON(w, http.StatusOK, eps)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source  schedule.Source `json:"source"`
		SortBy  string          `json:"sortBy"`
		Channel *channelInput   `json:"channel"` // the channel being edited, for trailers following its filter
	}
	if err := readJSON(r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	var kind string
	var cfg *schedule.Config
	if in.Channel != nil {
		kind = in.Channel.Kind
		cfg, _ = schedule.ParseConfig(in.Channel.Config)
	}
	items, err := s.sched.Preview(r.Context(), &in.Source, in.SortBy, kind, cfg)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	var total int64
	movies, episodes, trailers := 0, 0, 0
	shows := map[string]bool{}
	for _, m := range items {
		total += m.DurationMs
		switch m.Type {
		case "episode":
			episodes++
			shows[m.ShowKey] = true
		case store.TypeTrailer:
			trailers++
		default:
			movies++
		}
	}
	sample := make([]mediaView, 0, 60)
	seenShow := map[string]bool{}
	for _, m := range items {
		if len(sample) >= 60 {
			break
		}
		if m.Type == "episode" && in.Source.Type != "manual" {
			if seenShow[m.ShowKey] {
				continue
			}
			seenShow[m.ShowKey] = true
		}
		sample = append(sample, viewOf(m, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(items), "movies": movies, "episodes": episodes, "shows": len(shows), "trailers": trailers,
		"durationMs": total, "items": sample,
	})
}

// handlePosterColor derives an accent colour from the posters of a source's titles.
func (s *Server) handlePosterColor(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source schedule.Source `json:"source"`
	}
	if err := readJSON(r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	items, err := s.sched.Preview(r.Context(), &in.Source, "", "", nil)
	if err != nil || len(items) == 0 {
		s.fail(w, r, http.StatusBadRequest, "api.noColorTitles")
		return
	}
	// a spread-out sample of up to 16 posters
	step := max(1, len(items)/16)
	var imgs []image.Image
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := 0; i < len(items) && i/step < 16; i += step {
		m := items[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if img := s.fetchPoster(r.Context(), m); img != nil {
				mu.Lock()
				imgs = append(imgs, img)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	color, ok := logo.DominantColor(imgs)
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "api.colorless")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"color": color})
}

func (s *Server) handleSeasonalPreset(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, schedule.SeasonalPreset(s.reqLang(r)))
}

// ---- settings ----

const maskedKey = "••••••••"

// masked hides a stored secret ("" stays "").
func masked(v string) string {
	if v == "" {
		return ""
	}
	return maskedKey
}

// validPrefix keeps XMLTV ids safe for every consumer (no spaces, quotes or markup).
func validPrefix(p string) bool {
	if len(p) > 20 {
		return false
	}
	for _, r := range p {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := ""
	if s.store.Setting(ctx, store.SetAIKey, "") != "" {
		key = maskedKey
	}
	gpuOK, gpuStatus := s.vaapi.Available(ctx)
	_, decodeStatus := s.vaapi.Decode(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"language":         s.lang(ctx),
		"friendlyName":     s.friendlyName(ctx),
		"epgPrefix":        s.store.Setting(ctx, store.SetEPGPrefix, ""),
		"tunerCount":       s.store.SettingInt(ctx, store.SetTunerCount, 4),
		"normalizeRes":     s.store.SettingInt(ctx, store.SetNormalizeRes, 720),
		"audioLanguages":   s.store.Setting(ctx, store.SetAudioLanguages, "deu,ger"),
		"syncInterval":     s.store.SettingInt(ctx, store.SetSyncInterval, 6),
		"streamBuffer":     s.store.SettingInt(ctx, store.SetStreamBuffer, int(stream.DefaultBuffer/time.Second)),
		"hwAccel":          s.store.Setting(ctx, store.SetHWAccel, "auto"),
		"defaultTranscode": s.defaultTranscode(ctx),
		"hwAvailable":      gpuOK,
		"hwStatus":         gpuStatus.In(s.reqLang(r)),
		"hwDecode":         s.store.Setting(ctx, store.SetHWDecode, "auto"),
		"hwDecodeStatus":   decodeStatus.In(s.reqLang(r)),
		"trailerHubs":      s.trailerHubs(ctx),
		"trailerCaption":   s.store.Setting(ctx, store.SetTrailerCaption, "0") == "1",
		"cardMusic":        s.store.Setting(ctx, store.SetCardMusic, "1") == "1",
		"credits":          s.defaultCredits(ctx),
		"allowSharedUsers": s.store.Setting(ctx, store.SetAllowSharedUser, "1") == "1",
		"aiProvider":       s.store.Setting(ctx, store.SetAIProvider, ""),
		"aiModel":          s.store.Setting(ctx, store.SetAIModel, ""),
		"aiBaseUrl":        s.store.Setting(ctx, store.SetAIBaseURL, ""),
		"aiKey":            key,
		"tmdbKey":          masked(s.store.Setting(ctx, store.SetTMDbKey, "")),
		"tmdbRegion":       s.store.Setting(ctx, store.SetTMDbRegion, ""),
		"tmdbRegionAuto":   s.tmdbRegion(ctx),
		"slateSeconds":     int(s.slateLength(ctx) / time.Second),
		"weatherPlace":     s.weatherPlace(ctx),
		"newsFeeds":        strings.Join(s.newsFeeds(ctx), "\n"),
		"aiDefaultModel":   ai.DefaultAnthropicModel,
		"serverName":       s.store.Setting(ctx, store.SetServerName, ""),
		"serverUrl":        s.store.Setting(ctx, store.SetServerURL, ""),
		"plexUrlOverride":  s.cfg.PlexURL,
		"guideReload":      s.guideReloadStatus(s.reqLang(r)),
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Language         *string         `json:"language"`
		FriendlyName     *string         `json:"friendlyName"`
		EPGPrefix        *string         `json:"epgPrefix"`
		TunerCount       *int            `json:"tunerCount"`
		NormalizeRes     *int            `json:"normalizeRes"`
		AudioLanguages   *string         `json:"audioLanguages"`
		SyncInterval     *int            `json:"syncInterval"`
		StreamBuffer     *int            `json:"streamBuffer"`
		HWAccel          *string         `json:"hwAccel"`
		HWDecode         *string         `json:"hwDecode"`
		DefaultTranscode *string         `json:"defaultTranscode"`
		TrailerHubs      *[]string       `json:"trailerHubs"`
		TrailerCaption   *bool           `json:"trailerCaption"`
		CardMusic        *bool           `json:"cardMusic"`
		Credits          *string         `json:"credits"`
		AllowSharedUsers *bool           `json:"allowSharedUsers"`
		AIProvider       *string         `json:"aiProvider"`
		AIModel          *string         `json:"aiModel"`
		AIBaseURL        *string         `json:"aiBaseUrl"`
		AIKey            *string         `json:"aiKey"`
		TMDbKey          *string         `json:"tmdbKey"`
		TMDbRegion       *string         `json:"tmdbRegion"`
		SlateSeconds     *int            `json:"slateSeconds"`
		WeatherPlace     json.RawMessage `json:"weatherPlace"` // null removes the place (a pointer would read null as absent)
		NewsFeeds        *string         `json:"newsFeeds"`
		ServerURL        *string         `json:"serverUrl"`
	}
	if err := readJSON(r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	v := map[string]string{}
	if in.Language != nil {
		if !i18n.Known(*in.Language) {
			s.fail(w, r, http.StatusBadRequest, "api.language")
			return
		}
		v[store.SetLanguage] = *in.Language
	}
	if in.EPGPrefix != nil {
		p := strings.TrimSpace(*in.EPGPrefix)
		if !validPrefix(p) {
			s.fail(w, r, http.StatusBadRequest, "api.prefix")
			return
		}
		v[store.SetEPGPrefix] = p
	}
	if in.FriendlyName != nil {
		v[store.SetFriendlyName] = strings.TrimSpace(*in.FriendlyName)
	}
	if in.TunerCount != nil {
		v[store.SetTunerCount] = strconv.Itoa(min(max(*in.TunerCount, 1), 16))
	}
	if in.NormalizeRes != nil {
		res := 720
		if *in.NormalizeRes == 1080 {
			res = 1080
		}
		v[store.SetNormalizeRes] = strconv.Itoa(res)
	}
	if in.AudioLanguages != nil {
		v[store.SetAudioLanguages] = strings.ReplaceAll(*in.AudioLanguages, " ", "")
	}
	if in.SyncInterval != nil {
		v[store.SetSyncInterval] = strconv.Itoa(max(*in.SyncInterval, 0))
	}
	hubsChanged := false
	if in.TrailerHubs != nil {
		var hubs []string
		for _, h := range *in.TrailerHubs {
			if h = strings.TrimSpace(h); h != "" && !strings.Contains(h, ",") {
				hubs = append(hubs, h)
			}
		}
		hubsChanged = strings.Join(hubs, ",") != strings.Join(s.trailerHubs(r.Context()), ",")
		v[store.SetTrailerHubs] = strings.Join(hubs, ",")
	}
	if in.StreamBuffer != nil {
		v[store.SetStreamBuffer] = strconv.Itoa(min(max(*in.StreamBuffer, 2), 30))
	}
	if in.HWAccel != nil {
		v[store.SetHWAccel] = map[bool]string{true: "off", false: "auto"}[*in.HWAccel == "off"]
	}
	if in.HWDecode != nil {
		v[store.SetHWDecode] = map[bool]string{true: "off", false: "auto"}[*in.HWDecode == "off"]
	}
	if in.DefaultTranscode != nil {
		switch *in.DefaultTranscode {
		case "copy", "audio", "full":
			v[store.SetTranscode] = *in.DefaultTranscode
		default:
			s.fail(w, r, http.StatusBadRequest, "api.transcodeMode")
			return
		}
	}
	if in.TrailerCaption != nil {
		v[store.SetTrailerCaption] = map[bool]string{true: "1", false: "0"}[*in.TrailerCaption]
	}
	if in.CardMusic != nil {
		v[store.SetCardMusic] = map[bool]string{true: "1", false: "0"}[*in.CardMusic]
	}
	if in.Credits != nil {
		switch *in.Credits {
		case "off", "squeeze":
			v[store.SetCredits] = *in.Credits
		default:
			s.fail(w, r, http.StatusBadRequest, "api.creditsMode")
			return
		}
	}
	if in.AllowSharedUsers != nil {
		v[store.SetAllowSharedUser] = map[bool]string{true: "1", false: "0"}[*in.AllowSharedUsers]
	}
	if in.AIProvider != nil {
		switch *in.AIProvider {
		case "", "anthropic", "openai":
			v[store.SetAIProvider] = *in.AIProvider
		default:
			s.fail(w, r, http.StatusBadRequest, "api.unknownAI")
			return
		}
	}
	if in.AIModel != nil {
		v[store.SetAIModel] = strings.TrimSpace(*in.AIModel)
	}
	if in.AIBaseURL != nil {
		v[store.SetAIBaseURL] = strings.TrimSpace(*in.AIBaseURL)
	}
	tmdbChanged := false
	if in.TMDbKey != nil && *in.TMDbKey != maskedKey {
		v[store.SetTMDbKey] = strings.TrimSpace(*in.TMDbKey)
		tmdbChanged = v[store.SetTMDbKey] != ""
	}
	if in.TMDbRegion != nil {
		v[store.SetTMDbRegion] = strings.ToUpper(strings.TrimSpace(*in.TMDbRegion))
	}
	infoChanged := map[string]bool{}
	if in.WeatherPlace != nil {
		var p *weather.Place
		if err := json.Unmarshal(in.WeatherPlace, &p); err != nil || (p != nil && (p.Name == "" || math.Abs(p.Lat) > 90 || math.Abs(p.Lon) > 180)) {
			s.fail(w, r, http.StatusBadRequest, "api.badRequest")
			return
		}
		val := ""
		if p != nil {
			b, _ := json.Marshal(p)
			val = string(b)
		}
		if val != s.store.Setting(r.Context(), store.SetWeatherPlace, "") {
			v[store.SetWeatherPlace] = val
			infoChanged["weather"] = true
		}
	}
	if in.NewsFeeds != nil {
		val := strings.Join(splitFeeds(*in.NewsFeeds), "\n")
		if val != s.store.Setting(r.Context(), store.SetNewsFeeds, "") {
			v[store.SetNewsFeeds] = val
			infoChanged["news"] = true
		}
	}
	if in.SlateSeconds != nil {
		v[store.SetSlateSeconds] = strconv.Itoa(min(max(*in.SlateSeconds, 8), 60))
	}
	if in.AIKey != nil && *in.AIKey != maskedKey {
		v[store.SetAIKey] = strings.TrimSpace(*in.AIKey)
	}
	if in.ServerURL != nil && *in.ServerURL != "" && strings.TrimRight(*in.ServerURL, "/") != s.store.Setting(r.Context(), store.SetServerURL, "") {
		url := strings.TrimRight(*in.ServerURL, "/")
		var id string
		var err error
		if s.platform(r.Context()) == "plex" {
			var srv *plex.Server
			if srv, err = s.plexServer(r.Context()); err == nil {
				srv.BaseURL = url
				id, err = srv.Identity(r.Context())
			}
		} else {
			var c *jellyfin.Client
			if c, err = s.jellyfinClient(r.Context()); err == nil {
				c.BaseURL = url
				id, err = c.Identity(r.Context())
			}
		}
		if err != nil || id != s.store.Setting(r.Context(), store.SetServerID, "") {
			s.fail(w, r, http.StatusBadRequest, "api.serverUnreachable")
			return
		}
		v[store.SetServerURL] = url
	}
	sharedBefore := s.store.Setting(r.Context(), store.SetAllowSharedUser, "1")
	if err := s.store.SetSettings(r.Context(), v); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if v[store.SetAllowSharedUser] == "0" && sharedBefore != "0" {
		// Only the owner may sign in now: end everybody else's sessions.
		if err := s.store.DeleteOtherSessions(r.Context(), s.ownerUserID(r.Context())); err != nil {
			s.log.Warn("end sessions", "err", err)
		}
	}
	if tmdbChanged && s.platform(r.Context()) != "plex" {
		s.startSlates()
	}
	if len(infoChanged) > 0 {
		s.infoSettingsChanged(infoChanged)
	}
	if hubsChanged && len(s.trailerHubs(r.Context())) > 0 {
		s.startDiscover()
	}
	if _, ok := v[store.SetEPGPrefix]; ok {
		s.guideChanged(false) // the channel ids in the XMLTV guide
	}
	s.handleGetSettings(w, r)
}

func (s *Server) handleAITest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	p, err := s.aiProvider(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusBadRequest, err)
		return
	}
	items, _ := s.lib.All(ctx)
	var cands []ai.Candidate
	for _, m := range items {
		if m.Type == "movie" {
			cands = append(cands, ai.Candidate{ID: m.RatingKey, Kind: "movie", Title: m.Title, Year: m.Year, Genres: m.Tags.Genre})
		}
		if len(cands) >= 40 {
			break
		}
	}
	if len(cands) == 0 {
		s.fail(w, r, http.StatusBadRequest, "ai.noMovies")
		return
	}
	picks, err := p.Pick(ctx, ai.Request{Theme: i18n.T(s.reqLang(r), "ai.testTheme"), Count: 3, Candidates: cands, Lang: s.reqLang(r)})
	if err != nil {
		s.failErr(w, r, http.StatusBadGateway, err)
		return
	}
	var out []map[string]string
	for _, pk := range picks {
		title := pk.ID
		if m := s.lib.Get(ctx, pk.ID); m != nil {
			title = m.Title
		}
		out = append(out, map[string]string{"title": title, "reason": pk.Reason})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- users ----

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	owner := s.store.Setting(r.Context(), store.SetOwnerID, "")
	type uv struct {
		store.User
		Owner bool `json:"owner"`
	}
	out := []uv{}
	for _, u := range users {
		out = append(out, uv{u, u.ExternalID == owner && u.Provider == s.platform(r.Context())})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil || (in.Role != "admin" && in.Role != "viewer") {
		s.fail(w, r, http.StatusBadRequest, "api.badRole")
		return
	}
	if id == userFrom(r).ID {
		s.fail(w, r, http.StatusBadRequest, "api.ownRole")
		return
	}
	if s.isOwner(r.Context(), id) {
		s.fail(w, r, http.StatusBadRequest, "api.ownerAdmin")
		return
	}
	if err := s.store.SetUserRole(r.Context(), id, in.Role); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// isOwner reports whether the user with this id is the owner of the instance.
func (s *Server) isOwner(ctx context.Context, id int64) bool {
	owner := s.ownerUserID(ctx)
	return owner != 0 && owner == id
}

// ownerUserID returns the user id of the owner (0 if unknown).
func (s *Server) ownerUserID(ctx context.Context) int64 {
	users, _ := s.store.ListUsers(ctx)
	owner := s.store.Setting(ctx, store.SetOwnerID, "")
	for _, u := range users {
		if u.ExternalID == owner && u.Provider == s.platform(ctx) {
			return u.ID
		}
	}
	return 0
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id == userFrom(r).ID {
		s.fail(w, r, http.StatusBadRequest, "api.removeSelf")
		return
	}
	if s.isOwner(r.Context(), id) {
		s.fail(w, r, http.StatusBadRequest, "api.removeOwner")
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
