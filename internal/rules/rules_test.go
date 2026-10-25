package rules

import (
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

func movie(title string, year int, genres ...string) *store.Media {
	return &store.Media{RatingKey: title, Type: "movie", Title: title, Year: year, DurationMs: 90 * 60000, Tags: store.Tags{Genre: genres}}
}

func TestNestedGroups(t *testing.T) {
	items := []*store.Media{
		movie("Zurück in die Zukunft", 1985, "Komödie", "Science-Fiction"),
		movie("Ghostbusters", 1984, "Komödie", "Fantasy"),
		movie("Stirb langsam", 1988, "Action"),
		movie("Hangover", 2009, "Komödie"),
		movie("Die Goonies", 1985, "Abenteuer", "Familie"),
	}
	// (Komödie OR Familie) AND 80s AND NOT Fantasy
	g := &Group{Match: "all", Rules: []Rule{{Field: "decade", Op: "is", Values: []string{"1980"}}},
		Groups: []Group{
			{Match: "any", Rules: []Rule{{Field: "genre", Op: "is", Values: []string{"komödie"}}, {Field: "genre", Op: "is", Values: []string{"Familie"}}}},
			{Match: "none", Rules: []Rule{{Field: "genre", Op: "is", Values: []string{"Fantasy"}}}},
		}}
	got := g.Filter(items)
	if len(got) != 2 || got[0].Title != "Zurück in die Zukunft" || got[1].Title != "Die Goonies" {
		t.Fatalf("unexpected result: %v", titles(got))
	}
}

func TestNumbersAndEmpty(t *testing.T) {
	m := movie("X", 1999)
	now := time.Now()
	if !(&Group{}).Matches(m, now) {
		t.Fatal("empty group must match")
	}
	g := &Group{Match: "all", Rules: []Rule{{Field: "year", Op: "between", Values: []string{"2000", "1990"}}, {Field: "duration", Op: "lt", Values: []string{"100"}}}}
	if !g.Matches(m, now) {
		t.Fatal("between/lt should match")
	}
	// incomplete rules (no value yet in the editor) are ignored
	g = &Group{Match: "all", Rules: []Rule{{Field: "genre", Op: "is"}}}
	if !g.Matches(m, now) {
		t.Fatal("incomplete rule must be ignored")
	}
}

func titles(ms []*store.Media) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Title)
	}
	return out
}

func TestWithout(t *testing.T) {
	g := &Group{Match: "all", Rules: []Rule{{Field: "genre", Op: "is", Values: []string{"Horror"}}, {Field: "library", Op: "is", Values: []string{"1"}}},
		Groups: []Group{{Match: "none", Rules: []Rule{{Field: "actor", Op: "is", Values: []string{"X"}}}}}}
	w := g.Without(map[string]bool{"library": true, "actor": true})
	if len(w.Rules) != 1 || w.Rules[0].Field != "genre" || len(w.Groups) != 0 {
		t.Fatalf("unexpected: %+v", w)
	}
	if len(g.Rules) != 2 || len(g.Groups) != 1 {
		t.Fatal("original group must stay untouched")
	}
	var none *Group
	if !none.Without(map[string]bool{"x": true}).Matches(movie("a", 2000), time.Now()) {
		t.Fatal("nil group matches everything")
	}
}

// Episodes rarely describe their show, so they match its description too.
func TestSummaryOfShow(t *testing.T) {
	ep := &store.Media{RatingKey: "e1", Type: "episode", Title: "Pilot", ShowTitle: "Montalbano", DurationMs: 60 * 60000,
		Summary: "Ein Toter am Strand.", ShowSummary: "Ein Kommissar ermittelt auf Sizilien."}
	now := time.Now()
	in := &Group{Match: "all", Rules: []Rule{{Field: "summary", Op: "contains", Values: []string{"sizilien"}}}}
	if !in.Matches(ep, now) {
		t.Error("contains misses the description of the show")
	}
	out := &Group{Match: "all", Rules: []Rule{{Field: "summary", Op: "notContains", Values: []string{"Sizilien"}}}}
	if out.Matches(ep, now) {
		t.Error("notContains ignores the description of the show")
	}
}

// An episode counts for every decade its show ran in, its year stays its own.
func TestDecadeOfShow(t *testing.T) {
	ep := &store.Media{RatingKey: "e1", Type: "episode", ShowKey: "s", Year: 1994, DurationMs: 60 * 60000,
		ShowFirstYear: 1987, ShowLastYear: 2001}
	now := time.Now()
	for _, d := range []string{"1980", "1990", "2000"} {
		if !(&Group{Match: "all", Rules: []Rule{{Field: "decade", Op: "is", Values: []string{d}}}}).Matches(ep, now) {
			t.Errorf("decade %s does not match", d)
		}
	}
	if (&Group{Match: "all", Rules: []Rule{{Field: "decade", Op: "is", Values: []string{"1970"}}}}).Matches(ep, now) {
		t.Error("decade 1970 matches")
	}
	if (&Group{Match: "all", Rules: []Rule{{Field: "year", Op: "is", Values: []string{"1987"}}}}).Matches(ep, now) {
		t.Error("year is the episode's, not the show's")
	}
}

// Incomplete rules are ignored at any depth, as in the flat form.
func TestIncompleteRulesNested(t *testing.T) {
	m := movie("X", 1999, "Drama")
	now := time.Now()
	incomplete := Group{Match: "all", Rules: []Rule{{Field: "genre", Op: "is"}}}
	if g := (&Group{Match: "none", Groups: []Group{incomplete}}); !g.Matches(m, now) {
		t.Error("none[group of an incomplete rule] excludes everything")
	}
	if g := (&Group{Match: "all", Rules: []Rule{{Field: "genre", Op: "is", Values: []string{"Horror"}}},
		Groups: []Group{{Match: "any", Rules: incomplete.Rules}}}); g.Matches(m, now) {
		t.Error("an incomplete sub-group must not let everything through")
	}
	if g := (&Group{Match: "all", Rules: []Rule{{Field: "year", Op: "between", Values: []string{"abc", "def"}}}}); !g.IsEmpty() {
		t.Error("a year rule without a number is no condition")
	}
	if g := (&Group{Match: "none", Rules: []Rule{{Field: "year", Op: "is", Values: []string{"abc"}}}}); !g.Matches(m, now) {
		t.Error("a year rule without a number excludes everything")
	}
}

// Without leaves an item that lacks a field neither required nor excluded,
// also where the rule sits in an "all" group below a none.
func TestWithoutBelowNone(t *testing.T) {
	kids := &store.Media{RatingKey: "k", Type: "movie", Title: "K", Tags: store.Tags{Genre: []string{"Kids"}}}
	now := time.Now()
	g := &Group{Match: "none", Groups: []Group{{Match: "all", Rules: []Rule{
		{Field: "genre", Op: "is", Values: []string{"Kids"}}, {Field: "country", Op: "is", Values: []string{"US"}}}}}}
	if !g.Without(map[string]bool{"country": true}).Matches(kids, now) {
		t.Error("excluded for a country it does not carry")
	}
	g = &Group{Match: "none", Groups: []Group{{Match: "any", Rules: []Rule{
		{Field: "genre", Op: "is", Values: []string{"Kids"}}, {Field: "country", Op: "is", Values: []string{"US"}}}}}}
	if g.Without(map[string]bool{"country": true}).Matches(kids, now) {
		t.Error("the known genre must still exclude")
	}
}
