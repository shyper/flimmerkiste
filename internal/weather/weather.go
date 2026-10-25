// Package weather reads current weather and forecasts from Open-Meteo
// (https://open-meteo.com), which combines the models of national weather
// services (for Germany the ICON model of the Deutscher Wetterdienst), and
// looks places up by name through its geocoding API. No key is needed; the
// free service is for non-commercial use and asks to be credited.
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	forecastBase = "https://api.open-meteo.com/v1"
	geocodeBase  = "https://geocoding-api.open-meteo.com/v1"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Client reads Open-Meteo; the bases can point elsewhere in tests.
type Client struct {
	Base        string // forecast API; empty = api.open-meteo.com
	GeocodeBase string // geocoding API; empty = geocoding-api.open-meteo.com
}

// StatusError is an HTTP error answer.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("weather: HTTP %d", e.Code) }

func get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &StatusError{Code: resp.StatusCode}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Place is a location to show the weather for.
type Place struct {
	Name    string  `json:"name"`
	Region  string  `json:"region,omitempty"` // e.g. "Nordrhein-Westfalen, Deutschland"
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Country string  `json:"country,omitempty"` // ISO code
}

// Search looks places up by name, in the given language.
func (c *Client) Search(ctx context.Context, name, lang string) ([]Place, error) {
	q := url.Values{"name": {name}, "count": {"8"}, "language": {lang}, "format": {"json"}}
	var r struct {
		Results []struct {
			Name        string  `json:"name"`
			Latitude    float64 `json:"latitude"`
			Longitude   float64 `json:"longitude"`
			CountryCode string  `json:"country_code"`
			Country     string  `json:"country"`
			Admin1      string  `json:"admin1"`
		} `json:"results"`
	}
	if err := get(ctx, or(c.GeocodeBase, geocodeBase)+"/search?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	var out []Place
	for _, p := range r.Results {
		region := p.Country
		if p.Admin1 != "" && p.Admin1 != p.Name {
			region = p.Admin1 + ", " + p.Country
		}
		out = append(out, Place{Name: p.Name, Region: region, Lat: p.Latitude, Lon: p.Longitude, Country: p.CountryCode})
	}
	return out, nil
}

// Hour is one hour of observations or forecast.
type Hour struct {
	Time        time.Time `json:"time"`
	Temperature float64   `json:"temperature"` // °C
	Icon        string    `json:"icon"`        // clear-day, partly-cloudy-night, rain, snow …
	RainChance  int       `json:"rainChance"`  // %, -1 = unknown
	Wind        float64   `json:"wind"`        // km/h
}

// Report is what the slates show: now and the coming hours and days.
type Report struct {
	Fetched time.Time `json:"fetched"`
	Now     Hour      `json:"now"`
	Hours   []Hour    `json:"hours"` // hourly forecast from the current hour on
}

// Load reads the current weather and the hourly forecast for the next days at p.
func (c *Client) Load(ctx context.Context, p Place, days int, now time.Time) (*Report, error) {
	tz := now.Location().String()
	if tz == "Local" || tz == "" || tz != "UTC" && !strings.Contains(tz, "/") {
		tz = "auto" // Open-Meteo knows IANA names only, not fixed zones like "UTC+2"
	}
	q := url.Values{
		"latitude":      {strconv.FormatFloat(p.Lat, 'f', 4, 64)},
		"longitude":     {strconv.FormatFloat(p.Lon, 'f', 4, 64)},
		"current":       {"temperature_2m,weather_code,wind_speed_10m,is_day"},
		"hourly":        {"temperature_2m,weather_code,precipitation_probability,wind_speed_10m,is_day"},
		"timezone":      {tz},
		"forecast_days": {strconv.Itoa(min(days+2, 16))},
	}
	var r struct {
		Timezone  string `json:"timezone"`
		UTCOffset int    `json:"utc_offset_seconds"`
		Current   struct {
			Time  string  `json:"time"`
			Temp  float64 `json:"temperature_2m"`
			Code  int     `json:"weather_code"`
			Wind  float64 `json:"wind_speed_10m"`
			IsDay int     `json:"is_day"`
		} `json:"current"`
		Hourly struct {
			Time   []string   `json:"time"`
			Temp   []*float64 `json:"temperature_2m"`
			Code   []*int     `json:"weather_code"`
			Precip []*int     `json:"precipitation_probability"`
			Wind   []*float64 `json:"wind_speed_10m"`
			IsDay  []int      `json:"is_day"`
		} `json:"hourly"`
	}
	if err := get(ctx, or(c.Base, forecastBase)+"/forecast?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	// times come as local times of the answer's time zone, without offset
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil || r.Timezone == "" {
		loc = time.FixedZone(r.Timezone, r.UTCOffset)
	}
	parse := func(v string) (time.Time, bool) {
		t, err := time.ParseInLocation("2006-01-02T15:04", v, loc)
		return t, err == nil
	}
	rep := &Report{Fetched: now}
	if t, ok := parse(r.Current.Time); ok {
		rep.Now = Hour{Time: t, Temperature: r.Current.Temp, Icon: Icon(r.Current.Code, r.Current.IsDay == 1, r.Current.Wind),
			Wind: r.Current.Wind, RainChance: -1}
	}
	hourStart := now.Truncate(time.Hour)
	h := r.Hourly
	for i, v := range h.Time {
		t, ok := parse(v)
		if !ok || i >= len(h.Temp) || h.Temp[i] == nil || t.Before(hourStart) {
			continue
		}
		hr := Hour{Time: t, Temperature: *h.Temp[i], RainChance: -1}
		if i < len(h.Wind) && h.Wind[i] != nil {
			hr.Wind = *h.Wind[i]
		}
		if i < len(h.Code) && h.Code[i] != nil {
			hr.Icon = Icon(*h.Code[i], i < len(h.IsDay) && h.IsDay[i] == 1, hr.Wind)
		}
		if i < len(h.Precip) && h.Precip[i] != nil {
			hr.RainChance = *h.Precip[i]
		}
		rep.Hours = append(rep.Hours, hr)
	}
	if rep.Now.Time.IsZero() {
		return nil, fmt.Errorf("weather: no current weather in the answer")
	}
	return rep, nil
}

// windy: from this wind speed (km/h) on, fair weather shows the wind icon.
const windy = 50

// Icon names the icon of a WMO weather code (as Open-Meteo gives it): clear-day,
// partly-cloudy-night, cloudy, fog, wind, rain, sleet, snow, thunderstorm.
func Icon(code int, day bool, wind float64) string {
	sky := func(clear, partly string) string {
		if day {
			return clear + "-day"
		}
		return partly + "-night"
	}
	switch {
	case code <= 3 && wind >= windy:
		return "wind"
	case code == 0:
		return sky("clear", "clear")
	case code == 1, code == 2:
		return sky("partly-cloudy", "partly-cloudy")
	case code == 3:
		return "cloudy"
	case code == 45, code == 48:
		return "fog"
	case code == 56, code == 57, code == 66, code == 67:
		return "sleet" // freezing drizzle and rain
	case code >= 51 && code <= 65, code >= 80 && code <= 82:
		return "rain"
	case code >= 71 && code <= 77, code == 85, code == 86:
		return "snow"
	case code >= 95: // also with hail: the thunder is what counts
		return "thunderstorm"
	}
	return "cloudy"
}

// Day sums up the forecast of one day.
type Day struct {
	Date       time.Time
	Min, Max   float64
	Icon       string // the most telling icon of the daytime
	RainChance int    // highest chance, -1 = unknown
}

// severity orders icons: the worse weather of a day is what people want to know.
// Evenings of winter days fall into the daytime window, hence the night icons.
var severity = map[string]int{"clear-day": 1, "clear-night": 1, "partly-cloudy-day": 2, "partly-cloudy-night": 2,
	"cloudy": 3, "wind": 4, "fog": 4, "rain": 5, "sleet": 6, "snow": 6, "thunderstorm": 8}

// Days sums up the forecast per calendar day, starting with the day of from.
func (r *Report) Days(from time.Time, n int) []Day {
	var out []Day
	y, m, d := from.Date()
	for i := 0; i < n; i++ {
		start := time.Date(y, m, d+i, 0, 0, 0, 0, from.Location())
		end := start.AddDate(0, 0, 1)
		day := Day{Date: start, RainChance: -1}
		count := map[string]int{}
		seen := false
		for _, h := range r.Hours {
			if h.Time.Before(start) || !h.Time.Before(end) {
				continue
			}
			if !seen || h.Temperature < day.Min {
				day.Min = h.Temperature
			}
			if !seen || h.Temperature > day.Max {
				day.Max = h.Temperature
			}
			seen = true
			day.RainChance = max(day.RainChance, h.RainChance)
			if hr := h.Time.In(from.Location()).Hour(); hr >= 8 && hr <= 20 {
				count[h.Icon]++
			}
		}
		if !seen {
			break
		}
		// an icon counts once it covers a few daytime hours; the worst of those wins
		icons := slices.Sorted(maps.Keys(count))
		best := ""
		for _, icon := range icons {
			if count[icon] >= 3 && (best == "" || severity[icon] > severity[best]) {
				best = icon
			}
		}
		if best == "" {
			for _, icon := range icons {
				if best == "" || count[icon] > count[best] {
					best = icon
				}
			}
		}
		day.Icon = best
		out = append(out, day)
	}
	return out
}

// At returns the forecast hour at t (the first one after, if t is between).
func (r *Report) At(t time.Time) (Hour, bool) {
	for _, h := range r.Hours {
		if !h.Time.Before(t.Truncate(time.Hour)) {
			return h, true
		}
	}
	return Hour{}, false
}
