package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
)

func TestActiveLogoSeasons(t *testing.T) {
	today := time.Now().Format("01-02")
	other := time.Now().AddDate(0, 6, 0).Format("01-02")
	seasonal := func(style, from string) *store.Channel {
		cfg := schedule.Config{LogoStyle: "classic", LogoHue: 60, LogoHoliday: true, Seasons: []schedule.Season{
			{ID: "s", Name: "Halloween", Emoji: "🎃", From: from, To: from, LogoStyle: style}}}
		raw, _ := json.Marshal(cfg)
		return &store.Channel{ID: 1, Name: "Saisonkino", Kind: schedule.KindSeasonal, Logo: "ch1-rest.png", Config: raw}
	}
	s := &Server{}

	// outside the seasons: the channel's own logo
	if file, _ := s.activeLogo(seasonal("", other)); file != "ch1-rest.png" {
		t.Errorf("rest of the year: got %q", file)
	}
	// running season with "Standard": the channel's logo; its automatic style follows the season
	if file, _ := s.activeLogo(seasonal("", today)); file != "ch1-rest.png" {
		t.Errorf("standard season logo: got %q", file)
	}
	ch := seasonal("", today)
	ch.Logo = ""
	if _, o := s.activeLogo(ch); o.Style != "classic" || o.Hue != 60 || !strings.Contains(o.Hint, "Halloween") {
		t.Errorf("standard generated season logo: %+v", o)
	}
	// "Automatisch": generated, themed after the season, with its symbol
	file, o := s.activeLogo(seasonal("auto", today))
	if file != "" || o.Style != "auto" || o.Hue != 0 || !strings.Contains(o.Hint, "Halloween") || o.Symbol != "🎃" {
		t.Errorf("automatic season logo: %q %+v", file, o)
	}
	// chosen style
	if file, o := s.activeLogo(seasonal("gothic", today)); file != "" || o.Style != "gothic" {
		t.Errorf("season style: %q %+v", file, o)
	}
}
