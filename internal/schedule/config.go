// Package schedule turns channel definitions into a materialised program
// guide. The guide is generated a few days ahead and stored, so "what is on
// at 12:30" is a single indexed lookup and nothing runs while nobody watches.
package schedule

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/rules"
	"github.com/shyper/flimmerkiste/internal/store"
)

// Channel kinds, mostly for the UI.
const (
	KindManual   = "manual"
	KindSmart    = "smart"
	KindSeasonal = "seasonal"
	KindAI       = "ai"
)

type Config struct {
	Source   Source   `json:"source"`
	Seasons  []Season `json:"seasons,omitempty"`
	Fallback *Source  `json:"fallback,omitempty"`

	// Order: "fixed" plays items in list/sort order, "shuffle" plays everything
	// once in random order before repeating.
	Order string `json:"order"`
	// SortBy applies to rule based sources in fixed order: title | year | added | release.
	SortBy string `json:"sortBy,omitempty"`
	// KeepEpisodeOrder: in shuffle mode, series still air episode after episode.
	KeepEpisodeOrder bool `json:"keepEpisodeOrder"`
	// NoRepeatHours: a title is not repeated within this window (shuffle/AI only).
	NoRepeatHours int `json:"noRepeatHours"`
	// Transcode: default (global setting) | copy (remux, cheapest) | audio (video copy,
	// audio to AAC) | full (H.264/AAC)
	Transcode string `json:"transcode"`
	LogoStyle string `json:"logoStyle,omitempty"`
	// Logo extras for automatic logos.
	LogoSeed    int    `json:"logoSeed,omitempty"`    // variant of the decoration layout
	LogoHue     int    `json:"logoHue,omitempty"`     // colour theme (hue rotation in degrees)
	LogoTagline string `json:"logoTagline,omitempty"` // small second line
	LogoNumber  bool   `json:"logoNumber,omitempty"`  // show the channel number as a badge
	LogoHoliday bool   `json:"logoHoliday,omitempty"` // seasonal accessories (hat, pumpkin …), off by default

	// Align: main titles start on the next multiple of this many minutes (0 = right away),
	// e.g. 15 -> 20:00, 20:15, 20:30 … The gap is filled with filler clips and an "up next" slate.
	Align int `json:"align,omitempty"`
	// Filler: optional pool of short clips (trailers, music videos …) used between titles.
	Filler *Source `json:"filler,omitempty"`
	// FillerBetween: number of filler clips after every title (independent of Align).
	FillerBetween int `json:"fillerBetween,omitempty"`
	// Previews: breaks (Align) show the trailer of a title airing later on this
	// channel, followed by a slate "Today at 10:00 PM".
	Previews bool `json:"previews,omitempty"`
	// Watermark: channel logo burned into the picture (only with Transcode "full").
	Watermark Watermark `json:"watermark"`
	// Credits: default (global setting) | off | squeeze (the end credits shrink
	// to a corner next to an info panel; only with Transcode "full").
	Credits string `json:"credits"`
}

// SamePlan reports whether two configurations produce the same guide: only
// what is aired and when counts, not logo, technique or watermark.
func (c Config) SamePlan(o Config) bool {
	plan := func(c Config) string {
		raw, _ := json.Marshal(struct {
			Source           Source
			Seasons          []Season
			Fallback         *Source
			Order, SortBy    string
			KeepEpisodeOrder bool
			NoRepeatHours    int
			Align            int
			Filler           *Source
			FillerBetween    int
			Previews         bool
		}{c.Source, c.Seasons, c.Fallback, c.Order, c.SortBy, c.KeepEpisodeOrder, c.NoRepeatHours, c.Align, c.Filler, c.FillerBetween, c.Previews})
		return string(raw)
	}
	return plan(c) == plan(o)
}

type Watermark struct {
	Enabled  bool   `json:"enabled"`
	Position string `json:"position,omitempty"` // tl | tr | bl | br
	Opacity  int    `json:"opacity,omitempty"`  // percent
	Size     int    `json:"size,omitempty"`     // logo width in percent of the picture width
	Tone     string `json:"tone,omitempty"`     // color | gray | white
}

type Source struct {
	Type  string       `json:"type"` // manual | rules | ai | trailers (filler only)
	Items []Item       `json:"items,omitempty"`
	Rules *rules.Group `json:"rules,omitempty"` // for trailers: filter on the advertised titles
	AI    *AIConfig    `json:"ai,omitempty"`
	// TrailerOrigin selects trailers of library movies, of titles from the Plex
	// Discover lists, or both: library | discover | all.
	TrailerOrigin string `json:"trailerOrigin,omitempty"`
	// ChannelFilter: trailers also have to match the channel's own filter
	// (seasonal channels: the season airing at that time).
	ChannelFilter bool `json:"channelFilter,omitempty"`
}

// Item is a manual entry; shows and seasons expand to their episodes.
type Item struct {
	Key      string `json:"key"`
	Type     string `json:"type"` // movie | show | season | episode
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Season   int    `json:"season,omitempty"`
	Year     int    `json:"year,omitempty"` // for "sort by year" in the editor
}

type AIConfig struct {
	Theme           string       `json:"theme"`
	BatchSize       int          `json:"batchSize,omitempty"`
	EpisodesPerShow int          `json:"episodesPerShow,omitempty"`
	Filter          *rules.Group `json:"filter,omitempty"`
}

// Season is one phase of a seasonal channel, active between two calendar
// days (inclusive, "MM-DD"); ranges may wrap around new year.
type Season struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Emoji  string `json:"emoji,omitempty"`
	From   string `json:"from"`
	To     string `json:"to"`
	Source Source `json:"source"`
	// LogoStyle is the logo while the season runs: "" = the channel's logo, "auto" =
	// generated to match the season, otherwise a generated logo in that style.
	LogoStyle string `json:"logoStyle,omitempty"`
}

// LogoHint returns words that help the automatic logo style of the season.
func (s *Season) LogoHint() string {
	parts := []string{s.Name}
	if s.Source.AI != nil {
		parts = append(parts, s.Source.AI.Theme)
	}
	return strings.Join(parts, " ")
}

func ParseConfig(raw json.RawMessage) (*Config, error) {
	c := &Config{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, c); err != nil {
			// callers that only read settings (logo, tuner) keep working with the defaults
			c = &Config{}
			c.normalize()
			return c, err
		}
	}
	c.normalize()
	return c, nil
}

func (c *Config) normalize() {
	if c.Order != "fixed" {
		c.Order = "shuffle"
	}
	switch c.Transcode {
	case "default", "copy", "audio", "full":
	default:
		c.Transcode = "copy"
	}
	switch c.Credits {
	case "off", "squeeze":
	default:
		c.Credits = "default"
	}
	if c.NoRepeatHours < 0 {
		c.NoRepeatHours = 0
	}
	switch c.Align {
	case 0, 5, 10, 15, 30, 60:
	default:
		c.Align = 0
	}
	c.FillerBetween = min(max(c.FillerBetween, 0), 5)
	if c.Filler != nil && c.Filler.Type == "" {
		c.Filler = nil
	}
	c.Source.normalize()
	for i := range c.Seasons {
		c.Seasons[i].Source.normalize()
	}
	if c.Fallback != nil {
		c.Fallback.normalize()
	}
	if c.Filler != nil {
		c.Filler.normalize()
	}
	switch c.Watermark.Position {
	case "tl", "tr", "bl", "br":
	default:
		c.Watermark.Position = "tr"
	}
	if c.Watermark.Opacity <= 0 || c.Watermark.Opacity > 100 {
		c.Watermark.Opacity = 70
	}
	if c.Watermark.Size <= 0 || c.Watermark.Size > 40 {
		c.Watermark.Size = 14
	}
	switch c.Watermark.Tone {
	case "color", "gray", "white":
	default:
		c.Watermark.Tone = "color"
	}
}

// normalize completes a source of any stored age: lists present, defaults set.
func (s *Source) normalize() {
	switch s.Type {
	case "manual":
		if s.Items == nil {
			s.Items = []Item{}
		}
	case "rules":
		s.Rules = s.Rules.Normalized()
	case "ai":
		if s.AI == nil {
			s.AI = &AIConfig{}
		}
		if s.AI.BatchSize <= 0 {
			s.AI.BatchSize = 12
		}
		if s.AI.EpisodesPerShow <= 0 {
			s.AI.EpisodesPerShow = 2
		}
		if s.AI.Filter != nil {
			s.AI.Filter = s.AI.Filter.Normalized()
		}
	case "trailers":
		s.Rules = s.Rules.Normalized()
		switch s.TrailerOrigin {
		case "library", "discover", "all":
		default:
			s.TrailerOrigin = "library"
		}
	}
}

// FillerOnly reports a channel without titles of its own: a manual channel with
// an empty list that plays nothing but its filler clips (e.g. only trailers).
func (c *Config) FillerOnly(kind string) bool {
	return kind != KindSeasonal && c.Source.Type == "manual" && len(c.Source.Items) == 0 && c.Filler != nil
}

// Validate returns a user-facing error for broken definitions.
func (c *Config) Validate(kind string) error {
	if c.FillerOnly(kind) {
		return c.validateFiller()
	}
	check := func(s *Source, where i18n.Text) error {
		switch s.Type {
		case "manual":
			if len(s.Items) == 0 {
				return i18n.E("config.noTitles", where)
			}
		case "rules":
		case "ai":
			if s.AI == nil || strings.TrimSpace(s.AI.Theme) == "" {
				return i18n.E("config.noTheme", where)
			}
		default:
			return i18n.E("config.unknownSource", where, s.Type)
		}
		return nil
	}
	if kind == KindSeasonal {
		if len(c.Seasons) == 0 {
			return i18n.E("config.noSeason")
		}
		for i := range c.Seasons {
			s := &c.Seasons[i]
			if _, ok := parseDay(s.From); !ok {
				return i18n.E("config.badFrom", s.Name)
			}
			if _, ok := parseDay(s.To); !ok {
				return i18n.E("config.badTo", s.Name)
			}
			if err := check(&s.Source, i18n.M("config.season", s.Name)); err != nil {
				return err
			}
		}
		if c.Fallback != nil {
			if err := check(c.Fallback, i18n.M("config.rest")); err != nil {
				return err
			}
		}
		return c.validateFiller()
	}
	if err := check(&c.Source, i18n.M("config.content")); err != nil {
		return err
	}
	return c.validateFiller()
}

func (c *Config) validateFiller() error {
	if c.Filler == nil {
		return nil
	}
	switch c.Filler.Type {
	case "manual", "rules", "trailers":
		return nil
	case "ai":
		return i18n.E("config.fillerAI")
	}
	return i18n.E("config.fillerUnknown", c.Filler.Type)
}

// parseDay turns "MM-DD" into a comparable month*100+day.
// The date must exist (29 February does, in leap years).
func parseDay(s string) (int, bool) {
	ms, ds, ok := strings.Cut(s, "-")
	m, err1 := strconv.Atoi(ms)
	d, err2 := strconv.Atoi(ds)
	if !ok || err1 != nil || err2 != nil || m < 1 || m > 12 || d < 1 || d > daysIn[m-1] {
		return 0, false
	}
	return m*100 + d, true
}

var daysIn = [12]int{31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// Active reports whether t falls inside the season.
func (s *Season) Active(t time.Time) bool {
	from, ok1 := parseDay(s.From)
	to, ok2 := parseDay(s.To)
	if !ok1 || !ok2 {
		return false
	}
	day := int(t.Month())*100 + t.Day()
	if from <= to {
		return day >= from && day <= to
	}
	return day >= from || day <= to // wraps around new year
}

// SourceByID maps a scheduler source id ("main", "season:<id>", "fallback", "filler") to its definition.
func (c *Config) SourceByID(id string) *Source {
	switch {
	case id == "main":
		return &c.Source
	case id == "fallback":
		return c.Fallback
	case id == "filler":
		return c.Filler
	case strings.HasPrefix(id, "season:"):
		for i := range c.Seasons {
			if "season:"+c.Seasons[i].ID == id {
				return &c.Seasons[i].Source
			}
		}
	}
	return nil
}

// SeasonAt returns the first matching season (list order = priority).
func (c *Config) SeasonAt(t time.Time) *Season {
	for i := range c.Seasons {
		if c.Seasons[i].Active(t) {
			return &c.Seasons[i]
		}
	}
	return nil
}

// LogoHint returns words that help the automatic logo style (theme, genres).
func (c *Config) LogoHint() string {
	var parts []string
	if c.Source.AI != nil {
		parts = append(parts, c.Source.AI.Theme)
	}
	if c.Source.Rules != nil {
		parts = append(parts, ruleWords(c.Source.Rules)...)
	}
	return strings.Join(parts, " ")
}

func ruleWords(g *rules.Group) []string {
	var out []string
	for _, r := range g.Rules {
		if r.Field == "genre" || r.Field == "decade" || r.Field == "collection" {
			out = append(out, r.Values...)
			if r.Field == "decade" {
				for _, v := range r.Values {
					if len(v) == 4 {
						out = append(out, v[2:]+"er")
					}
				}
			}
		}
	}
	for i := range g.Groups {
		out = append(out, ruleWords(&g.Groups[i])...)
	}
	return out
}

// Sources lists every content source of the channel (main or seasons and the
// rest of the year, and the filler) in a fixed order.
func (c *Config) Sources(kind string) []*Source {
	m := c.sources(kind)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*Source, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}

// sources lists every content source of the channel together with its scheduler id.
func (c *Config) sources(kind string) map[string]*Source {
	out := map[string]*Source{}
	if kind == KindSeasonal {
		for i := range c.Seasons {
			out["season:"+c.Seasons[i].ID] = &c.Seasons[i].Source
		}
		if c.Fallback != nil {
			out["fallback"] = c.Fallback
		}
	} else {
		out["main"] = &c.Source
	}
	if c.Filler != nil {
		out["filler"] = c.Filler
	}
	return out
}

// Wants reports whether a newly added library item would appear on this channel.
// AI sources are left out: new titles are considered at the next AI request anyway.
func (c *Config) Wants(kind string, m *store.Media, now time.Time) bool {
	for _, src := range c.sources(kind) {
		switch src.Type {
		case "rules":
			if src.Rules.Matches(m, now) {
				return true
			}
		case "manual":
			for _, it := range src.Items {
				switch it.Type {
				case "show":
					if m.Type == "episode" && m.ShowKey == it.Key {
						return true
					}
				case "season":
					if m.Type == "episode" && m.ShowKey == it.Key && m.Season == it.Season {
						return true
					}
				default:
					if m.RatingKey == it.Key {
						return true
					}
				}
			}
		}
	}
	return false
}

// FillYears sets the year of manual entries saved before it was stored:
// movies and episodes their own, shows and seasons their first regular episode's.
func (c *Config) FillYears(kind string, catalog []*store.Media) {
	byKey := map[string]*store.Media{}
	first := map[string]int{} // "show|season" and "show" -> earliest year
	for _, m := range catalog {
		byKey[m.RatingKey] = m
		if m.Type != "episode" || m.Year == 0 {
			continue
		}
		for _, k := range []string{fmt.Sprintf("%s|%d", m.ShowKey, m.Season), m.ShowKey} {
			if k == m.ShowKey && m.Season == 0 {
				continue // specials often carry odd years
			}
			if y, ok := first[k]; !ok || m.Year < y {
				first[k] = m.Year
			}
		}
	}
	for _, src := range c.sources(kind) {
		if src.Type != "manual" {
			continue
		}
		for i := range src.Items {
			it := &src.Items[i]
			if it.Year != 0 {
				continue
			}
			switch it.Type {
			case "show":
				it.Year = first[it.Key]
			case "season":
				it.Year = first[fmt.Sprintf("%s|%d", it.Key, it.Season)]
			default:
				if m := byKey[it.Key]; m != nil {
					it.Year = m.Year
				}
			}
		}
	}
}

// MissingItems returns the manual entries that no longer exist in the library,
// as "type|key|season" ids for the editor.
func (c *Config) MissingItems(kind string, catalog []*store.Media) []string {
	keys := map[string]bool{}
	shows := map[string]bool{}
	seasons := map[string]bool{}
	for _, m := range catalog {
		keys[m.RatingKey] = true
		if m.Type == "episode" {
			shows[m.ShowKey] = true
			seasons[fmt.Sprintf("%s|%d", m.ShowKey, m.Season)] = true
		}
	}
	out := []string{}
	for _, src := range c.sources(kind) {
		if src.Type != "manual" {
			continue
		}
		for _, it := range src.Items {
			ok := false
			switch it.Type {
			case "show":
				ok = shows[it.Key]
			case "season":
				ok = seasons[fmt.Sprintf("%s|%d", it.Key, it.Season)]
			default:
				ok = keys[it.Key]
			}
			if !ok {
				out = append(out, fmt.Sprintf("%s|%s|%d", it.Type, it.Key, it.Season))
			}
		}
	}
	return out
}
