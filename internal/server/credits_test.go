package server

import (
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

func TestPickHighlights(t *testing.T) {
	at := time.Date(2026, 10, 14, 19, 50, 0, 0, time.Local)
	a, b, c := &store.Channel{ID: 1}, &store.Channel{ID: 2}, &store.Channel{ID: 3}
	h := func(ch *store.Channel, key string, start time.Time, m *store.Media) highlight {
		m.RatingKey = key
		return highlight{ch: ch, prog: store.Program{Start: start.UnixMilli()}, m: m, score: highlightScore(m, start, at)}
	}
	film := func() *store.Media { return &store.Media{Type: "movie", Rating: 8} }
	ep := func() *store.Media { return &store.Media{Type: "episode", Rating: 8} }
	all := []highlight{
		h(a, "a1", at.Add(25*time.Minute), film()), // prime time film
		h(a, "a2", at.Add(40*time.Minute), film()), // second film on the same channel
		h(b, "b1", at.Add(10*time.Minute), ep()),
		h(c, "c1", at.Add(150*time.Minute), ep()),
	}
	got := pickHighlights(all, 3)
	var keys []string
	for _, x := range got {
		keys = append(keys, x.m.RatingKey)
	}
	// one per channel first (a's second film loses to c), ordered by start
	if len(keys) != 3 || keys[0] != "b1" || keys[1] != "a1" || keys[2] != "c1" {
		t.Fatalf("got %v", keys)
	}
	if got := pickHighlights(all, 4); len(got) != 4 {
		t.Fatalf("a second title per channel fills up, got %d", len(got))
	}
}
