package library

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

const (
	// recheckExtras: new trailers keep appearing for a while after a release.
	recheckExtras = 30 * 24 * time.Hour
	// maxTrailersPerTitle keeps big franchises from dominating the pool.
	maxTrailersPerTitle = 5
	extrasWorkers       = 4
)

// StartTrailerSync looks up missing trailers in the background, e.g. after an
// update when the library itself is not due for a sync yet.
func (l *Library) StartTrailerSync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		if err := l.syncTrailers(ctx); err != nil && !errors.Is(err, ErrNotLinked) {
			l.log.Warn("trailer sync", "err", err)
		}
	}()
}

// syncTrailers looks up the extras of library movies that were not checked
// recently and stores their trailers. Plex streams them on demand from its
// online provider, so only metadata is kept.
func (l *Library) syncTrailers(ctx context.Context) error {
	l.mu.Lock()
	if l.trailersRunning {
		l.mu.Unlock()
		return nil
	}
	l.trailersRunning = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.trailersRunning = false
		l.mu.Unlock()
	}()

	srv, err := l.server(ctx)
	if err != nil {
		return err
	}
	movies, err := l.store.MoviesWithoutExtras(ctx, time.Now().Add(-recheckExtras))
	if err != nil || len(movies) == 0 {
		return err
	}
	var done, found atomic.Int64
	jobs := make(chan *store.Media)
	var wg sync.WaitGroup
	for range extrasWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				extras, err := srv.Extras(ctx, m.RatingKey)
				if err != nil && !errors.Is(err, mediaserver.ErrNotFound) {
					l.log.Debug("extras", "title", m.Title, "err", err)
					continue // try again next sync
				}
				ts := libraryTrailers(m, extras)
				if err := l.store.SetLibraryTrailers(ctx, m.RatingKey, ts, time.Now()); err != nil {
					l.log.Warn("store trailers", "title", m.Title, "err", err)
					continue
				}
				found.Add(int64(len(ts)))
				if n := done.Add(1); n%25 == 0 {
					l.setTrailerStatus(i18n.T(l.lang(ctx), "library.trailersProgress", n, len(movies)))
				}
			}
		}()
	}
feed:
	for _, m := range movies {
		select {
		case jobs <- m:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	l.setTrailerStatus(i18n.T(l.lang(ctx), "library.trailersDone", done.Load(), found.Load()))
	l.log.Info("library trailers synced", "movies", done.Load(), "trailers", found.Load())
	return ctx.Err()
}

func (l *Library) setTrailerStatus(msg string) {
	l.mu.Lock()
	l.status.Trailers = msg
	l.mu.Unlock()
}

// libraryTrailers converts the extras of a movie into trailers.
func libraryTrailers(m *store.Media, extras []mediaserver.Item) []*store.Trailer {
	var out []*store.Trailer
	for i := range extras {
		t := TrailerFrom(&extras[i])
		if t == nil {
			continue
		}
		t.Origin, t.MediaKey, t.GUID = store.OriginLibrary, m.RatingKey, m.GUID
		t.Title, t.Kind, t.Year, t.Released = m.Title, "movie", m.Year, m.Released
		t.Genres, t.ContentRating, t.Summary, t.Thumb = m.Tags.Genre, m.ContentRating, m.Summary, m.Thumb
		out = append(out, t)
		if len(out) == maxTrailersPerTitle {
			break
		}
	}
	return out
}

// TrailerFrom converts an extra into a trailer (nil for other extras or
// unplayable entries). The advertised title still has to be filled in.
func TrailerFrom(e *mediaserver.Item) *store.Trailer {
	if e.Subtype != "trailer" || len(e.Media) == 0 {
		return nil
	}
	t := &store.Trailer{Key: store.TrailerPrefix + e.ID, Name: e.Title, DurationMs: e.Duration}
	for _, mi := range e.Media {
		if len(mi.Parts) == 0 {
			continue
		}
		// Discover extras carry a direct file URL; library extras a path on the Plex server.
		u := mi.URL
		if u == "" {
			u = mi.Parts[0].Key
		}
		h := mi.Height
		if h == 0 {
			h, _ = strconv.Atoi(strings.TrimSuffix(mi.VideoResolution, "p"))
		}
		t.Parts = append(t.Parts, store.TrailerPart{Height: h, URL: u})
		if t.DurationMs == 0 {
			t.DurationMs = mi.Duration
		}
		if t.Language == "" {
			for _, st := range mi.Parts[0].Streams {
				if st.StreamType == 2 && st.LanguageCode != "" {
					t.Language = st.LanguageCode
				}
			}
		}
	}
	if len(t.Parts) == 0 || t.DurationMs <= 0 {
		return nil
	}
	return t
}
