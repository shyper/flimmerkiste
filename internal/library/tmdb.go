package library

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/tmdb"
)

const (
	// tmdbPages: upcoming movies read per refresh (20 per page).
	tmdbPages = 2
	// tmdbShowPages, tmdbShowDays: the most popular new shows starting
	// within tmdbShowDays.
	tmdbShowPages = 1
	tmdbShowDays  = 90
)

// SyncTMDb stores "coming to theaters" slates for upcoming movies of a region
// and "new show" slates for shows about to start (TMDb): no video, the slate
// itself is the filler. Titles already in the library and those released are
// left out; slates of titles that left the lists expire after staleDiscover.
func (l *Library) SyncTMDb(ctx context.Context, c *tmdb.Client, region, language string, slate time.Duration) (DiscoverResult, error) {
	var res DiscoverResult
	movies, err := c.Upcoming(ctx, region, language, tmdbPages)
	if err != nil {
		return res, i18n.E("tmdb.load", err)
	}
	owned, ownedShows := map[string]bool{}, map[string]bool{}
	if all, err := l.All(ctx); err == nil {
		for _, m := range all {
			if id := m.Ext["tmdb"]; id != "" && m.Type == "movie" {
				owned[id] = true
			}
			if id := m.Ext["show.tmdb"]; id != "" {
				ownedShows[id] = true
			}
		}
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	var found []*store.Trailer
	for i := range movies {
		m := &movies[i]
		if owned[strconv.Itoa(m.ID)] {
			continue
		}
		if err := c.Details(ctx, m, region, language); err != nil {
			l.log.Debug("tmdb details", "title", m.Title, "err", err)
			continue
		}
		if m.ReleaseDate == "" || m.ReleaseDate < today || m.Backdrop == "" {
			continue
		}
		res.Titles++
		rating := m.Certification
		if rating != "" && region == "DE" {
			rating = "de/" + rating
		}
		found = append(found, &store.Trailer{
			Key: store.TrailerPrefix + "tmdb-" + strconv.Itoa(m.ID), Origin: store.OriginTMDb, GUID: fmt.Sprintf("tmdb://%d", m.ID),
			Title: m.Title, Kind: "movie", Year: m.Year, Released: m.ReleaseDate, Genres: m.Genres, ContentRating: rating,
			Summary: m.Overview, Thumb: m.Poster, Art: m.Backdrop, Name: m.Title, DurationMs: slate.Milliseconds(),
			RuntimeMin: m.RuntimeMin, LastSeen: now.Unix(),
		})
	}
	found = append(found, l.tmdbShows(ctx, c, region, language, slate, ownedShows, &res)...)
	if err := l.store.UpsertTrailers(ctx, found); err != nil {
		return res, err
	}
	res.Trailers = len(found)
	res.Removed, err = l.store.DeleteStaleTrailers(ctx, store.OriginTMDb, now.Add(-staleDiscover))
	l.log.Info("tmdb slates synced", "titles", res.Titles, "removed", res.Removed)
	return res, err
}

// tmdbShows returns slates of popular shows about to start. Errors only cost
// these slates, not those of the movies.
func (l *Library) tmdbShows(ctx context.Context, c *tmdb.Client, region, language string, slate time.Duration, owned map[string]bool, res *DiscoverResult) []*store.Trailer {
	shows, err := c.UpcomingShows(ctx, language, tmdbShowDays, tmdbShowPages)
	if err != nil {
		l.log.Warn("tmdb shows", "err", err)
		return nil
	}
	now := time.Now()
	var out []*store.Trailer
	for i := range shows {
		sh := &shows[i]
		// without a description in the language, the show is hardly aired here
		if owned[strconv.Itoa(sh.ID)] || sh.Overview == "" || sh.Backdrop == "" || sh.FirstAirDate == "" {
			continue
		}
		if err := c.ShowDetails(ctx, sh, region, language); err != nil {
			l.log.Debug("tmdb show details", "title", sh.Name, "err", err)
			continue
		}
		res.Titles++
		rating := sh.Certification
		if rating != "" && region == "DE" {
			rating = "de/" + rating
		}
		out = append(out, &store.Trailer{
			Key: store.TrailerPrefix + "tmdb-tv-" + strconv.Itoa(sh.ID), Origin: store.OriginTMDb, GUID: fmt.Sprintf("tmdb://tv/%d", sh.ID),
			Title: sh.Name, Kind: "show", Year: sh.Year, Released: sh.FirstAirDate, Genres: sh.Genres, ContentRating: rating,
			Summary: sh.Overview, Thumb: sh.Poster, Art: sh.Backdrop, Name: sh.Name, DurationMs: slate.Milliseconds(),
			LastSeen: now.Unix(),
		})
	}
	return out
}
