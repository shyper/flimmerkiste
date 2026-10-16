package server

// Export and import of channels: the configuration travels as JSON, with a
// description of every library title it refers to (title, year, external
// ids), so another installation – even with another media server – finds the
// same titles in its own library.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/rules"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
)

const (
	exportFormat = "flimmerkiste-channels"
	// oldExportFormat: exports of the project under its earlier name
	oldExportFormat = "pseudotv-channels"
	exportVersion   = 1
)

// exportFile is the file a channel export is written to.
type exportFile struct {
	Format     string          `json:"format"`
	Version    int             `json:"version"`
	ExportedAt int64           `json:"exportedAt"`
	App        string          `json:"app"`
	Language   string          `json:"language"`
	Platform   string          `json:"platform"`
	Channels   []exportChannel `json:"channels"`
}

type exportChannel struct {
	Number  int             `json:"number"`
	Name    string          `json:"name"`
	Enabled bool            `json:"enabled"`
	Kind    string          `json:"kind"`
	Color   string          `json:"color,omitempty"`
	Config  json.RawMessage `json:"config"`
	// Logo is an uploaded logo as base64 PNG ("" = automatic logo).
	Logo string `json:"logo,omitempty"`
	// Titles describes the library titles of manual entries by their key
	// ("type|key|season").
	Titles map[string]exportTitle `json:"titles,omitempty"`
	// Libraries describes the libraries filter rules name, by id.
	Libraries map[string]exportLibrary `json:"libraries,omitempty"`
}

type exportTitle struct {
	Type      string            `json:"type"` // movie | show | season | episode
	Title     string            `json:"title"`
	Year      int               `json:"year,omitempty"`
	ShowTitle string            `json:"showTitle,omitempty"`
	Season    int               `json:"season,omitempty"`
	Episode   int               `json:"episode,omitempty"`
	IDs       map[string]string `json:"ids,omitempty"` // tmdb, imdb, tvdb of the movie, show or episode
	ShowIDs   map[string]string `json:"showIds,omitempty"`
}

type exportLibrary struct {
	Title string `json:"title"`
	Type  string `json:"type"`
}

// missingLibrary marks a library of an imported filter rule that does not exist
// here; the editor asks to choose one instead.
const missingLibrary = "missing:"

// maxImport is the largest export file accepted (logos travel inside it).
const maxImport = 32 << 20

func titleKey(it schedule.Item) string { return fmt.Sprintf("%s|%s|%d", it.Type, it.Key, it.Season) }

// showExt returns the external ids of a show, taken from one of its episodes.
func showExt(m *store.Media) map[string]string {
	out := map[string]string{}
	for k, v := range m.Ext {
		if p, ok := strings.CutPrefix(k, "show."); ok {
			out[p] = v
		}
	}
	return out
}

func ownExt(m *store.Media) map[string]string {
	out := map[string]string{}
	for k, v := range m.Ext {
		if !strings.Contains(k, ".") {
			out[k] = v
		}
	}
	return out
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// walkRules calls fn for every rule of a group and its subgroups.
func walkRules(g *rules.Group, fn func(*rules.Rule)) {
	if g == nil {
		return
	}
	for i := range g.Rules {
		fn(&g.Rules[i])
	}
	for i := range g.Groups {
		walkRules(&g.Groups[i], fn)
	}
}

// sourceRules lists the filters of a source.
func sourceRules(src *schedule.Source) []*rules.Group {
	out := []*rules.Group{src.Rules}
	if src.AI != nil {
		out = append(out, src.AI.Filter)
	}
	return out
}

// exportChannels describes the channels for another installation.
func (s *Server) exportChannels(ctx context.Context, chans []*store.Channel) (*exportFile, error) {
	catalog, err := s.lib.All(ctx)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*store.Media{}
	showEp := map[string]*store.Media{} // an episode per show
	for _, m := range catalog {
		byKey[m.RatingKey] = m
		if m.Type == "episode" && showEp[m.ShowKey] == nil {
			showEp[m.ShowKey] = m
		}
	}
	libs, err := s.store.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	libByID := map[string]store.Library{}
	for _, l := range libs {
		libByID[l.ID] = l
	}
	f := &exportFile{Format: exportFormat, Version: exportVersion, ExportedAt: s.now().Unix(), App: Version,
		Language: string(s.lang(ctx)), Platform: "plex"}
	for _, c := range chans {
		cfg, err := schedule.ParseConfig(c.Config)
		if err != nil {
			return nil, err
		}
		ec := exportChannel{Number: c.Number, Name: c.Name, Enabled: c.Enabled, Kind: c.Kind, Color: c.Color, Config: c.Config,
			Titles: map[string]exportTitle{}, Libraries: map[string]exportLibrary{}}
		for _, src := range cfg.Sources(c.Kind) {
			for _, it := range src.Items {
				t := exportTitle{Type: it.Type, Title: it.Title, Year: it.Year, Season: it.Season}
				switch it.Type {
				case "show", "season":
					if ep := showEp[it.Key]; ep != nil {
						t.Title, t.ShowTitle, t.ShowIDs = ep.ShowTitle, ep.ShowTitle, nilIfEmpty(showExt(ep))
					}
				case "episode":
					if m := byKey[it.Key]; m != nil {
						t.Title, t.ShowTitle, t.Season, t.Episode = m.Title, m.ShowTitle, m.Season, m.Episode
						t.IDs, t.ShowIDs = nilIfEmpty(ownExt(m)), nilIfEmpty(showExt(m))
					}
				default:
					if m := byKey[it.Key]; m != nil {
						t.Title, t.Year, t.IDs = m.Title, m.Year, nilIfEmpty(ownExt(m))
					}
				}
				ec.Titles[titleKey(it)] = t
			}
			for _, g := range sourceRules(src) {
				walkRules(g, func(r *rules.Rule) {
					if r.Field != "library" {
						return
					}
					for _, id := range r.Values {
						if l, ok := libByID[id]; ok {
							ec.Libraries[id] = exportLibrary{Title: l.Title, Type: l.Type}
						}
					}
				})
			}
		}
		if c.Logo != "" {
			if data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "logos", c.Logo)); err == nil {
				ec.Logo = base64.StdEncoding.EncodeToString(data)
			}
		}
		f.Channels = append(f.Channels, ec)
	}
	return f, nil
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := s.store.ListChannels(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	var chans []*store.Channel
	want := map[int64]bool{}
	for _, v := range strings.Split(r.URL.Query().Get("channels"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			want[id] = true
		}
	}
	for _, c := range all {
		if len(want) == 0 || want[c.ID] {
			chans = append(chans, c)
		}
	}
	f, err := s.exportChannels(ctx, chans)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	name := "flimmerkiste-channels-" + s.now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	writeJSON(w, http.StatusOK, f)
}

// importMatcher finds exported titles and libraries in this library.
type importMatcher struct {
	byExt      map[string]*store.Media // "tmdb:603" -> movie or episode
	showByExt  map[string]string       // "tmdb:1399" -> show key
	movies     map[string]*store.Media // "title|year" (lower case) -> movie
	moviesT    map[string]*store.Media // "title" -> movie (when the year differs)
	shows      map[string]string       // "title" -> show key
	episodes   map[string]*store.Media // "showkey|season|episode" -> episode
	seasons    map[string]bool         // "showkey|season"
	libsByName map[string]string       // "title|type" -> library id
}

func (s *Server) newImportMatcher(ctx context.Context) (*importMatcher, error) {
	catalog, err := s.lib.All(ctx)
	if err != nil {
		return nil, err
	}
	m := &importMatcher{byExt: map[string]*store.Media{}, showByExt: map[string]string{}, movies: map[string]*store.Media{},
		moviesT: map[string]*store.Media{}, shows: map[string]string{}, episodes: map[string]*store.Media{}, seasons: map[string]bool{},
		libsByName: map[string]string{}}
	for _, x := range catalog {
		for k, v := range x.Ext {
			if p, ok := strings.CutPrefix(k, "show."); ok {
				m.showByExt[p+":"+v] = x.ShowKey
			} else {
				m.byExt[k+":"+v] = x
			}
		}
		switch x.Type {
		case "movie":
			m.movies[fmt.Sprintf("%s|%d", strings.ToLower(x.Title), x.Year)] = x
			m.moviesT[strings.ToLower(x.Title)] = x
		case "episode":
			m.shows[strings.ToLower(x.ShowTitle)] = x.ShowKey
			m.episodes[fmt.Sprintf("%s|%d|%d", x.ShowKey, x.Season, x.Episode)] = x
			m.seasons[fmt.Sprintf("%s|%d", x.ShowKey, x.Season)] = true
		}
	}
	libs, err := s.store.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range libs {
		m.libsByName[strings.ToLower(l.Title)+"|"+l.Type] = l.ID
	}
	return m, nil
}

// ext looks a title up by its external ids (tmdb first).
func extLookup[T any](ids map[string]string, table map[string]T) (T, bool) {
	for _, p := range []string{"tmdb", "imdb", "tvdb"} {
		if v := ids[p]; v != "" {
			if x, ok := table[p+":"+v]; ok {
				return x, true
			}
		}
	}
	var zero T
	return zero, false
}

func (m *importMatcher) show(t exportTitle) (string, bool) {
	if k, ok := extLookup(t.ShowIDs, m.showByExt); ok {
		return k, true
	}
	name := t.ShowTitle
	if name == "" {
		name = t.Title
	}
	k, ok := m.shows[strings.ToLower(name)]
	return k, ok
}

// item finds the local key of an exported entry.
func (m *importMatcher) item(it schedule.Item, t exportTitle) (string, bool) {
	switch it.Type {
	case "show":
		return m.show(t)
	case "season":
		k, ok := m.show(t)
		return k, ok && m.seasons[fmt.Sprintf("%s|%d", k, it.Season)]
	case "episode":
		if x, ok := extLookup(t.IDs, m.byExt); ok && x.Type == "episode" {
			return x.RatingKey, true
		}
		if k, ok := m.show(t); ok {
			if x := m.episodes[fmt.Sprintf("%s|%d|%d", k, t.Season, t.Episode)]; x != nil {
				return x.RatingKey, true
			}
		}
		return "", false
	}
	if x, ok := extLookup(t.IDs, m.byExt); ok && x.Type == "movie" {
		return x.RatingKey, true
	}
	if x := m.movies[fmt.Sprintf("%s|%d", strings.ToLower(t.Title), t.Year)]; x != nil {
		return x.RatingKey, true
	}
	if x := m.moviesT[strings.ToLower(t.Title)]; x != nil && t.Year == 0 {
		return x.RatingKey, true
	}
	return "", false
}

// adapt maps the library references of an exported channel to this library.
// It returns the configuration and what could not be found.
func (m *importMatcher) adapt(ec exportChannel) (json.RawMessage, []string, error) {
	cfg, err := schedule.ParseConfig(ec.Config)
	if err != nil {
		return nil, nil, err
	}
	var missing []string
	for _, src := range cfg.Sources(ec.Kind) {
		kept := src.Items[:0]
		for _, it := range src.Items {
			t, ok := ec.Titles[titleKey(it)]
			if !ok {
				t = exportTitle{Type: it.Type, Title: it.Title, Year: it.Year, Season: it.Season}
			}
			if key, found := m.item(it, t); found {
				it.Key = key
				kept = append(kept, it)
			} else {
				missing = append(missing, it.Title)
			}
		}
		if len(src.Items) > 0 {
			src.Items = kept
		}
		for _, g := range sourceRules(src) {
			walkRules(g, func(r *rules.Rule) {
				if r.Field != "library" {
					return
				}
				vals := r.Values[:0]
				for _, id := range r.Values {
					l, ok := ec.Libraries[id]
					if ok {
						if local, ok := m.libsByName[strings.ToLower(l.Title)+"|"+l.Type]; ok {
							vals = append(vals, local)
							continue
						}
					} else {
						l.Title = strings.TrimPrefix(id, missingLibrary)
					}
					// Kept as a value no library has: dropped, the rule would no
					// longer count and the channel would take every library.
					vals = append(vals, missingLibrary+l.Title)
					missing = append(missing, l.Title)
				}
				r.Values = vals
			})
		}
	}
	raw, err := json.Marshal(cfg)
	return raw, missing, err
}

// knownFormat accepts exports of this and of the earlier name.
func knownFormat(f string) bool { return f == exportFormat || f == oldExportFormat }

// readExport reads and checks an export file.
func readExport(r *http.Request) (*exportFile, error) {
	var f exportFile
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxImport)).Decode(&f); err != nil {
		return nil, i18n.E("transfer.badFile")
	}
	if !knownFormat(f.Format) {
		return nil, i18n.E("transfer.badFile")
	}
	if f.Version > exportVersion {
		return nil, i18n.E("transfer.tooNew")
	}
	return &f, nil
}

type importPreview struct {
	Index    int      `json:"index"`
	Name     string   `json:"name"`
	Number   int      `json:"number"`
	Kind     string   `json:"kind"`
	Conflict string   `json:"conflict,omitempty"` // name of the channel with the same number
	Missing  []string `json:"missing"`            // titles and libraries not found here
	Error    string   `json:"error,omitempty"`    // the channel cannot be imported
}

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := readExport(r)
	if err != nil {
		s.failErr(w, r, http.StatusBadRequest, err)
		return
	}
	m, err := s.newImportMatcher(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	out := []importPreview{}
	for i, ec := range f.Channels {
		p := importPreview{Index: i, Name: ec.Name, Number: ec.Number, Kind: ec.Kind, Missing: []string{}}
		if c, err := s.store.GetChannelByNumber(ctx, ec.Number); err == nil {
			p.Conflict = c.Name
		}
		if _, missing, err := m.adapt(ec); err != nil {
			p.Error = i18n.Message(s.reqLang(r), i18n.E("api.badConfig", err))
		} else if missing != nil {
			p.Missing = missing
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out, "platform": f.Platform, "exportedAt": f.ExportedAt})
}

// importChoice says what happens to a channel of the file: "skip", "new"
// (keep its number or take the next free one) or "replace" (the channel with
// its number takes over the imported configuration).
type importChoice struct {
	Index  int    `json:"index"`
	Action string `json:"action"`
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		File    json.RawMessage `json:"file"`
		Choices []importChoice  `json:"choices"`
	}
	// the file comes along, so the limit is the file's plus room for the choices
	if err := readJSONLimit(r, &in, maxImport+(1<<20)); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	var f exportFile
	if err := json.Unmarshal(in.File, &f); err != nil || !knownFormat(f.Format) {
		s.fail(w, r, http.StatusBadRequest, "transfer.badFile")
		return
	}
	if f.Version > exportVersion {
		s.fail(w, r, http.StatusBadRequest, "transfer.tooNew")
		return
	}
	m, err := s.newImportMatcher(ctx)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	res := struct {
		Created  int      `json:"created"`
		Replaced int      `json:"replaced"`
		Skipped  int      `json:"skipped"`
		Missing  []string `json:"missing"`
		Errors   []string `json:"errors"`
	}{Missing: []string{}, Errors: []string{}}
	choices := map[int]string{}
	for _, c := range in.Choices {
		choices[c.Index] = c.Action
	}
	// replaced channels first, so their numbers are free for nothing else
	order := make([]int, len(f.Channels))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return choices[order[a]] == "replace" && choices[order[b]] != "replace" })
	for _, i := range order {
		ec := f.Channels[i]
		action := choices[i]
		if action != "new" && action != "replace" {
			res.Skipped++
			continue
		}
		raw, missing, err := m.adapt(ec)
		if err == nil {
			err = s.importChannel(ctx, ec, raw, action == "replace")
		}
		if err != nil {
			res.Errors = append(res.Errors, ec.Name+": "+i18n.Message(s.reqLang(r), err))
			continue
		}
		res.Missing = append(res.Missing, missing...)
		if action == "replace" {
			res.Replaced++
		} else {
			res.Created++
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// importChannel creates a channel from the file, or with replace updates the
// channel that has its number.
func (s *Server) importChannel(ctx context.Context, ec exportChannel, cfg json.RawMessage, replace bool) error {
	c := &store.Channel{Enabled: true}
	number := ec.Number
	if existing, err := s.store.GetChannelByNumber(ctx, ec.Number); err == nil {
		if replace {
			c = existing
		} else {
			number = 0 // the next free one
		}
	}
	enabled := ec.Enabled
	if err := s.applyChannelInput(ctx, c, channelInput{Number: number, Name: ec.Name, Enabled: &enabled, Kind: ec.Kind, Color: ec.Color, Config: cfg}); err != nil {
		return err
	}
	var err error
	if c.ID != 0 {
		err = s.store.UpdateChannel(ctx, c)
	} else {
		err = s.store.CreateChannel(ctx, c)
	}
	if err != nil {
		return err
	}
	if ec.Logo != "" {
		if data, err := base64.StdEncoding.DecodeString(ec.Logo); err == nil {
			if data, err = logo.NormalizeUpload(data); err == nil {
				name := fmt.Sprintf("ch%d-%s.png", c.ID, randomHex(4))
				if err := os.WriteFile(filepath.Join(s.cfg.DataDir, "logos", name), data, 0o644); err == nil {
					if c.Logo != "" {
						_ = os.Remove(filepath.Join(s.cfg.DataDir, "logos", c.Logo))
					}
					c.Logo = name
					if err := s.store.UpdateChannel(ctx, c); err != nil {
						return err
					}
				}
			}
		}
	} else if replace && c.Logo != "" {
		_ = os.Remove(filepath.Join(s.cfg.DataDir, "logos", c.Logo))
		c.Logo = ""
		if err := s.store.UpdateChannel(ctx, c); err != nil {
			return err
		}
	}
	s.regenerate(c.ID)
	return nil
}
