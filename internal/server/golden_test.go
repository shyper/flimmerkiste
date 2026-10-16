package server

// Golden tests for everything viewers see on screen: slate texts, lower
// thirds, the credits panel and card, the clock and the XMLTV guide, at fixed
// times in a fixed time zone. The pictures are compared pixel by pixel.
//
// After an intended change, rewrite the references and look at the PNGs:
//
//	FLIMMERKISTE_GOLDEN_UPDATE=1 go test -run TestGolden ./internal/server/

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/news"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/stream"
	"github.com/shyper/flimmerkiste/internal/weather"
)

const goldenDir = "testdata/golden"

func goldenUpdate() bool { return os.Getenv("FLIMMERKISTE_GOLDEN_UPDATE") == "1" }

// berlin fixes time.Local for the test (the times on screen are local).
func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	return loc
}

// goldenText compares got with the file name in goldenDir.
func goldenText(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, langDir(t), name)
	if goldenUpdate() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (create it with FLIMMERKISTE_GOLDEN_UPDATE=1)", name, err)
	}
	if string(want) != got {
		out := filepath.Join(t.TempDir(), filepath.Base(name))
		_ = os.WriteFile(out, []byte(got), 0o644)
		t.Errorf("%s differs from the reference; got written to %s", name, out)
	}
}

// goldenPNG compares a picture pixel by pixel with the PNG name in goldenDir.
func goldenPNG(t *testing.T, name string, img image.Image) {
	t.Helper()
	path := filepath.Join(goldenDir, langDir(t), name)
	if goldenUpdate() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%s: %v (create it with FLIMMERKISTE_GOLDEN_UPDATE=1)", name, err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if diff := pixelDiff(want, img); diff != "" {
		out := filepath.Join(t.TempDir(), filepath.Base(name))
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		_ = os.WriteFile(out, b.Bytes(), 0o644)
		t.Errorf("%s: %s; got written to %s", name, diff, out)
	}
}

func goldenPNGBytes(t *testing.T, name string, data []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	goldenPNG(t, name, img)
}

// langDir keeps the references of other languages than German (the original
// ones) apart: subtests named after the language.
func langDir(t *testing.T) string {
	if strings.HasSuffix(t.Name(), "/en") {
		return "en"
	}
	return ""
}

// pixelDiff describes how b differs from a ("" = identical).
func pixelDiff(a, b image.Image) string {
	if a.Bounds() != b.Bounds() {
		return fmt.Sprintf("size %v, want %v", b.Bounds(), a.Bounds())
	}
	n := 0
	first := image.Point{-1, -1}
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if color.NRGBAModel.Convert(a.At(x, y)) != color.NRGBAModel.Convert(b.At(x, y)) {
				if n == 0 {
					first = image.Pt(x, y)
				}
				n++
			}
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d pixels differ, first at %v", n, first)
}

// goldenWorld is a small fixed library with three channels and a guide around
// Saturday, 12 December 2026, 19:55 in Berlin.
type goldenWorld struct {
	lang i18n.Lang
	srv  *Server
	now  time.Time
	ch   []*store.Channel
	prog map[string]*store.Program
}

func newGoldenWorld(t *testing.T, lang i18n.Lang) *goldenWorld {
	t.Helper()
	loc := berlin(t)
	now := time.Date(2026, 12, 12, 19, 55, 0, 0, loc)
	ctx := context.Background()

	// Posters: a coloured gradient per item, like the demo.
	mux := http.NewServeMux()
	mux.HandleFunc("/photo/:/transcode", func(w http.ResponseWriter, r *http.Request) {
		h := fnv.New32a()
		h.Write([]byte(r.URL.Query().Get("url")))
		v := h.Sum32()
		iw, ih := 200, 300
		if strings.Contains(r.URL.Query().Get("url"), "backdrop") {
			iw, ih = 640, 360 // landscape artwork
		}
		img := image.NewRGBA(image.Rect(0, 0, iw, ih))
		for y := 0; y < ih; y++ {
			for x := 0; x < iw; x++ {
				img.Set(x, y, color.RGBA{uint8(v>>16)/2 + uint8(y/4), uint8(v>>8) / 2, uint8(v)/2 + uint8(x/4), 255})
			}
		}
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, img)
	})
	plex := httptest.NewServer(mux)
	t.Cleanup(plex.Close)

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, sub := range []string{"logos", "cache"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.SetSettings(ctx, map[string]string{
		store.SetServerURL: plex.URL, store.SetServerToken: "tok", store.SetServerID: "golden",
		store.SetServerName: "Wohnzimmer", store.SetOwnerID: "1", store.SetInstanceID: "golden",
		store.SetTrailerCaption: "1", store.SetLanguage: string(lang),
	}))
	must(st.UpsertLibrary(ctx, store.Library{ID: "1", Title: "Filme", Type: "movie"}))
	must(st.UpsertLibrary(ctx, store.Library{ID: "2", Title: "Serien", Type: "show"}))
	movie := func(key, title string, year int, min int, rating float64, genres ...string) *store.Media {
		return &store.Media{RatingKey: key, LibraryID: "1", Type: "movie", Title: title, SortTitle: strings.ToLower(title),
			Year: year, DurationMs: int64(min) * 60000, Rating: rating, ContentRating: "de/12", PartKey: "/library/parts/" + key,
			Summary: "Ein Klassiker: " + title + ". Spannung, Humor und große Gefühle – für einen gemütlichen Abend.",
			Thumb:   "/library/metadata/" + key + "/thumb/1", AddedAt: now.Add(-400 * 24 * time.Hour).Unix(),
			Tags: store.Tags{Genre: genres}}
	}
	items := []*store.Media{
		movie("1", "Zurück in die Zukunft", 1985, 116, 8.5, "Abenteuer", "Komödie", "Science-Fiction"),
		movie("2", "Ghostbusters", 1984, 105, 7.8, "Komödie", "Fantasy"),
		movie("3", "Der Grinch", 2000, 104, 6.3, "Komödie", "Familie", "Fantasy"),
		movie("4", "Stirb langsam", 1988, 132, 8.2, "Action", "Thriller"),
		movie("5", "Kevin – Allein zu Haus", 1990, 103, 7.7, "Komödie", "Familie"),
		{RatingKey: "205", LibraryID: "2", Type: "episode", Title: "Bart, das Genie", DurationMs: 22 * 60000,
			ShowKey: "200", ShowTitle: "Die Simpsons", ShowThumb: "/library/metadata/200/thumb/1", Season: 1, Episode: 2,
			Year: 1990, PartKey: "/library/parts/205", Tags: store.Tags{Genre: []string{"Animation", "Komödie"}}},
	}
	must(st.UpsertMedia(ctx, items, now.Unix()))

	srv := &Server{
		cfg:      config.Config{DataDir: dir, BaseURL: "http://flimmerkiste.local:8089", Horizon: 24},
		store:    st,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		streamer: &stream.Streamer{CacheDir: filepath.Join(dir, "cache")},
		pins:     map[int64]time.Time{},
		clock:    func() time.Time { return now },
	}
	srv.lib = library.New(st, srv.mediaServer, srv.log)

	w := &goldenWorld{lang: lang, srv: srv, now: now, prog: map[string]*store.Program{}}
	chans := []*store.Channel{
		{Number: 1, Name: "Komödien", Enabled: true, Kind: "smart", Color: "#e5a00d", Config: json.RawMessage(`{"logoStyle":"classic"}`)},
		{Number: 2, Name: "Filmklassiker", Enabled: true, Kind: "smart", Color: "#3b82f6", Config: json.RawMessage(`{"logoStyle":"cinema"}`)},
		{Number: 3, Name: "Zeichentrick", Enabled: true, Kind: "smart", Color: "#22c55e", Config: json.RawMessage(`{"logoStyle":"cozy"}`)},
	}
	for _, c := range chans {
		must(st.CreateChannel(ctx, c))
	}
	w.ch = chans
	at := func(h, m int) int64 { return time.Date(2026, 12, 12, h, m, 0, 0, loc).UnixMilli() }
	tomorrow := func(h, m int) int64 { return time.Date(2026, 12, 13, h, m, 0, 0, loc).UnixMilli() }
	monday := func(h, m int) int64 { return time.Date(2026, 12, 14, h, m, 0, 0, loc).UnixMilli() }
	progs := map[string]store.Program{
		// Komödien: Zurück in die Zukunft until 19:59, slate, Ghostbusters at 20:15
		"c1-main":   {ChannelID: chans[0].ID, Start: at(18, 3), Stop: at(19, 59), RatingKey: "1"},
		"c1-card":   {ChannelID: chans[0].ID, Start: at(19, 59), Stop: at(20, 15), RatingKey: store.CardKey, Kind: store.KindCard},
		"c1-next":   {ChannelID: chans[0].ID, Start: at(20, 15), Stop: at(22, 0), RatingKey: "2"},
		"c1-later":  {ChannelID: chans[0].ID, Start: at(22, 0), Stop: at(23, 44), RatingKey: "3"},
		"c1-tmrw":   {ChannelID: chans[0].ID, Start: tomorrow(20, 15), Stop: tomorrow(21, 58), RatingKey: "5"},
		"c1-monday": {ChannelID: chans[0].ID, Start: monday(20, 15), Stop: monday(21, 59), RatingKey: "4"},
		// Filmklassiker: Stirb langsam at 20:15, Der Grinch at 22:30
		"c2-now":  {ChannelID: chans[1].ID, Start: at(18, 0), Stop: at(20, 15), RatingKey: "5"},
		"c2-next": {ChannelID: chans[1].ID, Start: at(20, 15), Stop: at(22, 27), RatingKey: "4"},
		"c2-late": {ChannelID: chans[1].ID, Start: at(22, 30), Stop: at(0, 14) + 24*3600_000, RatingKey: "3"},
		// Zeichentrick: Simpsons now, Kevin at 21:00
		"c3-now":  {ChannelID: chans[2].ID, Start: at(19, 50), Stop: at(20, 12), RatingKey: "205"},
		"c3-next": {ChannelID: chans[2].ID, Start: at(21, 0), Stop: at(22, 43), RatingKey: "5"},
	}
	for name, p := range progs {
		must(st.InsertPrograms(ctx, []store.Program{p}))
		got, err := st.ProgramAt(ctx, p.ChannelID, time.UnixMilli(p.Start))
		must(err)
		w.prog[name] = got
	}
	must(st.UpsertTrailers(ctx, []*store.Trailer{
		{Key: store.TrailerPrefix + "lib-2", Origin: store.OriginLibrary, MediaKey: "2", Title: "Ghostbusters", Kind: "movie",
			Year: 1984, Genres: []string{"Komödie", "Fantasy"}, Thumb: "/library/metadata/2/thumb/1", Name: "Trailer",
			Language: "deu", DurationMs: 90000, Parts: []store.TrailerPart{{Height: 1080, URL: "/library/parts/t2"}}},
		{Key: store.TrailerPrefix + "lib-4", Origin: store.OriginLibrary, MediaKey: "4", Title: "Stirb langsam", Kind: "movie",
			Year: 1988, Genres: []string{"Action", "Thriller"}, Thumb: "/library/metadata/4/thumb/1", Name: "Trailer",
			Language: "deu", DurationMs: 90000, Parts: []store.TrailerPart{{Height: 1080, URL: "/library/parts/t4"}}},
		{Key: store.TrailerPrefix + "lib-1", Origin: store.OriginLibrary, MediaKey: "1", Title: "Zurück in die Zukunft", Kind: "movie",
			Year: 1985, Name: "Trailer", Language: "deu", DurationMs: 90000, Parts: []store.TrailerPart{{Height: 1080, URL: "/library/parts/t1"}}},
		{Key: store.TrailerPrefix + "disc-a", Origin: store.OriginDiscover, Title: "Avengers: Doomsday", Kind: "movie",
			Year: 2026, Released: "2026-12-16", Genres: []string{"Action", "Science-Fiction"}, Hubs: []string{"home.blockbuster-trailers"},
			Name: "Avengers: Doomsday (Trailer 2)", Language: "de", DurationMs: 150000, Parts: []store.TrailerPart{{Height: 1080, URL: "https://example.invalid/a.mp4"}}},
		{Key: store.TrailerPrefix + "disc-b", Origin: store.OriginDiscover, Title: "Stranger Things", Kind: "show",
			Year: 2027, Released: "2027-01-07", Genres: []string{"Drama", "Mystery"},
			Name: "Stranger Things 5 (Trailer)", Language: "de", DurationMs: 120000, Parts: []store.TrailerPart{{Height: 1080, URL: "https://example.invalid/b.mp4"}}},
		{Key: store.TrailerPrefix + "tmdb-1", Origin: store.OriginTMDb, GUID: "tmdb://1", Title: "Avatar: Fire and Ash", Kind: "movie",
			Year: 2026, Released: "2026-12-17", Genres: []string{"Science-Fiction", "Abenteuer"}, ContentRating: "de/12",
			Summary: "Jake Sully und Neytiri stellen sich einem neuen Stamm der Na'vi, der das Feuer verehrt – und alles verändert, was sie über Pandora zu wissen glaubten.",
			Thumb:   plex.URL + "/photo/:/transcode?url=poster-tmdb", Art: plex.URL + "/photo/:/transcode?url=backdrop-tmdb", RuntimeMin: 192,
			Name: "Avatar: Fire and Ash", DurationMs: 20000},
		{Key: store.TrailerPrefix + "disc-c", Origin: store.OriginDiscover, Title: "Wicked: Teil 2", Kind: "movie",
			Year: 2026, Released: "2026-11-20", Genres: []string{"Fantasy", "Musical"}, Hubs: []string{"home.blockbuster-trailers"},
			Name: "Wicked: Teil 2 (Trailer)", Language: "de", DurationMs: 120000, Parts: []store.TrailerPart{{Height: 1080, URL: "https://example.invalid/c.mp4"}}},
	}))
	return w
}

func (w *goldenWorld) trailer(t *testing.T, key string) *store.Trailer {
	t.Helper()
	tr, err := w.srv.store.GetTrailer(context.Background(), store.TrailerPrefix+key)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// cardText lists the texts of a slate.
func cardText(o logo.CardOptions) string {
	return fmt.Sprintf("headline=%q when=%q title=%q subtitle=%q poster=%v logo=%v", o.Headline, o.When, o.Title, o.Subtitle, o.Poster != nil, o.Logo != nil)
}

func TestGoldenTexts(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.DE, i18n.EN} {
		t.Run(string(l), func(t *testing.T) { testGoldenTexts(t, newGoldenWorld(t, l)) })
	}
}

func testGoldenTexts(t *testing.T, w *goldenWorld) {
	ctx := context.Background()
	s := w.srv
	var b strings.Builder
	line := func(name, v string) { fmt.Fprintf(&b, "%s: %s\n", name, v) }

	// airText around midnight, weekdays, the switch to summer time and the new year
	for _, at := range []time.Time{
		w.now.Add(2 * time.Hour), w.now.Add(4*time.Hour + 5*time.Minute), w.now.Add(4*time.Hour + 6*time.Minute),
		w.now.Add(28 * time.Hour), w.now.Add(52 * time.Hour), w.now.Add(6 * 24 * time.Hour),
	} {
		line("airText "+at.Format("Mon 02.01. 15:04"), airText(w.lang, at, w.now))
	}
	spring := time.Date(2027, 3, 27, 23, 30, 0, 0, time.Local)
	line("airText DST", airText(w.lang, time.Date(2027, 3, 28, 3, 15, 0, 0, time.Local), spring))
	newYear := time.Date(2026, 12, 31, 22, 0, 0, 0, time.Local)
	line("airText new year", airText(w.lang, time.Date(2027, 1, 1, 0, 5, 0, 0, time.Local), newYear))

	// releaseText for upcoming, current and older titles
	for _, tr := range []*store.Trailer{
		{Kind: "movie", Released: "2026-12-16", Hubs: []string{"home.blockbuster-trailers"}},
		{Kind: "movie", Released: "2026-12-24"},
		{Kind: "show", Released: "2027-01-07"},
		{Kind: "movie", Released: "2026-11-20", Hubs: []string{"home.blockbuster-trailers"}},
		{Kind: "movie", Released: "2026-03-01"},
		{Kind: "movie"},
	} {
		h, d := releaseText(w.lang, tr, w.now)
		line(fmt.Sprintf("releaseText %s %s %v", tr.Kind, tr.Released, tr.Hubs), fmt.Sprintf("%q %q", h, d))
	}

	// captionKicker: on this channel, on another one, nowhere, Discover titles
	for _, k := range []string{"lib-2", "lib-4", "lib-1", "disc-a", "disc-b", "disc-c"} {
		line("captionKicker "+k, s.captionKicker(ctx, w.ch[0], w.trailer(t, k), w.now))
	}

	// slates
	line("card next", cardText(s.cardOptions(ctx, w.ch[0], w.prog["c1-next"], 720)))
	line("card episode", cardText(s.cardOptions(ctx, w.ch[2], w.prog["c3-now"], 720)))
	soon := *w.prog["c1-next"]
	soon.Start = w.now.Add(time.Minute).UnixMilli()
	line("card soon", cardText(s.cardOptions(ctx, w.ch[0], &soon, 720)))
	line("card none", cardText(s.cardOptions(ctx, w.ch[0], nil, 720)))
	fault := &store.Program{ChannelID: w.ch[0].ID, RatingKey: store.FaultKey, Note: "1"}
	line("fault", cardText(s.faultOptions(ctx, w.ch[0], fault, w.prog["c1-next"], 720)))
	line("fault unknown", cardText(s.faultOptions(ctx, w.ch[0], &store.Program{RatingKey: store.FaultKey, Note: "x"}, nil, 720)))
	for _, p := range []struct{ name, note string }{
		{"promo today", fmt.Sprint(w.prog["c1-next"].Start)},
		{"promo tomorrow", fmt.Sprint(w.prog["c1-tmrw"].Start)},
		{"promo monday", fmt.Sprint(w.prog["c1-monday"].Start)},
		{"promo discover", store.TrailerPrefix + "disc-a"},
		{"promo discover show", store.TrailerPrefix + "disc-b"},
		{"promo tmdb slate", store.TrailerPrefix + "tmdb-1"},
	} {
		prog := &store.Program{ChannelID: w.ch[0].ID, RatingKey: store.PromoKey, Note: p.note}
		line(p.name, cardText(s.promoOptions(ctx, w.ch[0], prog, 720)))
	}

	// credits panel and card
	o, _ := s.creditsOptions(ctx, w.ch[0], w.prog["c1-main"].StopTime(), w.now, 720)
	line("credits", fmt.Sprintf("headline=%q when=%q title=%q subtitle=%q kicker=%q", o.Headline, o.When, o.Title, o.Subtitle, o.Kicker))
	for i, h := range o.Highlights {
		line(fmt.Sprintf("credits highlight %d", i), fmt.Sprintf("when=%q title=%q subtitle=%q", h.When, h.Title, h.Subtitle))
	}
	o, _ = s.creditsOptions(ctx, w.ch[0], w.now.Add(15*time.Minute), w.now, 720)
	line("credits later", fmt.Sprintf("when=%q", o.When))
	// the next title follows right away: still its start time
	o, _ = s.creditsOptions(ctx, w.ch[0], w.prog["c1-next"].StartTime().Add(-time.Minute), w.now, 720)
	line("credits right before", fmt.Sprintf("when=%q title=%q", o.When, o.Title))
	pages, _ := s.creditsCardPages(ctx, w.ch[0], w.prog["c1-main"].StopTime(), w.now, 720)
	for i, p := range pages {
		line(fmt.Sprintf("credits card %d", i), fmt.Sprintf("kicker=%q title=%q", p.Kicker, p.Title))
	}

	// weather and news slates (Jellyfin/Emby)
	rep, hs := goldenInfo(w.now)
	h := s.infoHeader(ctx, w.ch[0], 720, "")
	for _, v := range []struct {
		name      string
		days, usa bool
	}{{"weather now", false, false}, {"weather days", true, false}, {"weather now °F", false, true}} {
		o := weatherSlate(w.lang, h, rep, "Hamburg", v.days, v.usa, w.now)
		line(v.name, fmt.Sprintf("headline=%q line=%q credit=%q now=%q %q %q", o.Headline, o.Line, o.Credit, o.Now.Label, o.Now.Temp, o.NowDetail))
		for _, sl := range o.Slots {
			line(v.name+" slot", fmt.Sprintf("%q %q %s %q %q %q", sl.Label, sl.Sub, sl.Icon, sl.Temp, sl.Low, sl.Rain))
		}
	}
	for _, idx := range []int{1, 2} {
		prog := &store.Program{RatingKey: store.PromoKey, Note: fmt.Sprintf("%snews-%d", store.InfoPrefix, idx), Start: w.now.UnixMilli()}
		o := newsSlate(w.lang, h, hs, w.now.Add(-4*time.Minute), prog)
		line(fmt.Sprint("news ", idx), fmt.Sprintf("headline=%q line=%q", o.Headline, o.Line))
		for _, it := range o.Items {
			line(fmt.Sprint("news ", idx, " item"), fmt.Sprintf("%q %q", it.Meta, it.Title))
		}
	}

	goldenText(t, "tv_texts.txt", b.String())
}

// goldenInfo is a fixed weather report and a few headlines around now.
func goldenInfo(now time.Time) (*weather.Report, []news.Headline) {
	icons := []string{"cloudy", "rain", "rain", "partly-cloudy-night", "clear-night", "fog", "partly-cloudy-day", "snow", "snow", "cloudy", "thunderstorm", "clear-day"}
	rep := &weather.Report{Fetched: now,
		Now: weather.Hour{Time: now.Truncate(30 * time.Minute), Temperature: 2.4, Icon: "partly-cloudy-night", Wind: 14.2, RainChance: -1}}
	start := now.Truncate(time.Hour)
	for i := 0; i < 5*24; i++ {
		t := start.Add(time.Duration(i) * time.Hour)
		rep.Hours = append(rep.Hours, weather.Hour{Time: t, Temperature: 1 + 5*math.Sin(float64(t.Hour()-9)*math.Pi/12) - float64(i)/40,
			Icon: icons[(i/3)%len(icons)], RainChance: (i * 7) % 90, Wind: 10})
	}
	titles := []string{
		"Bundestag beschließt Haushalt für das kommende Jahr nach langer Debatte",
		"Wintereinbruch: Erster Schnee im Norden erwartet",
		"Weihnachtsmärkte melden Besucherrekord",
		"Bahn kündigt neuen Fahrplan an – mehr Verbindungen zwischen Hamburg und Berlin",
		"Forschende entdecken neue Tiefseeart vor Norwegen",
		"Handball-EM: Deutschland zieht ins Halbfinale ein",
		"Sternwarten laden zur Beobachtung der Geminiden ein",
	}
	var hs []news.Headline
	for i, title := range titles {
		src := "tagesschau.de"
		if i%3 == 2 {
			src = "NDR"
		}
		hs = append(hs, news.Headline{Title: title, Source: src, Published: now.Add(-time.Duration(5+i*17) * time.Minute)})
	}
	return rep, hs
}

func TestGoldenPictures(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.DE, i18n.EN} {
		t.Run(string(l), func(t *testing.T) { testGoldenPictures(t, newGoldenWorld(t, l)) })
	}
}

func testGoldenPictures(t *testing.T, w *goldenWorld) {
	ctx := context.Background()
	s := w.srv
	render := func(name string, o logo.CardOptions) {
		t.Helper()
		data, err := logo.RenderCard(o)
		if err != nil {
			t.Fatal(err)
		}
		goldenPNGBytes(t, name, data)
	}
	render("card_next.png", s.cardOptions(ctx, w.ch[0], w.prog["c1-next"], 720))
	render("card_episode.png", s.cardOptions(ctx, w.ch[2], w.prog["c3-now"], 720))
	render("card_none.png", s.cardOptions(ctx, w.ch[1], nil, 720))
	render("card_fault.png", s.faultOptions(ctx, w.ch[0], &store.Program{RatingKey: store.FaultKey, Note: "1"}, w.prog["c1-next"], 720))
	render("card_promo.png", s.promoOptions(ctx, w.ch[0], &store.Program{ChannelID: w.ch[0].ID, RatingKey: store.PromoKey, Note: fmt.Sprint(w.prog["c1-tmrw"].Start)}, 720))
	render("card_promo_discover.png", s.promoOptions(ctx, w.ch[0], &store.Program{RatingKey: store.PromoKey, Note: store.TrailerPrefix + "disc-a"}, 720))
	render("card_1080.png", s.cardOptions(ctx, w.ch[1], w.prog["c2-next"], 1080))
	render("card_slate_tmdb.png", s.promoOptions(ctx, w.ch[0], &store.Program{RatingKey: store.PromoKey, Note: store.TrailerPrefix + "tmdb-1"}, 720))

	// weather and news slates
	rep, hs := goldenInfo(w.now)
	h := s.infoHeader(ctx, w.ch[0], 720, "")
	for name, days := range map[string]bool{"info_weather_now.png": false, "info_weather_days.png": true} {
		data, err := logo.RenderWeather(weatherSlate(w.lang, h, rep, "Hamburg", days, false, w.now))
		if err != nil {
			t.Fatal(err)
		}
		goldenPNGBytes(t, name, data)
	}
	data, err := logo.RenderNews(newsSlate(w.lang, h, hs, w.now.Add(-4*time.Minute),
		&store.Program{RatingKey: store.PromoKey, Note: store.InfoPrefix + "news-1", Start: w.now.UnixMilli()}))
	if err != nil {
		t.Fatal(err)
	}
	goldenPNGBytes(t, "info_news.png", data)

	// pages of a long "up next" slate: the slate, what the title is about, weather and news
	ctx2 := context.Background()
	s.store.SetSettings(ctx2, map[string]string{store.SetWeatherPlace: `{"name":"Hamburg","lat":53.55,"lon":9.99}`,
		store.SetNewsFeeds: "https://example.org/feed.xml"})
	s.info.mu.Lock()
	s.info.weather, s.info.news, s.info.newsLast = rep, hs, w.now.Add(-4*time.Minute)
	s.info.mu.Unlock()
	next := w.prog["c1-next"]
	card := &store.Program{ChannelID: w.ch[0].ID, RatingKey: store.CardKey, Kind: store.KindCard, Start: next.Start - 90_000, Stop: next.Start}
	slatePages, err := s.renderCardPages(ctx2, w.ch[0], card, 720)
	if err != nil || len(slatePages) != 4 {
		t.Fatalf("pages: %v %v", slatePages, err)
	}
	for i, name := range []string{"about", "weather", "news"} {
		data, err := os.ReadFile(slatePages[i+1])
		if err != nil {
			t.Fatal(err)
		}
		goldenPNGBytes(t, "page_"+name+".png", data)
	}
	s.store.SetSettings(ctx2, map[string]string{store.SetWeatherPlace: "", store.SetNewsFeeds: ""})
	// without place and feeds: the slate and what the title is about; other slates never turn pages
	if p, _ := s.renderCardPages(ctx2, w.ch[0], card, 720); len(p) != 2 {
		t.Errorf("without weather and news: %v", p)
	}
	promo := &store.Program{ChannelID: w.ch[0].ID, RatingKey: store.PromoKey, Kind: store.KindCard, Note: store.TrailerPrefix + "disc-a", Start: card.Start, Stop: card.Stop}
	if p, _ := s.renderCardPages(ctx2, w.ch[0], promo, 720); len(p) != 1 {
		t.Errorf("promo: %v", p)
	}

	// lower thirds over trailers
	for _, k := range []string{"lib-2", "disc-a"} {
		prog := &store.Program{ChannelID: w.ch[0].ID, RatingKey: store.TrailerPrefix + k}
		path, err := s.renderCaption(ctx, w.ch[0], prog, 720)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		goldenPNGBytes(t, "caption_"+k+".png", data)
	}

	// panel next to shrunk credits
	o, _ := s.creditsOptions(ctx, w.ch[0], w.prog["c1-main"].StopTime(), w.now, 720)
	s.loadCreditsImages(ctx, w.ch[0], w.now, &o)
	frames, err := logo.RenderCredits(o)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range frames {
		goldenPNG(t, fmt.Sprintf("credits_%d.png", i), f)
	}

	// small card over credits that are not shrunk
	pages, _ := s.creditsCardPages(ctx, w.ch[0], w.prog["c1-main"].StopTime(), w.now, 720)
	cards, err := logo.RenderCaptionPages(pages)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range cards {
		goldenPNG(t, fmt.Sprintf("creditscard_%d.png", i), f)
	}

	// clock
	clock, err := logo.RenderClock(i18n.Clock(w.lang, w.now), 720, stream.ClockPad(720))
	if err != nil {
		t.Fatal(err)
	}
	goldenPNGBytes(t, "clock.png", clock)
}

func TestGoldenXMLTV(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.DE, i18n.EN} {
		t.Run(string(l), func(t *testing.T) { testGoldenXMLTV(t, newGoldenWorld(t, l)) })
	}
}

func testGoldenXMLTV(t *testing.T, w *goldenWorld) {
	req := httptest.NewRequest(http.MethodGet, "http://flimmerkiste.local:8089/epg.xml", nil)
	rec := httptest.NewRecorder()
	w.srv.handleXMLTV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	// logo versions depend on rendering details; the guide's text is what counts
	body := rec.Body.String()
	goldenText(t, "epg.xml", body)
}
