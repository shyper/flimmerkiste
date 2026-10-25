package server

import (
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
)

func TestStreamsBusy(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.Local)
	cases := []struct {
		name    string
		streams int
		lastEnd time.Time
		want    bool
	}{
		{"never streamed", 0, time.Time{}, false},
		{"streaming", 1, now.Add(-time.Hour), true},
		{"just switched channel", 0, now.Add(-30 * time.Second), true},
		{"stream ended a while ago", 0, now.Add(-5 * time.Minute), false},
	}
	for _, c := range cases {
		if got := streamsBusy(c.streams, c.lastEnd, now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNoInfoProgrammes(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	old := time.Local
	time.Local = loc
	defer func() { time.Local = old }()
	// across the switch to winter time on 25 October (2:00–3:00 twice)
	from := time.Date(2026, 10, 24, 19, 0, 0, 0, loc)
	got := noInfoProgrammes(i18n.DE, "7", "http://flimmerkiste/logos/7.png", from, from.Add(32*time.Hour)) // until 26.10. 2:00 (one hour gained)
	want := [][2]string{
		{"20261024000000 +0200", "20261025000000 +0200"},
		{"20261025000000 +0200", "20261026000000 +0100"}, // 25 hours
		{"20261026000000 +0100", "20261027000000 +0100"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	for i, p := range got {
		if p.Start != want[i][0] || p.Stop != want[i][1] || p.Channel != "7" || p.Title.Value != "Keine Programminformationen verfügbar" || p.Date != "" ||
			p.Desc == nil || len(p.Categories) == 0 || p.Icon == nil || p.Icon.Src != "http://flimmerkiste/logos/7.png" {
			t.Errorf("entry %d: %+v", i, p)
		}
	}
}
