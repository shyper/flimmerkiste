package schedule

import (
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/store"
)

func TestSeasonWrapAndPriority(t *testing.T) {
	cfg := SeasonalPreset(i18n.DE)
	cases := map[string]string{
		"2026-12-24": "christmas", "2026-12-31": "winter", "2027-01-15": "winter",
		"2027-04-01": "easter", "2027-05-10": "spring", "2027-07-01": "summer",
		"2026-10-31": "halloween", "2026-09-10": "autumn",
	}
	for day, want := range cases {
		d, _ := time.Parse("2006-01-02", day)
		s := cfg.SeasonAt(d)
		if s == nil || s.ID != want {
			got := "<none>"
			if s != nil {
				got = s.ID
			}
			t.Errorf("%s: got %s, want %s", day, got, want)
		}
	}
	if err := cfg.Validate(KindSeasonal); err != nil {
		t.Fatal(err)
	}
}

func TestAlign(t *testing.T) {
	at := time.Date(2026, 10, 10, 20, 5, 30, 0, time.Local)
	if got := alignDown(at, 15*time.Minute); !got.Equal(time.Date(2026, 10, 10, 20, 0, 0, 0, time.Local)) {
		t.Errorf("alignDown: %s", got)
	}
	if got := alignUp(at, 15*time.Minute); !got.Equal(time.Date(2026, 10, 10, 20, 15, 0, 0, time.Local)) {
		t.Errorf("alignUp: %s", got)
	}
	exact := time.Date(2026, 10, 10, 20, 15, 0, 0, time.Local)
	if !alignDown(exact, 15*time.Minute).Equal(exact) || !alignUp(exact, 15*time.Minute).Equal(exact) {
		t.Error("round times stay as they are")
	}
}

func TestFillYears(t *testing.T) {
	catalog := []*store.Media{
		{RatingKey: "1", Type: "movie", Year: 2016},
		{RatingKey: "10", Type: "episode", ShowKey: "s", Season: 0, Year: 1990}, // special
		{RatingKey: "11", Type: "episode", ShowKey: "s", Season: 1, Year: 2001},
		{RatingKey: "12", Type: "episode", ShowKey: "s", Season: 2, Year: 2003},
	}
	cfg := &Config{Source: Source{Type: "manual", Items: []Item{
		{Key: "1", Type: "movie"}, {Key: "s", Type: "show"}, {Key: "s", Type: "season", Season: 2},
		{Key: "12", Type: "episode"}, {Key: "1", Type: "movie", Year: 1999}, {Key: "gone", Type: "movie"},
	}}}
	cfg.FillYears(KindManual, catalog)
	want := []int{2016, 2001, 2003, 2003, 1999, 0}
	for i, it := range cfg.Source.Items {
		if it.Year != want[i] {
			t.Errorf("item %d (%s %s): year %d, want %d", i, it.Type, it.Key, it.Year, want[i])
		}
	}
}

func TestNextStartIgnoresSeconds(t *testing.T) {
	at := func(h, m, s int) time.Time { return time.Date(2026, 10, 10, h, m, s, 0, time.Local) }
	step := 15 * time.Minute
	cases := []struct{ end, want time.Time }{
		{at(20, 0, 0), at(20, 0, 0)},    // exactly on time
		{at(20, 0, 35), at(20, 0, 35)},  // seconds over: next title follows directly
		{at(20, 0, 59), at(20, 0, 59)},  // still within the minute
		{at(20, 1, 0), at(20, 15, 0)},   // a full minute over: wait for the slot
		{at(20, 14, 30), at(20, 15, 0)}, // just before a slot
	}
	for _, c := range cases {
		if got := nextStart(c.end, step); !got.Equal(c.want) {
			t.Errorf("end %s: got %s, want %s", c.end.Format("15:04:05"), got.Format("15:04:05"), c.want.Format("15:04:05"))
		}
	}
}

func TestSamePlanIgnoresLooks(t *testing.T) {
	base := Config{Order: "shuffle", Transcode: "copy", Align: 15, Source: Source{Type: "manual", Items: []Item{{Key: "1"}}}}
	looks := base
	looks.Transcode, looks.LogoStyle, looks.LogoHue, looks.Watermark = "full", "retro", 120, Watermark{Enabled: true}
	if !base.SamePlan(looks) {
		t.Error("technique, logo and watermark must not count as programme changes")
	}
	for name, change := range map[string]func(*Config){
		"order":   func(c *Config) { c.Order = "fixed" },
		"align":   func(c *Config) { c.Align = 30 },
		"source":  func(c *Config) { c.Source.Items = append(c.Source.Items, Item{Key: "2"}) },
		"filler":  func(c *Config) { c.Filler = &Source{Type: "trailers"} },
		"repeat":  func(c *Config) { c.NoRepeatHours = 12 },
		"preview": func(c *Config) { c.Previews = true },
	} {
		c := base
		c.Source.Items = append([]Item(nil), base.Source.Items...)
		change(&c)
		if base.SamePlan(c) {
			t.Errorf("%s change not detected", name)
		}
	}
}

func TestFillerOnly(t *testing.T) {
	trailers := &Source{Type: "trailers", TrailerOrigin: "library"}
	empty := Config{Source: Source{Type: "manual"}, Filler: trailers}
	if !empty.FillerOnly(KindManual) || empty.Validate(KindManual) != nil {
		t.Fatal("an empty manual channel with trailers is valid and plays only them")
	}
	noFiller := Config{Source: Source{Type: "manual"}}
	if noFiller.FillerOnly(KindManual) || noFiller.Validate(KindManual) == nil {
		t.Fatal("an empty manual channel without filler stays invalid")
	}
	withItems := Config{Source: Source{Type: "manual", Items: []Item{{Key: "1", Type: "movie"}}}, Filler: trailers}
	if withItems.FillerOnly(KindManual) {
		t.Fatal("a channel with titles is not filler-only")
	}
}

// TestWatermarkToneDefault: channels saved before the tone existed keep the
// original colors; unknown tones fall back to them too.
func TestWatermarkToneDefault(t *testing.T) {
	for in, want := range map[string]string{"": "color", "gray": "gray", "white": "white", "sepia": "color"} {
		c := Config{Watermark: Watermark{Tone: in}}
		c.normalize()
		if c.Watermark.Tone != want {
			t.Errorf("tone %q: got %q, want %q", in, c.Watermark.Tone, want)
		}
	}
}
