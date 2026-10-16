package server

// Interactive UI demo with a fake Plex library (no real media, no ffmpeg needed):
//
//	FLIMMERKISTE_DEMO=1 go test -run TestDemo -timeout 0 ./internal/server/
//
// FLIMMERKISTE_DEMO_LANG=en shows it in English (titles, genres, channels), as in
// the screenshots of the README.
//
// Then open http://localhost:18090 (session cookie "flimmerkiste_session=demo" is set on first visit).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
)

func TestDemo(t *testing.T) {
	if os.Getenv("FLIMMERKISTE_DEMO") != "1" {
		t.Skip("set FLIMMERKISTE_DEMO=1 to run")
	}
	en := os.Getenv("FLIMMERKISTE_DEMO_LANG") == "en"
	// tr picks the English or the German text.
	tr := func(de, eng string) string {
		if en {
			return eng
		}
		return de
	}
	genreEN := map[string]string{"Komödie": "Comedy", "Science-Fiction": "Science Fiction", "Abenteuer": "Adventure",
		"Familie": "Family", "Liebesfilm": "Romance", "Animation": "Animation"}
	genre := func(g string) string {
		if e, ok := genreEN[g]; ok && en {
			return e
		}
		return g
	}
	films := []struct {
		title, titleEN string
		year           int
		genre          []string
		min            int
	}{
		{"Zurück in die Zukunft", "Back to the Future", 1985, []string{"Komödie", "Science-Fiction"}, 116},
		{"Ghostbusters", "Ghostbusters", 1984, []string{"Komödie", "Fantasy"}, 105},
		{"Die Goonies", "The Goonies", 1985, []string{"Abenteuer", "Familie"}, 114},
		{"Stirb langsam", "Die Hard", 1988, []string{"Action", "Thriller"}, 132},
		{"Kevin – Allein zu Haus", "Home Alone", 1990, []string{"Komödie", "Familie"}, 103},
		{"Der Grinch", "How the Grinch Stole Christmas", 2000, []string{"Komödie", "Familie", "Fantasy"}, 104},
		{"Tatsächlich… Liebe", "Love Actually", 2003, []string{"Komödie", "Liebesfilm"}, 135},
		{"Halloween", "Halloween", 1978, []string{"Horror"}, 91},
		{"Shining", "The Shining", 1980, []string{"Horror", "Drama"}, 146},
		{"The Sixth Sense", "The Sixth Sense", 1999, []string{"Mystery", "Thriller"}, 107},
		{"Fight Club", "Fight Club", 1999, []string{"Drama"}, 139},
		{"Inception", "Inception", 2010, []string{"Action", "Science-Fiction"}, 148},
		{"Harry Potter und der Stein der Weisen", "Harry Potter and the Philosopher's Stone", 2001, []string{"Fantasy", "Abenteuer"}, 152},
		{"Harry Potter und die Kammer des Schreckens", "Harry Potter and the Chamber of Secrets", 2002, []string{"Fantasy", "Abenteuer"}, 161},
		{"Harry Potter und der Gefangene von Askaban", "Harry Potter and the Prisoner of Azkaban", 2004, []string{"Fantasy", "Abenteuer"}, 142},
		{"Ferris macht blau", "Ferris Bueller's Day Off", 1986, []string{"Komödie"}, 103},
		{"Top Gun", "Top Gun", 1986, []string{"Action", "Drama"}, 110},
		{"E.T. – Der Außerirdische", "E.T. the Extra-Terrestrial", 1982, []string{"Science-Fiction", "Familie"}, 115},
		{"Jurassic Park", "Jurassic Park", 1993, []string{"Abenteuer", "Science-Fiction"}, 127},
		{"Die Hard 2", "Die Hard 2", 1990, []string{"Action"}, 124},
	}
	var movies []map[string]any
	for i, f := range films {
		var g []map[string]string
		for _, x := range f.genre {
			g = append(g, map[string]string{"tag": genre(x)})
		}
		key := fmt.Sprint(i + 1)
		title := tr(f.title, f.titleEN)
		movies = append(movies, map[string]any{"ratingKey": key, "type": "movie", "title": title, "year": f.year,
			"duration": f.min * 60000, "summary": tr("Ein Klassiker: "+title+". Spannung, Humor und große Gefühle.",
				"A classic: "+title+". Suspense, humour and big emotions."),
			"thumb": "/library/metadata/" + key + "/thumb/1", "Genre": g, "contentRating": tr("FSK 12", "PG-13"),
			"audienceRating": 7.5, "addedAt": time.Now().Add(-time.Duration(i*5) * 24 * time.Hour).Unix(), "viewCount": i % 3,
			"Media": []map[string]any{{"duration": f.min * 60000, "Part": []map[string]any{{"key": "/library/parts/" + key}}}}})
	}
	episodeTitles := []string{"Simpsons Roasting on an Open Fire", "Bart the Genius", "Homer's Odyssey",
		"There's No Disgrace Like Home", "Bart the General", "Moaning Lisa", "Bart Gets an F", "Simpson and Delilah",
		"Treehouse of Horror", "Two Cars in Every Garage and Three Eyes on Every Fish", "Dancin' Homer", "Dead Putting Society"}
	var episodes []map[string]any
	for s := 1; s <= 2; s++ {
		for e := 1; e <= 6; e++ {
			key := fmt.Sprintf("2%d%02d", s, e)
			episodes = append(episodes, map[string]any{"ratingKey": key, "type": "episode", "title": tr(fmt.Sprintf("Folge %d", e), episodeTitles[(s-1)*6+e-1]),
				"index": e, "parentIndex": s, "grandparentRatingKey": "200", "grandparentTitle": tr("Die Simpsons", "The Simpsons"),
				"grandparentThumb": "/library/metadata/200/thumb/1", "duration": 22 * 60000, "year": 1990,
				"Media": []map[string]any{{"duration": 22 * 60000, "Part": []map[string]any{{"key": "/library/parts/" + key}}}}})
		}
	}
	shows := []map[string]any{{"ratingKey": "200", "type": "show", "title": tr("Die Simpsons", "The Simpsons"), "year": 1989,
		"Genre": []map[string]string{{"tag": genre("Komödie")}, {"tag": "Animation"}}}}

	mux := http.NewServeMux()
	mc := func(w http.ResponseWriter, v map[string]any) {
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": v})
	}
	mux.HandleFunc("/identity", func(w http.ResponseWriter, r *http.Request) { mc(w, map[string]any{"machineIdentifier": "demo"}) })
	mux.HandleFunc("/library/sections", func(w http.ResponseWriter, r *http.Request) {
		mc(w, map[string]any{"Directory": []map[string]string{{"key": "1", "title": tr("Filme", "Movies"), "type": "movie"}, {"key": "2", "title": tr("Serien", "Shows"), "type": "show"}}})
	})
	mux.HandleFunc("/library/sections/{id}/all", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Container-Start") != "0" {
			mc(w, map[string]any{"Metadata": []any{}})
			return
		}
		switch r.PathValue("id") + "/" + r.URL.Query().Get("type") {
		case "1/1":
			mc(w, map[string]any{"Metadata": movies})
		case "2/2":
			mc(w, map[string]any{"Metadata": shows})
		case "2/4":
			mc(w, map[string]any{"Metadata": episodes})
		default:
			mc(w, map[string]any{"Metadata": []any{}})
		}
	})
	// Posters: a coloured gradient per item.
	mux.HandleFunc("/photo/:/transcode", func(w http.ResponseWriter, r *http.Request) {
		h := fnv.New32a()
		h.Write([]byte(r.URL.Query().Get("url")))
		v := h.Sum32()
		img := image.NewRGBA(image.Rect(0, 0, 200, 300))
		for y := 0; y < 300; y++ {
			for x := 0; x < 200; x++ {
				img.Set(x, y, color.RGBA{uint8(v>>16)/2 + uint8(y/4), uint8(v>>8) / 2, uint8(v)/2 + uint8(x/4), 255})
			}
		}
		var b bytes.Buffer
		png.Encode(&b, img)
		w.Header().Set("Content-Type", "image/png")
		w.Write(b.Bytes())
	})
	plex := httptest.NewServer(mux)
	defer plex.Close()

	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, FFmpeg: "ffmpeg", Horizon: 24, BaseURL: "http://192.168.1.10:8089"}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st.SetSettings(ctx, map[string]string{
		store.SetServerURL: plex.URL, store.SetServerToken: "tok", store.SetServerID: "demo",
		store.SetServerName: tr("Wohnzimmer-Server", "Living room"), store.SetOwnerID: "1", store.SetLanguage: tr("de", "en"),
	})
	u := &store.User{Provider: "plex", ExternalID: "1", Username: "Alex", Role: "admin"}
	st.UpsertUser(ctx, u)
	st.CreateSession(ctx, "demo", u.ID, 24*time.Hour)

	webDir, _ := filepath.Abs("../../web/dist")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv, err := New(cfg, st, log, os.DirFS(webDir))
	if err != nil {
		t.Fatal(err)
	}
	srv.lib.StartSync()
	for srv.lib.Status().Running {
		time.Sleep(50 * time.Millisecond)
	}
	mk := func(name string, number int, kind, cfgJSON string) {
		c := &store.Channel{Number: number, Name: name, Enabled: true, Kind: kind, Config: json.RawMessage(cfgJSON)}
		st.CreateChannel(ctx, c)
		srv.sched.Reset(ctx, c.ID)
	}
	hp := func(i int) string { return tr(films[i].title, films[i].titleEN) }
	mk(tr("Komödien Kanal", "Comedy Club"), 1, "smart", `{"align":30,"order":"shuffle","keepEpisodeOrder":true,"noRepeatHours":48,"source":{"type":"rules","rules":{"match":"all","rules":[{"field":"genre","op":"is","values":["`+genre("Komödie")+`"]}]}}}`)
	mk(tr("80er Kult", "80s Classics"), 2, "smart", `{"align":30,"order":"shuffle","logoStyle":"retro","source":{"type":"rules","rules":{"match":"all","rules":[{"field":"decade","op":"is","values":["1980"]}]}}}`)
	mk("Harry Potter Marathon", 3, "manual", `{"align":30,"order":"fixed","logoStyle":"cinema","source":{"type":"manual","items":[{"key":"13","type":"movie","title":"`+hp(12)+`","subtitle":"2001"},{"key":"14","type":"movie","title":"`+hp(13)+`","subtitle":"2002"},{"key":"15","type":"movie","title":"`+hp(14)+`","subtitle":"2004"}]}}`)
	mk(tr("Grusel TV", "Fright Night"), 4, "smart", `{"align":30,"order":"shuffle","logoStyle":"horror","source":{"type":"rules","rules":{"match":"any","rules":[{"field":"genre","op":"is","values":["Horror","Mystery"]}]}}}`)
	// a channel following the seasons (Christmas, Halloween …), all films the rest of the year
	seasonal := schedule.SeasonalPreset(srv.lang(ctx))
	seasonal.Fallback = &schedule.Source{Type: "rules"}
	seasonal.LogoHoliday, seasonal.Align = true, 30
	raw, _ := json.Marshal(seasonal)
	mk(tr("Jahreszeiten", "Seasons"), 5, "seasonal", string(raw))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(sessionCookie); err != nil && !strings.HasPrefix(r.URL.Path, "/logos") {
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "demo", Path: "/"})
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "demo"})
		}
		srv.Handler().ServeHTTP(w, r)
	})
	t.Log("demo running on http://localhost:18090")
	if err := http.ListenAndServe("127.0.0.1:18090", handler); err != nil {
		t.Fatal(err)
	}
}
