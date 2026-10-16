package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoadAndDays(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone data")
	}
	now := time.Date(2026, 10, 16, 8, 40, 0, 0, loc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /forecast", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("latitude") != "50.9333" || q.Get("timezone") != "Europe/Berlin" || q.Get("forecast_days") != "4" {
			t.Errorf("query: %s", r.URL.RawQuery)
		}
		var times []string
		var temps []float64
		var codes, precip, isDay []int
		start := time.Date(2026, 10, 16, 0, 0, 0, 0, loc)
		for i := 0; i < 72; i++ {
			ts := start.Add(time.Duration(i) * time.Hour)
			code := 2 // partly cloudy
			if i >= 24 && i < 48 && ts.Hour() >= 12 && ts.Hour() <= 15 {
				code = 63 // a rainy afternoon on day two
			}
			times = append(times, ts.Format("2006-01-02T15:04"))
			temps = append(temps, float64(ts.Hour())-float64(i/24))
			codes = append(codes, code)
			precip = append(precip, ts.Hour())
			day := 0
			if ts.Hour() >= 8 && ts.Hour() < 18 {
				day = 1
			}
			isDay = append(isDay, day)
		}
		json.NewEncoder(w).Encode(map[string]any{"timezone": "Europe/Berlin", "utc_offset_seconds": 7200,
			"current": map[string]any{"time": "2026-10-16T08:30", "temperature_2m": 13.3, "weather_code": 3, "wind_speed_10m": 9.4, "is_day": 1},
			"hourly": map[string]any{"time": times, "temperature_2m": temps, "weather_code": codes, "precipitation_probability": precip,
				"wind_speed_10m": make([]float64, 72), "is_day": isDay}})
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"name":"Köln","latitude":50.93333,"longitude":6.95,"country_code":"DE","country":"Deutschland","admin1":"Nordrhein-Westfalen"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{Base: srv.URL, GeocodeBase: srv.URL}

	ps, err := c.Search(context.Background(), "Köln", "de")
	if err != nil || len(ps) != 1 || ps[0].Region != "Nordrhein-Westfalen, Deutschland" {
		t.Fatalf("search: %+v %v", ps, err)
	}
	rep, err := c.Load(context.Background(), ps[0], 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Now.Temperature != 13.3 || rep.Now.Wind != 9.4 || rep.Now.Icon != "cloudy" || rep.Now.RainChance != -1 ||
		!rep.Now.Time.Equal(time.Date(2026, 10, 16, 8, 30, 0, 0, loc)) {
		t.Errorf("now: %+v", rep.Now)
	}
	if len(rep.Hours) != 72-8 || rep.Hours[0].Time.Hour() != 8 || rep.Hours[0].Icon != "partly-cloudy-day" || rep.Hours[12].Icon != "partly-cloudy-night" {
		t.Errorf("hours from the current one: %d, first %+v", len(rep.Hours), rep.Hours[0])
	}
	if h, ok := rep.At(now.Add(3 * time.Hour)); !ok || h.Time.Hour() != 11 {
		t.Errorf("at: %+v", h)
	}
	days := rep.Days(now.AddDate(0, 0, 1), 3)
	if len(days) != 2 {
		t.Fatalf("days: %+v", days)
	}
	if d := days[0]; d.Icon != "rain" || d.Max != 22 || d.Min != -1 || d.RainChance != 23 {
		t.Errorf("rainy day: %+v", d)
	}
	if days[1].Icon != "partly-cloudy-day" {
		t.Errorf("fair day: %+v", days[1])
	}
}

func TestIcon(t *testing.T) {
	for _, c := range []struct {
		code int
		day  bool
		wind float64
		want string
	}{
		{0, true, 5, "clear-day"}, {0, false, 5, "clear-night"}, {1, true, 5, "partly-cloudy-day"}, {2, false, 5, "partly-cloudy-night"},
		{3, true, 5, "cloudy"}, {45, true, 5, "fog"}, {53, true, 5, "rain"}, {57, true, 5, "sleet"}, {61, false, 5, "rain"},
		{66, true, 5, "sleet"}, {73, true, 5, "snow"}, {77, true, 5, "snow"}, {81, true, 5, "rain"}, {86, true, 5, "snow"},
		{95, true, 5, "thunderstorm"}, {99, true, 5, "thunderstorm"}, {1, true, 60, "wind"}, {61, true, 60, "rain"}, {42, true, 5, "cloudy"},
	} {
		if got := Icon(c.code, c.day, c.wind); got != c.want {
			t.Errorf("code %d day=%v wind=%v: %s, want %s", c.code, c.day, c.wind, got, c.want)
		}
	}
}
