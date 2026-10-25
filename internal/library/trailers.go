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

// extrasJob is a movie or show whose extras are looked up.
type extrasJob struct {
	id, title string
	trailers  func([]mediaserver.Item) []*store.Trailer
	save      func(context.Context, []*store.Trailer) error
}

// syncTrailers looks up the extras of library movies and shows that were not
// checked recently and stores their trailers. Plex streams them on demand
// from its online provider, so only metadata is kept.
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
	jobs, err := l.extrasJobs(ctx)
	if err != nil || len(jobs) == 0 {
		return err
	}
	var done, found atomic.Int64
	ch := make(chan extrasJob)
	var wg sync.WaitGroup
	for range extrasWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				extras, err := srv.Extras(ctx, j.id)
				if err != nil && !errors.Is(err, mediaserver.ErrNotFound) {
					l.log.Debug("extras", "title", j.title, "err", err)
					continue // try again next sync
				}
				ts := j.trailers(extras)
				if err := j.save(ctx, ts); err != nil {
					l.log.Warn("store trailers", "title", j.title, "err", err)
					continue
				}
				found.Add(int64(len(ts)))
				if n := done.Add(1); n%25 == 0 {
					l.setTrailerStatus(i18n.T(l.lang(ctx), "library.trailersProgress", n, len(jobs)))
				}
			}
		}()
	}
feed:
	for _, j := range jobs {
		select {
		case ch <- j:
		case <-ctx.Done():
			break feed
		}
	}
	close(ch)
	wg.Wait()
	l.setTrailerStatus(i18n.T(l.lang(ctx), "library.trailersDone", done.Load(), found.Load()))
	l.log.Info("library trailers synced", "titles", done.Load(), "trailers", found.Load())
	return ctx.Err()
}

// extrasJobs lists the movies and shows whose extras are due.
func (l *Library) extrasJobs(ctx context.Context) ([]extrasJob, error) {
	before := time.Now().Add(-recheckExtras)
	movies, err := l.store.MoviesWithoutExtras(ctx, before)
	if err != nil {
		return nil, err
	}
	var jobs []extrasJob
	for _, m := range movies {
		jobs = append(jobs, extrasJob{id: m.RatingKey, title: m.Title,
			trailers: func(extras []mediaserver.Item) []*store.Trailer { return libraryTrailers(m, extras) },
			save: func(ctx context.Context, ts []*store.Trailer) error {
				return l.store.SetLibraryTrailers(ctx, m.RatingKey, ts, time.Now())
			}})
	}
	shows, err := l.store.ShowsWithoutExtras(ctx, before)
	if err != nil || len(shows) == 0 {
		return jobs, err
	}
	// A show is described by its first episode (specials aside), which carries
	// its title, poster, genres and description.
	all, err := l.All(ctx)
	if err != nil {
		return nil, err
	}
	first := map[string]*store.Media{}
	for _, m := range all {
		if f := first[m.ShowKey]; m.Type == "episode" && (f == nil || f.Season == 0 && m.Season > 0) {
			first[m.ShowKey] = m
		}
	}
	for _, key := range shows {
		ep := first[key]
		if ep == nil {
			continue // no playable episode: nothing to advertise
		}
		jobs = append(jobs, extrasJob{id: key, title: ep.ShowTitle,
			trailers: func(extras []mediaserver.Item) []*store.Trailer { return showTrailers(key, ep, extras) },
			save: func(ctx context.Context, ts []*store.Trailer) error {
				return l.store.SetShowTrailers(ctx, key, ts, time.Now())
			}})
	}
	return jobs, nil
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

// showTrailers converts the extras of a show into trailers; ep is one of its
// episodes.
func showTrailers(showKey string, ep *store.Media, extras []mediaserver.Item) []*store.Trailer {
	var out []*store.Trailer
	for i := range extras {
		t := TrailerFrom(&extras[i])
		if t == nil {
			continue
		}
		t.Origin, t.ShowKey = store.OriginLibrary, showKey
		t.Title, t.Kind, t.Year = ep.ShowTitle, "show", ep.Year
		t.Genres, t.ContentRating, t.Summary, t.Thumb = ep.Tags.Genre, ep.ContentRating, ep.ShowSummary, ep.ShowThumb
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
					t.Language = st.LanguageCode // the first audio track is the trailer's language
					break
				}
			}
		}
	}
	if len(t.Parts) == 0 || t.DurationMs <= 0 {
		return nil
	}
	return t
}
