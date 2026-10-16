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

// tmdbPages: upcoming movies read per refresh (20 per page).
const tmdbPages = 2

// SyncTMDb stores "coming to theaters" slates for upcoming movies of a region
// (TMDb): no video, the slate itself is the filler. Movies already in the
// library and those released are left out; slates of movies that left the
// list expire after staleDiscover.
func (l *Library) SyncTMDb(ctx context.Context, c *tmdb.Client, region, language string, slate time.Duration) (DiscoverResult, error) {
	var res DiscoverResult
	movies, err := c.Upcoming(ctx, region, language, tmdbPages)
	if err != nil {
		return res, i18n.E("tmdb.load", err)
	}
	owned := map[string]bool{}
	if all, err := l.All(ctx); err == nil {
		for _, m := range all {
			if id := m.Ext["tmdb"]; id != "" {
				owned[id] = true
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
	if err := l.store.UpsertTrailers(ctx, found); err != nil {
		return res, err
	}
	res.Trailers = len(found)
	res.Removed, err = l.store.DeleteStaleTrailers(ctx, store.OriginTMDb, now.Add(-staleDiscover))
	l.log.Info("tmdb slates synced", "titles", res.Titles, "removed", res.Removed)
	return res, err
}
