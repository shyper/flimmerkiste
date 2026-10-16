package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/weather"
)

func TestInfoSlates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"name":"Hamburg","latitude":53.55073,"longitude":9.99302,"country_code":"DE","country":"Deutschland","admin1":"Hamburg"}]}`))
	})
	mux.HandleFunc("GET /forecast", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now().Truncate(time.Hour)
		var times []string
		var temps []float64
		var codes, precip []int
		for i := 0; i < 96; i++ {
			times = append(times, start.Add(time.Duration(i)*time.Hour).Format("2006-01-02T15:04"))
			temps, codes, precip = append(temps, 2), append(codes, 3), append(precip, 40)
		}
		json.NewEncoder(w).Encode(map[string]any{"timezone": time.Local.String(),
			"current": map[string]any{"time": start.Format("2006-01-02T15:04"), "temperature_2m": 3.6, "weather_code": 73, "wind_speed_10m": 20, "is_day": 1},
			"hourly":  map[string]any{"time": times, "temperature_2m": temps, "weather_code": codes, "precipitation_probability": precip}})
	})
	mux.HandleFunc("GET /feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss version="2.0"><channel><title>Testnachrichten - alles neu</title><item><title>Eine Meldung</title><pubDate>` +
			time.Now().Add(-time.Hour).Format(time.RFC1123Z) + `</pubDate></item></channel></rss>`))
	})
	api := httptest.NewServer(mux)
	defer api.Close()
	old := weatherClient
	weatherClient = &weather.Client{Base: api.URL, GeocodeBase: api.URL}
	defer func() { weatherClient = old }()

	srv, h := transferWorld(t, "a")
	ctx := context.Background()
	srv.store.SetSetting(ctx, store.SetMediaServer, "jellyfin")

	var places []weather.Place
	if code := call(t, h, "GET", "/api/info/places?q=Hamburg", nil, &places); code != http.StatusOK || len(places) != 1 {
		t.Fatalf("places: %d %+v", code, places)
	}
	var settings map[string]any
	call(t, h, "PUT", "/api/settings", map[string]any{"weatherPlace": places[0], "newsFeeds": api.URL + "/feed.xml\nnot a url\n"}, &settings)
	if settings["newsFeeds"] != api.URL+"/feed.xml" || settings["weatherPlace"].(map[string]any)["name"] != "Hamburg" {
		t.Fatalf("settings: %v %v", settings["newsFeeds"], settings["weatherPlace"])
	}

	c, _ := srv.store.GetChannelByNumber(ctx, 1)
	if c == nil {
		call(t, h, "POST", "/api/channels", map[string]any{"name": "Wetter", "number": 1, "kind": "smart",
			"config": map[string]any{"source": map[string]any{"type": "rules"}}}, nil)
		c, _ = srv.store.GetChannelByNumber(ctx, 1)
	}
	render := func(note string) string {
		prog := &store.Program{ChannelID: c.ID, RatingKey: store.PromoKey, Note: note, Start: time.Now().UnixMilli(), Stop: time.Now().Add(20 * time.Second).UnixMilli()}
		path, err := srv.renderCard(ctx, c, prog, 360)
		if err != nil {
			t.Fatal(err)
		}
		if st, err := os.Stat(path); err != nil || st.Size() < 1000 {
			t.Fatalf("%s: %v", note, err)
		}
		return path
	}
	// the first slate fetches what is missing
	wx := render(store.InfoPrefix + "weather-now")
	render(store.InfoPrefix + "weather-days")
	render(store.InfoPrefix + "news-1")
	if o, ok := srv.weatherOptions(ctx, c, &store.Program{Note: store.InfoPrefix + "weather-now", Start: time.Now().UnixMilli()}, 720); !ok ||
		o.Now.Label != "Schnee" || o.Now.Temp != "4°" || o.NowDetail != "Wind 20 km/h" || len(o.Slots) != 4 {
		t.Errorf("weather: %+v", o)
	}
	if o, ok := srv.newsOptions(ctx, c, &store.Program{Note: store.InfoPrefix + "news-1"}, 720); !ok || len(o.Items) != 1 ||
		!strings.HasPrefix(o.Items[0].Meta, "Testnachrichten · ") || o.Items[0].Title != "Eine Meldung" {
		t.Errorf("news: %+v", o)
	}
	var status struct {
		Info struct {
			Weather struct{ Last int64 }
			News    struct{ Count int }
		}
	}
	call(t, h, "GET", "/api/trailers", nil, &status)
	if status.Info.Weather.Last == 0 || status.Info.News.Count != 1 {
		t.Errorf("status: %+v", status)
	}

	// switched off: the "up next" slate stands in, under another cache key
	call(t, h, "PUT", "/api/settings", map[string]any{"weatherPlace": nil}, nil)
	if p := render(store.InfoPrefix + "weather-now"); p == wx {
		t.Error("cached weather slate after switching off")
	}
}
