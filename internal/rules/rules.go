// Package rules evaluates user-defined filter trees against library items.
//
// A Group combines its rules and sub-groups with one of three modes:
//
//	all  – every condition must match (AND)
//	any  – at least one condition must match (OR)
//	none – no condition may match (NOT … OR …), i.e. an exclusion list
//
// Groups nest, so "(Comedy OR Family) AND 1980–1989 AND NOT rated 18" is
// all[ any[genre=Comedy, genre=Family], year between 1980 1989, none[rating=18] ].
package rules

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

type Group struct {
	Match  string  `json:"match"` // all | any | none
	Rules  []Rule  `json:"rules"`
	Groups []Group `json:"groups,omitempty"`
}

type Rule struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Values []string `json:"values"`
}

// Normalized returns the group with every list present (also for a nil group),
// so stored filters of any age reach the editor complete.
func (g *Group) Normalized() *Group {
	if g == nil {
		g = &Group{}
	}
	switch g.Match {
	case "all", "any", "none":
	default:
		g.Match = "all"
	}
	if g.Rules == nil {
		g.Rules = []Rule{}
	}
	for i := range g.Rules {
		if g.Rules[i].Values == nil {
			g.Rules[i].Values = []string{}
		}
	}
	for i := range g.Groups {
		g.Groups[i] = *g.Groups[i].Normalized()
	}
	return g
}

// IsEmpty reports whether the group has no conditions at all (matches everything).
func (g *Group) IsEmpty() bool {
	if g == nil {
		return true
	}
	if len(g.Rules) > 0 {
		return false
	}
	for i := range g.Groups {
		if !g.Groups[i].IsEmpty() {
			return false
		}
	}
	return true
}

// Matches evaluates the group against one item. A nil or empty group matches everything.
func (g *Group) Matches(m *store.Media, now time.Time) bool {
	if g == nil {
		return true
	}
	results := make([]bool, 0, len(g.Rules)+len(g.Groups))
	for i := range g.Rules {
		if !g.Rules[i].valid() {
			continue
		}
		results = append(results, g.Rules[i].match(m, now))
	}
	for i := range g.Groups {
		if g.Groups[i].IsEmpty() {
			continue
		}
		results = append(results, g.Groups[i].Matches(m, now))
	}
	if len(results) == 0 {
		return true
	}
	switch g.Match {
	case "any":
		for _, r := range results {
			if r {
				return true
			}
		}
		return false
	case "none":
		for _, r := range results {
			if r {
				return false
			}
		}
		return true
	default: // all
		for _, r := range results {
			if !r {
				return false
			}
		}
		return true
	}
}

// Without returns a copy of the group without rules on the given fields, for
// items that do not carry them (an unknown value neither requires nor excludes).
func (g *Group) Without(fields map[string]bool) *Group {
	if g == nil {
		return nil
	}
	out := &Group{Match: g.Match}
	for _, r := range g.Rules {
		if !fields[r.Field] {
			out.Rules = append(out.Rules, r)
		}
	}
	for i := range g.Groups {
		if sub := g.Groups[i].Without(fields); !sub.IsEmpty() {
			out.Groups = append(out.Groups, *sub)
		}
	}
	return out
}

// Filter returns the items matching the group.
func (g *Group) Filter(items []*store.Media) []*store.Media {
	now := time.Now()
	out := make([]*store.Media, 0, len(items)/4)
	for _, m := range items {
		if g.Matches(m, now) {
			out = append(out, m)
		}
	}
	return out
}

func (r *Rule) valid() bool {
	if r.Field == "" || r.Op == "" {
		return false
	}
	if r.Field == "watched" || r.Op == "exists" || r.Op == "missing" {
		return true
	}
	for _, v := range r.Values {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// ---- field access ----

// Field metadata, also served to the UI so the rule builder stays in sync.
type FieldInfo struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Kind  string   `json:"kind"` // text, tag, number, bool, enum
	Ops   []string `json:"ops"`
	Unit  string   `json:"unit,omitempty"`
}

var (
	textOps   = []string{"is", "isNot", "contains", "notContains", "startsWith"}
	tagOps    = []string{"is", "isNot", "contains", "notContains", "exists", "missing"}
	numberOps = []string{"is", "isNot", "gt", "lt", "between"}
	enumOps   = []string{"is", "isNot"}
)

var Fields = []FieldInfo{
	{ID: "type", Label: "Art", Kind: "enum", Ops: enumOps},
	{ID: "library", Label: "Bibliothek", Kind: "enum", Ops: enumOps},
	{ID: "genre", Label: "Genre", Kind: "tag", Ops: tagOps},
	{ID: "year", Label: "Jahr", Kind: "number", Ops: numberOps},
	{ID: "decade", Label: "Jahrzehnt", Kind: "enum", Ops: enumOps},
	{ID: "title", Label: "Titel", Kind: "text", Ops: textOps},
	{ID: "show", Label: "Serie", Kind: "tag", Ops: tagOps},
	{ID: "season", Label: "Staffel", Kind: "number", Ops: numberOps},
	{ID: "actor", Label: "Darsteller", Kind: "tag", Ops: tagOps},
	{ID: "director", Label: "Regie", Kind: "tag", Ops: tagOps},
	{ID: "writer", Label: "Drehbuch", Kind: "tag", Ops: tagOps},
	{ID: "collection", Label: "Sammlung", Kind: "tag", Ops: tagOps},
	{ID: "label", Label: "Label", Kind: "tag", Ops: tagOps},
	{ID: "studio", Label: "Studio", Kind: "tag", Ops: tagOps},
	{ID: "country", Label: "Land", Kind: "tag", Ops: tagOps},
	{ID: "contentRating", Label: "Altersfreigabe", Kind: "tag", Ops: tagOps},
	{ID: "rating", Label: "Bewertung", Kind: "number", Ops: numberOps, Unit: "/10"},
	{ID: "duration", Label: "Laufzeit", Kind: "number", Ops: numberOps, Unit: "Min."},
	{ID: "watched", Label: "Gesehen", Kind: "bool", Ops: []string{"is"}},
	{ID: "addedDays", Label: "Hinzugefügt vor", Kind: "number", Ops: []string{"lt", "gt"}, Unit: "Tagen"},
	{ID: "summary", Label: "Beschreibung", Kind: "text", Ops: []string{"contains", "notContains"}},
}

func stringsFor(m *store.Media, field string) []string {
	switch field {
	case "type":
		return []string{m.Type}
	case "library":
		return []string{m.LibraryID}
	case "genre":
		return m.Tags.Genre
	case "title":
		if m.Type == "episode" {
			return []string{m.Title, m.ShowTitle}
		}
		return []string{m.Title}
	case "show":
		if m.ShowTitle == "" {
			return nil
		}
		return []string{m.ShowTitle}
	case "actor":
		return m.Tags.Actor
	case "director":
		return m.Tags.Director
	case "writer":
		return m.Tags.Writer
	case "collection":
		return m.Tags.Collection
	case "label":
		return m.Tags.Label
	case "country":
		return m.Tags.Country
	case "studio":
		if m.Studio == "" {
			return nil
		}
		return []string{m.Studio}
	case "contentRating":
		if m.ContentRating == "" {
			return nil
		}
		return []string{m.ContentRating}
	case "summary":
		if m.ShowSummary != "" {
			return []string{m.Summary, m.ShowSummary}
		}
		return []string{m.Summary}
	case "decade":
		if m.Type == "episode" && m.ShowFirstYear > 0 {
			// an episode counts for every decade its show ran in
			var out []string
			for d := m.ShowFirstYear / 10 * 10; d <= m.ShowLastYear; d += 10 {
				out = append(out, strconv.Itoa(d))
			}
			return out
		}
		if m.Year == 0 {
			return nil
		}
		return []string{strconv.Itoa(m.Year / 10 * 10)}
	}
	return nil
}

func numberFor(m *store.Media, field string, now time.Time) (float64, bool) {
	switch field {
	case "year":
		return float64(m.Year), m.Year > 0
	case "season":
		return float64(m.Season), m.Type == "episode"
	case "rating":
		return m.Rating, m.Rating > 0
	case "duration":
		return float64(m.DurationMs) / 60000, true
	case "addedDays":
		if m.AddedAt == 0 {
			return 0, false
		}
		return now.Sub(time.Unix(m.AddedAt, 0)).Hours() / 24, true
	}
	return 0, false
}

func isNumeric(field string) bool {
	switch field {
	case "year", "season", "rating", "duration", "addedDays":
		return true
	}
	return false
}

func (r *Rule) match(m *store.Media, now time.Time) bool {
	if r.Field == "watched" {
		want := len(r.Values) == 0 || r.Values[0] != "false"
		return (m.ViewCount > 0) == want
	}
	if isNumeric(r.Field) {
		v, ok := numberFor(m, r.Field, now)
		if !ok {
			return r.Op == "isNot"
		}
		nums := parseNums(r.Values)
		if len(nums) == 0 {
			return true
		}
		switch r.Op {
		case "is":
			for _, n := range nums {
				if v == n {
					return true
				}
			}
			return false
		case "isNot":
			for _, n := range nums {
				if v == n {
					return false
				}
			}
			return true
		case "gt":
			return v > nums[0]
		case "lt":
			return v < nums[0]
		case "between":
			lo, hi := nums[0], nums[0]
			if len(nums) > 1 {
				hi = nums[1]
			}
			if lo > hi {
				lo, hi = hi, lo
			}
			return v >= lo && v <= hi
		}
		return false
	}

	have := stringsFor(m, r.Field)
	switch r.Op {
	case "exists":
		return len(have) > 0
	case "missing":
		return len(have) == 0
	case "is":
		return anyPair(have, r.Values, strings.EqualFold)
	case "isNot":
		return !anyPair(have, r.Values, strings.EqualFold)
	case "contains":
		return anyPair(have, r.Values, containsFold)
	case "notContains":
		return !anyPair(have, r.Values, containsFold)
	case "startsWith":
		return anyPair(have, r.Values, func(h, v string) bool {
			return strings.HasPrefix(strings.ToLower(h), strings.ToLower(v))
		})
	}
	return false
}

func anyPair(have, want []string, eq func(a, b string) bool) bool {
	for _, w := range want {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		for _, h := range have {
			if eq(h, w) {
				return true
			}
		}
	}
	return false
}

func containsFold(h, v string) bool {
	return strings.Contains(strings.ToLower(h), strings.ToLower(v))
}

func parseNums(vals []string) []float64 {
	var out []float64
	for _, v := range vals {
		v = strings.ReplaceAll(strings.TrimSpace(v), ",", ".")
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// ---- facets for autocompletion ----

type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Facets returns the distinct values of tag-like fields with counts, most common first.
func Facets(items []*store.Media, limit int) map[string][]FacetValue {
	fields := []string{"genre", "actor", "director", "writer", "collection", "label", "studio", "country", "contentRating", "show", "decade"}
	counts := map[string]map[string]int{}
	for _, f := range fields {
		counts[f] = map[string]int{}
	}
	seenShow := map[string]bool{}
	for _, m := range items {
		for _, f := range fields {
			for _, v := range stringsFor(m, f) {
				// Count a value once per show so episodes don't dominate, but
				// from every episode (guest stars).
				if m.Type == "episode" && f != "show" {
					k := m.ShowKey + "|" + f + "|" + v
					if seenShow[k] {
						continue
					}
					seenShow[k] = true
				}
				counts[f][v]++
			}
		}
	}
	out := map[string][]FacetValue{}
	for f, c := range counts {
		list := make([]FacetValue, 0, len(c))
		for v, n := range c {
			list = append(list, FacetValue{v, n})
		}
		sort.Slice(list, func(i, j int) bool {
			if f == "decade" {
				return list[i].Value < list[j].Value
			}
			if list[i].Count != list[j].Count {
				return list[i].Count > list[j].Count
			}
			return list[i].Value < list[j].Value
		})
		if limit > 0 && len(list) > limit {
			list = list[:limit]
		}
		out[f] = list
	}
	return out
}
