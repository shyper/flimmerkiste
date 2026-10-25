// Package tmdb reads upcoming movies and new shows from The Movie Database
// for the "coming soon" slates: titles, dates, ratings and artwork, no video.
//
// This product uses the TMDB API but is not endorsed or certified by TMDB.
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiBase   = "https://api.themoviedb.org/3"
	imageBase = "https://image.tmdb.org/t/p/"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

// Client reads the TMDb API with a key: a v3 API key or a v4 read access token.
type Client struct {
	Key  string
	Base string // API base URL (tests); empty = TMDb
}

// StatusError is an HTTP error answer.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("TMDb: HTTP %d", e.Code) }

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	base := c.Base
	if base == "" {
		base = apiBase
	}
	if q == nil {
		q = url.Values{}
	}
	bearer := len(c.Key) > 40 // v4 tokens are long JWTs, v3 keys 32 hex characters
	if !bearer {
		q.Set("api_key", c.Key)
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
		if err != nil {
			return fmt.Errorf("TMDb: %w", unwrapURL(err)) // the URL carries the key
		}
		if bearer {
			req.Header.Set("Authorization", "Bearer "+c.Key)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("TMDb: %w", unwrapURL(err))
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			// rate limited: wait as asked (a few seconds at most), then try again
			wait := 2 * time.Second
			if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 {
				wait = min(time.Duration(n)*time.Second, 10*time.Second)
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			return &StatusError{Code: resp.StatusCode}
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
}

// userAgent names the app, as TMDb asks of API clients.
const userAgent = "Flimmerkiste (+https://github.com/shyper/flimmerkiste)"

// unwrapURL drops the URL (with the key) from transport errors.
func unwrapURL(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

// Movie is an upcoming movie with what a slate shows.
type Movie struct {
	ID            int
	Title         string
	Overview      string
	ReleaseDate   string // theatrical release in the region, YYYY-MM-DD
	Year          int
	Genres        []string
	RuntimeMin    int
	Certification string // e.g. "12" (DE), "PG-13" (US)
	Poster        string // absolute URLs
	Backdrop      string
}

// Upcoming lists movies coming to theaters in a region (ISO 3166-1, e.g. DE).
func (c *Client) Upcoming(ctx context.Context, region, language string, pages int) ([]Movie, error) {
	var out []Movie
	for page := 1; page <= pages; page++ {
		var r struct {
			TotalPages int `json:"total_pages"`
			Results    []struct {
				ID          int    `json:"id"`
				Title       string `json:"title"`
				Overview    string `json:"overview"`
				ReleaseDate string `json:"release_date"`
				Poster      string `json:"poster_path"`
				Backdrop    string `json:"backdrop_path"`
			} `json:"results"`
		}
		q := url.Values{"region": {region}, "language": {language}, "page": {strconv.Itoa(page)}}
		if err := c.get(ctx, "/movie/upcoming", q, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Results {
			out = append(out, Movie{ID: m.ID, Title: m.Title, Overview: m.Overview, ReleaseDate: m.ReleaseDate,
				Year: year(m.ReleaseDate), Poster: image("w500", m.Poster), Backdrop: image("w1280", m.Backdrop)})
		}
		if page >= r.TotalPages {
			break
		}
	}
	return out, nil
}

// Details fills genres, running time and the regional release date and rating.
func (c *Client) Details(ctx context.Context, m *Movie, region, language string) error {
	var r struct {
		Title    string `json:"title"`
		Overview string `json:"overview"`
		Runtime  int    `json:"runtime"`
		Genres   []struct {
			Name string `json:"name"`
		} `json:"genres"`
		ReleaseDates struct {
			Results []struct {
				Country string `json:"iso_3166_1"`
				Dates   []struct {
					Certification string `json:"certification"`
					ReleaseDate   string `json:"release_date"`
					Type          int    `json:"type"` // 2 limited, 3 theatrical
				} `json:"release_dates"`
			} `json:"results"`
		} `json:"release_dates"`
	}
	q := url.Values{"language": {language}, "append_to_response": {"release_dates"}}
	if err := c.get(ctx, "/movie/"+strconv.Itoa(m.ID), q, &r); err != nil {
		return err
	}
	if r.Overview != "" {
		m.Overview = r.Overview
	}
	m.RuntimeMin = r.Runtime
	m.Genres = m.Genres[:0]
	for _, g := range r.Genres {
		m.Genres = append(m.Genres, g.Name)
	}
	for _, c := range r.ReleaseDates.Results {
		if !strings.EqualFold(c.Country, region) {
			continue
		}
		for _, d := range c.Dates {
			if d.Certification != "" && m.Certification == "" {
				m.Certification = d.Certification
			}
			if (d.Type == 3 || d.Type == 2) && len(d.ReleaseDate) >= 10 {
				m.ReleaseDate = d.ReleaseDate[:10]
			}
		}
	}
	m.Year = year(m.ReleaseDate)
	return nil
}

// Show is a show whose first season is about to start.
type Show struct {
	ID            int
	Name          string
	Overview      string
	FirstAirDate  string // YYYY-MM-DD
	Year          int
	Genres        []string
	Certification string // e.g. "12" (DE), "TV-MA" (US)
	Poster        string // absolute URLs
	Backdrop      string
}

// UpcomingShows lists the most popular shows starting within days, newest
// season starts of running shows aside.
func (c *Client) UpcomingShows(ctx context.Context, language string, days, pages int) ([]Show, error) {
	now := time.Now()
	var out []Show
	for page := 1; page <= pages; page++ {
		var r struct {
			TotalPages int `json:"total_pages"`
			Results    []struct {
				ID           int    `json:"id"`
				Name         string `json:"name"`
				Overview     string `json:"overview"`
				FirstAirDate string `json:"first_air_date"`
				Poster       string `json:"poster_path"`
				Backdrop     string `json:"backdrop_path"`
			} `json:"results"`
		}
		q := url.Values{"language": {language}, "page": {strconv.Itoa(page)}, "sort_by": {"popularity.desc"},
			"first_air_date.gte": {now.Format("2006-01-02")}, "first_air_date.lte": {now.AddDate(0, 0, days).Format("2006-01-02")}}
		if err := c.get(ctx, "/discover/tv", q, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Results {
			out = append(out, Show{ID: m.ID, Name: m.Name, Overview: m.Overview, FirstAirDate: m.FirstAirDate,
				Year: year(m.FirstAirDate), Poster: image("w500", m.Poster), Backdrop: image("w1280", m.Backdrop)})
		}
		if page >= r.TotalPages {
			break
		}
	}
	return out, nil
}

// TV is what a slate needs of a show: its details, the rating of a region
// and the next episode to air.
type TV struct {
	Name          string
	Overview      string
	Genres        []string
	Certification string
	Poster        string // absolute URLs
	Backdrop      string
	// NextSeason, NextAirDate: the season and air date of the next episode,
	// NextEpisode its number (0 = none announced).
	NextSeason, NextEpisode int
	NextAirDate             string
	// Seasons by number: their description and poster, if any.
	Seasons map[int]Season
}

// Season is one season of a show.
type Season struct {
	Overview string
	Poster   string
}

// TV reads a show.
func (c *Client) TV(ctx context.Context, id int, region, language string) (*TV, error) {
	var r struct {
		Name     string `json:"name"`
		Overview string `json:"overview"`
		Poster   string `json:"poster_path"`
		Backdrop string `json:"backdrop_path"`
		Genres   []struct {
			Name string `json:"name"`
		} `json:"genres"`
		Next *struct {
			AirDate string `json:"air_date"`
			Season  int    `json:"season_number"`
			Episode int    `json:"episode_number"`
		} `json:"next_episode_to_air"`
		Seasons []struct {
			Number   int    `json:"season_number"`
			Overview string `json:"overview"`
			Poster   string `json:"poster_path"`
		} `json:"seasons"`
		ContentRatings struct {
			Results []struct {
				Country string `json:"iso_3166_1"`
				Rating  string `json:"rating"`
			} `json:"results"`
		} `json:"content_ratings"`
	}
	q := url.Values{"language": {language}, "append_to_response": {"content_ratings"}}
	if err := c.get(ctx, "/tv/"+strconv.Itoa(id), q, &r); err != nil {
		return nil, err
	}
	tv := &TV{Name: r.Name, Overview: r.Overview, Poster: image("w500", r.Poster), Backdrop: image("w1280", r.Backdrop),
		Seasons: map[int]Season{}}
	for _, g := range r.Genres {
		tv.Genres = append(tv.Genres, g.Name)
	}
	for _, cr := range r.ContentRatings.Results {
		if strings.EqualFold(cr.Country, region) && cr.Rating != "" {
			tv.Certification = cr.Rating
		}
	}
	if r.Next != nil && len(r.Next.AirDate) >= 10 {
		tv.NextSeason, tv.NextEpisode, tv.NextAirDate = r.Next.Season, r.Next.Episode, r.Next.AirDate[:10]
	}
	for _, s := range r.Seasons {
		tv.Seasons[s.Number] = Season{Overview: s.Overview, Poster: image("w500", s.Poster)}
	}
	return tv, nil
}

// ShowDetails fills genres and the rating of a region.
func (c *Client) ShowDetails(ctx context.Context, s *Show, region, language string) error {
	tv, err := c.TV(ctx, s.ID, region, language)
	if err != nil {
		return err
	}
	if tv.Overview != "" {
		s.Overview = tv.Overview
	}
	s.Genres, s.Certification = tv.Genres, tv.Certification
	return nil
}

func image(size, path string) string {
	if path == "" {
		return ""
	}
	return imageBase + size + path
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}
