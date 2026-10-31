package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/stream"
)

func TestAirText(t *testing.T) {
	now := time.Date(2026, 10, 10, 19, 55, 0, 0, time.Local) // Saturday
	cases := map[time.Time]string{
		now.Add(2 * time.Hour):                            "Heute um 21:55 Uhr",
		time.Date(2026, 10, 11, 0, 30, 0, 0, time.Local):  "Morgen um 00:30 Uhr",
		time.Date(2026, 10, 12, 20, 15, 0, 0, time.Local): "Montag um 20:15 Uhr",
	}
	for at, want := range cases {
		if got := airText(i18n.DE, at, now); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestReleaseText(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)
	cinema := &store.Trailer{Kind: "movie", Released: "2026-12-16", Hubs: []string{"home.blockbuster-trailers"}}
	if h, d := releaseText(i18n.DE, cinema, now); h != "Demnächst im Kino" || d != "Ab 16. Dezember" {
		t.Errorf("cinema: %q %q", h, d)
	}
	movie := &store.Trailer{Kind: "movie", Released: "2026-10-23", Hubs: []string{"home.coming-soon"}}
	if h, d := releaseText(i18n.DE, movie, now); h != "Demnächst im Kino" || d != "Ab 23. Oktober" {
		t.Errorf("movie from coming soon: %q %q", h, d)
	}
	show := &store.Trailer{Kind: "show", Released: "2027-01-07"}
	if h, d := releaseText(i18n.DE, show, now); h != "Demnächst" || d != "Ab 7. Januar 2027" {
		t.Errorf("show: %q %q", h, d)
	}
	newShow := &store.Trailer{Origin: store.OriginTMDb, Kind: "show", Released: "2026-11-05"}
	if h, d := releaseText(i18n.DE, newShow, now); h != "Neue Serie" || d != "Ab 5. November" {
		t.Errorf("new show: %q %q", h, d)
	}
	season := &store.Trailer{Origin: store.OriginTMDb, Kind: "show", Season: 3, Released: "2026-11-05"}
	if h, d := releaseText(i18n.DE, season, now); h != "Neue Staffel" || d != "Staffel 3 ab 5. November" {
		t.Errorf("new season: %q %q", h, d)
	}
	old := &store.Trailer{Kind: "movie", Released: "2026-09-01"}
	if h, _ := releaseText(i18n.DE, old, now); h != "Neu" {
		t.Errorf("released: %q", h)
	}
}

func TestCardAudio(t *testing.T) {
	ctx := t.Context()
	tmp := t.TempDir()
	st, err := store.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	srv := &Server{
		store:    st,
		cfg:      config.Config{DataDir: tmp},
		streamer: &stream.Streamer{CacheDir: tmp},
	}

	// Default setting ("1") -> returns default music path
	p, _ := srv.cardAudio(ctx, &store.Channel{}, &store.Program{})
	if p == "" {
		t.Fatal("expected default music path, got empty")
	}
	// the short slate after a trailer stays silent
	if got, _ := srv.cardAudio(ctx, &store.Channel{}, &store.Program{RatingKey: store.PromoKey, Stop: 20000}); got != "" {
		t.Fatalf("promo slate plays %s", got)
	}

	// Disable setting ("0") -> returns empty
	_ = st.SetSetting(ctx, store.SetCardMusic, "0")
	if got, _ := srv.cardAudio(ctx, &store.Channel{}, &store.Program{}); got != "" {
		t.Fatalf("expected empty when disabled, got %s", got)
	}

	// Re-enable setting ("1") and add custom file in DataDir/music
	_ = st.SetSetting(ctx, store.SetCardMusic, "1")
	musicDir := filepath.Join(tmp, "music")
	if err := os.MkdirAll(musicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(musicDir, "custom.mp3")
	if err := os.WriteFile(customPath, []byte("fake-mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := srv.cardAudio(ctx, &store.Channel{}, &store.Program{}); got != customPath {
		t.Fatalf("expected custom path %s, got %s", customPath, got)
	}
}

// TestThemeFile fetches theme music from Plex once and keeps it, also the
// answer that an item has none.
func TestThemeFile(t *testing.T) {
	ctx := t.Context()
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "cache"), 0o755)
	st, err := store.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hits := map[string]int{}
	plex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		if r.Header.Get("X-Plex-Token") != "tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/library/metadata/7/theme" {
			w.Write([]byte("mp3"))
			return
		}
		http.NotFound(w, r)
	}))
	defer plex.Close()
	st.SetSettings(ctx, map[string]string{store.SetServerURL: plex.URL, store.SetServerToken: "tok"})
	srv := &Server{store: st, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv.cfg.DataDir = tmp

	for run := 0; run < 2; run++ {
		if p := srv.themeFile(ctx, "7"); p == "" {
			t.Fatal("theme of 7 missing")
		} else if b, _ := os.ReadFile(p); string(b) != "mp3" {
			t.Fatalf("theme of 7: %q", b)
		}
		if p := srv.themeFile(ctx, "8"); p != "" {
			t.Fatalf("8 has no theme, got %s", p)
		}
	}
	if hits["/library/metadata/7/theme"] != 1 || hits["/library/metadata/8/theme"] != 1 {
		t.Errorf("Plex asked again instead of the cache: %v", hits)
	}
}

func TestSeenText(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.Local)
	ago := func(days int) int64 { return now.AddDate(0, 0, -days).UnixMilli() }
	cases := []struct {
		watched int64
		want    string
	}{
		{0, "Noch nie gesehen"},
		{now.Add(-8 * time.Hour).UnixMilli(), "Zuletzt heute gesehen"},
		{now.Add(-10 * time.Hour).UnixMilli(), "Zuletzt gestern gesehen"}, // 23:00 the day before
		{ago(3), "Zuletzt vor 3 Tagen gesehen"},
		{ago(10), "Zuletzt vor einer Woche gesehen"},
		{ago(45), "Zuletzt vor 6 Wochen gesehen"},
		{ago(200), "Zuletzt vor 6 Monaten gesehen"},
		{ago(400), "Zuletzt vor einem Jahr gesehen"},
		{ago(3 * 365), "Zuletzt vor 3 Jahren gesehen"},
		{now.Add(time.Hour).UnixMilli(), "Zuletzt heute gesehen"}, // clocks a little apart
	}
	for _, c := range cases {
		if got := seenText(i18n.DE, &store.Media{}, c.watched, now); got != c.want {
			t.Errorf("%v: got %q, want %q", time.UnixMilli(c.watched), got, c.want)
		}
	}
	// the later of the media server and the channels counts
	m := &store.Media{ViewCount: 1, LastViewed: now.AddDate(0, 0, -2).Unix()}
	if got := seenText(i18n.EN, m, ago(30), now); got != "Last watched 2 days ago" {
		t.Errorf("media server later: %q", got)
	}
	if got := seenText(i18n.EN, m, ago(0), now); got != "Last watched today" {
		t.Errorf("channel later: %q", got)
	}
	// played according to the media server, but without a date: no line
	if got := seenText(i18n.EN, &store.Media{ViewCount: 1}, 0, now); got != "" {
		t.Errorf("played without date: %q", got)
	}
}
