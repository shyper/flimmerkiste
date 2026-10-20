package schedule

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

func TestInfoSlatesJoinBreaks(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tmdb := &store.Trailer{Key: store.TrailerPrefix + "tmdb-1", Origin: store.OriginTMDb, GUID: "tmdb://1", Kind: "movie", Title: "Kino", DurationMs: 20000}
	pool := func(platform, origin string) []string {
		st.SetSetting(ctx, store.SetMediaServer, platform)
		g := &generator{s: &Scheduler{store: st}, ctx: ctx, ch: &store.Channel{Kind: KindSmart}, resolved: map[string][]*store.Media{},
			cfg: &Config{Filler: &Source{Type: "trailers", TrailerOrigin: origin}}, trailers: []*store.Trailer{tmdb}}
		g.index()
		var keys []string
		for _, m := range g.fillerPool(time.Now()) {
			keys = append(keys, m.RatingKey)
		}
		return keys
	}
	if got := pool("jellyfin", "all"); len(got) != 1 {
		t.Errorf("nothing configured: %v", got)
	}
	st.SetSettings(ctx, map[string]string{store.SetWeatherPlace: `{"name":"Hamburg","lat":53.55,"lon":9.99}`,
		store.SetNewsFeeds: "https://example.org/rss", store.SetSlateSeconds: "15"})
	if got := pool("jellyfin", "discover"); len(got) != 5 || got[1] != "i:weather-now" || got[4] != "i:news-2" {
		t.Errorf("jellyfin: %v", got)
	}
	if got := pool("emby", "library"); len(got) != 0 {
		t.Errorf("library trailers only: %v", got)
	}
	if got := pool("plex", "all"); len(got) != 1 {
		t.Errorf("plex: %v", got)
	}

	// a slate of its own, as long as set
	st.SetSetting(ctx, store.SetMediaServer, "emby")
	g := &generator{s: &Scheduler{store: st}, ctx: ctx, ch: &store.Channel{Kind: KindSmart}, resolved: map[string][]*store.Media{},
		cfg: &Config{Filler: &Source{Type: "trailers", TrailerOrigin: "all"}}, trailers: []*store.Trailer{}, rng: rand.New(rand.NewSource(1)),
		lastAired: map[string]time.Time{}}
	g.index()
	from := time.Date(2026, 10, 16, 20, 14, 0, 0, time.Local)
	progs := g.fillGap(from, from.Add(time.Minute))
	if len(progs) < 2 || progs[0].RatingKey != store.PromoKey || progs[0].Kind != store.KindCard || progs[0].Stop-progs[0].Start != 15000 ||
		len(progs[0].Note) < 3 || progs[0].Note[:2] != store.InfoPrefix {
		t.Fatalf("break: %+v", progs)
	}
}

// A video from the trailer folder counts as "cinema & streaming", replaces the
// TMDb slate of the same title and is followed by the release date slate.
func TestFolderTrailerReplacesSlate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetSetting(ctx, store.SetMediaServer, "jellyfin")
	slate := &store.Trailer{Key: store.TrailerPrefix + "tmdb-1", Origin: store.OriginTMDb, GUID: "tmdb://1", Kind: "movie", Title: "Kino", DurationMs: 20000}
	other := &store.Trailer{Key: store.TrailerPrefix + "tmdb-2", Origin: store.OriginTMDb, GUID: "tmdb://2", Kind: "movie", Title: "Anderer", DurationMs: 20000}
	video := &store.Trailer{Key: store.TrailerPrefix + "folder-1-de", Origin: store.OriginFolder, GUID: "tmdb://1", Kind: "movie", Title: "Kino",
		Language: "de", DurationMs: 90000, Parts: []store.TrailerPart{{Height: 1080, URL: store.FileURL("/trailers/Kino.mp4")}}}
	for _, origin := range []string{"discover", "all", "library"} {
		g := &generator{s: &Scheduler{store: st}, ctx: ctx, ch: &store.Channel{Kind: KindSmart}, resolved: map[string][]*store.Media{},
			cfg: &Config{Filler: &Source{Type: "trailers", TrailerOrigin: origin}}, trailers: []*store.Trailer{slate, video, other}}
		g.index()
		var keys []string
		for _, m := range g.fillerPool(time.Now()) {
			keys = append(keys, m.RatingKey)
		}
		want := []string{video.Key, other.Key}
		if origin == "library" {
			want = nil
		}
		if len(keys) != len(want) || (len(want) > 0 && (keys[0] != want[0] || keys[1] != want[1])) {
			t.Errorf("%s: %v, want %v", origin, keys, want)
		}
	}
	from := time.Now()
	progs := fillerPrograms(video.Media(720), from, from.Add(10*time.Minute))
	if len(progs) != 2 || progs[0].RatingKey != video.Key || progs[1].RatingKey != store.PromoKey || progs[1].Note != video.Key {
		t.Fatalf("programs: %+v", progs)
	}
}
