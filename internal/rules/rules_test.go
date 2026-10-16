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
