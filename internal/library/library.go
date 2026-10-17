// Package library mirrors Plex library metadata into SQLite and keeps an
// in-memory catalog for fast rule evaluation.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

// ServerFunc returns a client for the currently linked media server.
type ServerFunc func(ctx context.Context) (mediaserver.Server, error)

type Status struct {
	Running  bool   `json:"running"`
	Message  string `json:"message"`
	Error    string `json:"error,omitempty"`
	Finished int64  `json:"finished,omitempty"`
	Added    int    `json:"added"`
	Removed  int    `json:"removed"`
	Trailers string `json:"trailers,omitempty"` // progress of the trailer lookup
}

// Changes lists the rating keys that appeared or disappeared during a sync.
type Changes struct {
	Added   map[string]bool
	Removed map[string]bool
}

func (c *Changes) Empty() bool { return len(c.Added) == 0 && len(c.Removed) == 0 }

type Library struct {
	store  *store.Store
	server ServerFunc
	log    *slog.Logger

	mu      sync.Mutex
	status  Status
	catalog []*store.Media
	byKey   map[string]*store.Media
	loaded  bool

	trailersRunning bool

	onSynced func(Changes)
}

func New(st *store.Store, server ServerFunc, log *slog.Logger) *Library {
	return &Library{store: st, server: server, log: log}
}

// OnSynced registers a callback that runs after a successful sync with the detected changes.
func (l *Library) OnSynced(fn func(Changes)) { l.onSynced = fn }

func (l *Library) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

func (l *Library) setStatus(msg string) {
	l.mu.Lock()
	l.status.Message = msg
	l.mu.Unlock()
}

// All returns the cached catalog (loaded from SQLite on first use).
// Callers must treat the returned items as read-only.
func (l *Library) All(ctx context.Context) ([]*store.Media, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.loaded {
		items, err := l.store.AllMedia(ctx)
		if err != nil {
			return nil, err
		}
		l.catalog = items
		l.byKey = make(map[string]*store.Media, len(items))
		for _, m := range items {
			l.byKey[m.RatingKey] = m
		}
		l.loaded = true
	}
	return l.catalog, nil
}

// Get returns one item from the catalog, or nil.
func (l *Library) Get(ctx context.Context, key string) *store.Media {
	if _, err := l.All(ctx); err != nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byKey[key]
}

// Invalidate drops the in-memory catalog; it is reloaded lazily.
func (l *Library) Invalidate() {
	l.mu.Lock()
	l.catalog, l.byKey, l.loaded = nil, nil, false
	l.mu.Unlock()
}

// StartSync runs a sync in the background unless one is already running.
func (l *Library) StartSync() bool {
	l.mu.Lock()
	if l.status.Running {
		l.mu.Unlock()
		return false
	}
	l.status = Status{Running: true, Message: i18n.T(l.lang(context.Background()), "library.starting"), Trailers: l.status.Trailers}
	l.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		changes := Changes{Added: map[string]bool{}, Removed: map[string]bool{}}
		err := l.sync(ctx, &changes)
		l.mu.Lock()
		l.status.Running = false
		l.status.Finished = time.Now().Unix()
		lang := l.lang(ctx)
		if err != nil {
			l.status.Error = i18n.Message(lang, err)
			l.status.Message = i18n.T(lang, "library.failed")
		} else {
			l.status.Message = i18n.T(lang, "library.done")
			l.status.Error = ""
			l.status.Added, l.status.Removed = len(changes.Added), len(changes.Removed)
		}
		l.mu.Unlock()
		if err != nil {
			l.log.Error("library sync failed", "err", err)
			// Libraries synced before the error are stored already: their
			// changes are not seen again, so they apply now.
			l.Invalidate()
			if l.onSynced != nil && !changes.Empty() {
				l.onSynced(changes)
			}
			return
		}
		_ = l.store.SetSetting(context.Background(), store.SetLastSync, fmt.Sprint(time.Now().Unix()))
		l.Invalidate()
		if l.onSynced != nil {
			l.onSynced(changes)
		}
		if err := l.syncTrailers(ctx); err != nil {
			l.log.Warn("trailer sync", "err", err)
		}
	}()
	return true
}

func (l *Library) sync(ctx context.Context, changes *Changes) error {
	srv, err := l.server(ctx)
	if err != nil {
		return err
	}
	sections, err := srv.Libraries(ctx)
	if err != nil {
		return i18n.E("library.load", err)
	}
	known, err := l.store.ListLibraries(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, sec := range sections {
		if sec.Type != "movie" && sec.Type != "show" {
			continue
		}
		seen[sec.ID] = true
		if err := l.store.UpsertLibrary(ctx, store.Library{ID: sec.ID, Title: sec.Title, Type: sec.Type}); err != nil {
			return err
		}
	}
	for _, k := range known {
		if !seen[k.ID] {
			if keys, err := l.store.MediaKeys(ctx, k.ID); err == nil {
				for key := range keys {
					changes.Removed[key] = true
				}
			}
			if err := l.store.DeleteLibrary(ctx, k.ID); err != nil {
				return err
			}
		}
	}
	libs, err := l.store.ListLibraries(ctx)
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if !lib.Enabled {
			continue
		}
		started := time.Now().Unix()
		before, err := l.store.MediaKeys(ctx, lib.ID)
		if err != nil {
			return err
		}
		after := map[string]bool{}
		var n int
		switch lib.Type {
		case "movie":
			n, err = l.syncMovies(ctx, srv, lib, started, after)
		case "show":
			n, err = l.syncShows(ctx, srv, lib, started, after)
		}
		// Titles stored before an error count as added: the next sync finds
		// them in the database and would not report them again.
		for k := range after {
			if !before[k] {
				changes.Added[k] = true
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", lib.Title, err)
		}
		for k := range before {
			if !after[k] {
				changes.Removed[k] = true
			}
		}
		removed, err := l.store.DeleteStaleMedia(ctx, lib.ID, started)
		if err != nil {
			return err
		}
		if err := l.syncDetails(ctx, srv, lib); err != nil {
			return fmt.Errorf("%s: %w", lib.Title, err)
		}
		if err := l.store.MarkLibrarySynced(ctx, lib.ID, time.Now().Unix()); err != nil {
			return err
		}
		l.log.Info("library synced", "library", lib.Title, "items", n, "removed", removed)
	}
	return nil
}

func (l *Library) syncMovies(ctx context.Context, srv mediaserver.Server, lib store.Library, now int64, seen map[string]bool) (int, error) {
	total := 0
	err := srv.Items(ctx, lib.ID, mediaserver.KindMovie, func(page []mediaserver.Item) error {
		batch := make([]*store.Media, 0, len(page))
		for i := range page {
			if m := convert(&page[i], lib.ID, nil); m != nil {
				batch = append(batch, m)
				seen[m.RatingKey] = true
			}
		}
		total += len(batch)
		l.setStatus(i18n.T(l.lang(ctx), "library.movies", lib.Title, total))
		return l.store.UpsertMedia(ctx, batch, now)
	})
	return total, err
}

func (l *Library) syncShows(ctx context.Context, srv mediaserver.Server, lib store.Library, now int64, seen map[string]bool) (int, error) {
	// Episodes inherit genres, studio, rating etc. from their show.
	shows := map[string]*mediaserver.Item{}
	err := srv.Items(ctx, lib.ID, mediaserver.KindShow, func(page []mediaserver.Item) error {
		for i := range page {
			s := page[i]
			shows[s.ID] = &s
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(shows))
	for k := range shows {
		keys = append(keys, k)
	}
	if err := l.store.SyncShows(ctx, lib.ID, keys); err != nil {
		return 0, err
	}
	total := 0
	err = srv.Items(ctx, lib.ID, mediaserver.KindEpisode, func(page []mediaserver.Item) error {
		batch := make([]*store.Media, 0, len(page))
		for i := range page {
			if m := convert(&page[i], lib.ID, shows[page[i].ShowID]); m != nil {
				batch = append(batch, m)
				seen[m.RatingKey] = true
			}
		}
		total += len(batch)
		l.setStatus(i18n.T(l.lang(ctx), "library.episodes", lib.Title, total))
		return l.store.UpsertMedia(ctx, batch, now)
	})
	return total, err
}

// Listings carry only the first few actors, directors and writers of a movie
// or show (Plex), so each movie and show is also fetched on its own: new ones
// right away, the others about every detailsEvery. The jitter spreads the
// first fetch of a whole library over the following weeks. Episodes get the
// tags of their show.
const (
	detailsEvery   = 30 * 24 * time.Hour
	detailsJitter  = 10 * 24 * time.Hour
	detailsWorkers = 4
	detailsBatch   = 50
	// detailsGiveUp failures in a row end the fetch for this sync; the items
	// stay due for the next one.
	detailsGiveUp = 10
)

func nextDetails(now time.Time) int64 {
	return now.Add(detailsEvery - detailsJitter + rand.N(2*detailsJitter)).Unix()
}

// syncDetails fetches the full metadata of the movies or shows of a library
// that are new or due.
func (l *Library) syncDetails(ctx context.Context, srv mediaserver.Server, lib store.Library) error {
	due, save := l.store.DueDetails, l.store.SetDetails
	if lib.Type == "show" {
		due, save = l.store.DueShowDetails, l.store.SetShowDetails
	}
	keys, err := due(ctx, lib.ID, time.Now().Unix())
	if err != nil || len(keys) == 0 {
		return err
	}
	type result struct {
		key string
		it  *mediaserver.Item
		err error
	}
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan string)
	results := make(chan result)
	go func() {
		defer close(jobs)
		for _, k := range keys {
			select {
			case jobs <- k:
			case <-fetchCtx.Done():
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for range min(detailsWorkers, len(keys)) {
		wg.Go(func() {
			for k := range jobs {
				it, err := srv.Item(fetchCtx, k)
				results <- result{k, it, err}
			}
		})
	}
	go func() { wg.Wait(); close(results) }()

	lang := l.lang(ctx)
	var batch []store.Details
	var flushErr error
	flush := func() {
		if len(batch) > 0 && flushErr == nil {
			flushErr = save(ctx, batch)
		}
		batch = batch[:0]
	}
	done, fetched, inARow := 0, 0, 0
	for r := range results {
		done++
		switch {
		case r.err == nil && r.it != nil:
			inARow = 0
			fetched++
			batch = append(batch, detailsOf(r.key, r.it, time.Now()))
		case fetchCtx.Err() == nil:
			inARow++
			l.log.Debug("fetch details", "item", r.key, "err", r.err)
			if inARow >= detailsGiveUp {
				l.log.Warn("fetching details stopped", "library", lib.Title, "err", r.err)
				cancel()
			}
		}
		if len(batch) >= detailsBatch {
			flush()
		}
		if done%10 == 0 || done == len(keys) {
			l.setStatus(i18n.T(lang, "library.details", lib.Title, done, len(keys)))
		}
	}
	flush()
	if flushErr != nil {
		return flushErr
	}
	l.log.Info("details fetched", "library", lib.Title, "items", fetched, "due", len(keys))
	return ctx.Err()
}

func detailsOf(key string, it *mediaserver.Item, now time.Time) store.Details {
	raw, _ := json.Marshal(it)
	// Collections and labels come complete with every sync; taken from here
	// they would outlive their removal by weeks.
	return store.Details{RatingKey: key, Raw: raw, FetchedAt: now.Unix(), DueAt: nextDetails(now),
		Tags: store.Tags{Genre: it.Genres, Actor: it.Actors, Director: it.Directors, Writer: it.Writers, Country: it.Countries}}
}

func convert(md *mediaserver.Item, libraryID string, show *mediaserver.Item) *store.Media {
	if len(md.Media) == 0 || len(md.Media[0].Parts) == 0 {
		return nil
	}
	mi := md.Media[0]
	dur := md.Duration
	if dur == 0 {
		dur = mi.Duration
	}
	if dur <= 0 {
		return nil
	}
	m := &store.Media{
		RatingKey:     md.ID,
		GUID:          md.GUID,
		LibraryID:     libraryID,
		Type:          md.Type,
		Title:         md.Title,
		SortTitle:     strings.ToLower(firstNonEmpty(md.SortTitle, md.Title)),
		Year:          md.Year,
		DurationMs:    dur,
		Summary:       md.Summary,
		Thumb:         md.Thumb,
		Art:           md.Art,
		ContentRating: md.ContentRating,
		Rating:        firstNonZero(md.AudienceRating, md.Rating),
		Studio:        md.Studio,
		AddedAt:       md.AddedAt,
		Released:      md.Released,
		ViewCount:     md.ViewCount,
		PartKey:       mi.Parts[0].Key,
		Container:     mi.Container,
		VideoCodec:    mi.VideoCodec,
		AudioCodec:    mi.AudioCodec,
		Resolution:    mi.VideoResolution,
		Tags: store.Tags{
			Genre:      md.Genres,
			Actor:      md.Actors,
			Director:   md.Directors,
			Writer:     md.Writers,
			Collection: merge(md.Collections, nil), // may arrive twice (listing and collection list)
			Label:      merge(md.Labels, nil),
			Country:    md.Countries,
		},
	}
	for k, v := range md.ExternalIDs {
		if m.Ext == nil {
			m.Ext = map[string]string{}
		}
		m.Ext[k] = v
	}
	if md.Type == "episode" {
		if show != nil {
			for k, v := range show.ExternalIDs {
				if m.Ext == nil {
					m.Ext = map[string]string{}
				}
				m.Ext["show."+k] = v
			}
		}
		m.ShowKey = md.ShowID
		m.ShowTitle = md.ShowTitle
		m.ShowThumb = md.ShowThumb
		m.Season = md.ParentIndex
		m.Episode = md.Index
		m.SortTitle = strings.ToLower(md.ShowTitle)
		if show != nil {
			m.SortTitle = strings.ToLower(firstNonEmpty(show.SortTitle, show.Title))
			if m.Year == 0 {
				m.Year = show.Year
			}
			if m.ContentRating == "" {
				m.ContentRating = show.ContentRating
			}
			if m.Studio == "" {
				m.Studio = show.Studio
			}
			if m.Rating == 0 {
				m.Rating = firstNonZero(show.AudienceRating, show.Rating)
			}
			m.Tags.Genre = merge(m.Tags.Genre, show.Genres)
			m.Tags.Actor = merge(m.Tags.Actor, show.Actors)
			m.Tags.Collection = merge(m.Tags.Collection, show.Collections)
			m.Tags.Label = merge(m.Tags.Label, show.Labels)
			m.Tags.Country = merge(m.Tags.Country, show.Countries)
		}
	}
	return m
}

func merge(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(a, b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstNonZero(v ...float64) float64 {
	for _, f := range v {
		if f != 0 {
			return f
		}
	}
	return 0
}

var ErrNotLinked = i18n.E("library.notLinked")

// lang is the language of the installation, for status messages.
func (l *Library) lang(ctx context.Context) i18n.Lang {
	if v := l.store.Setting(ctx, store.SetLanguage, ""); i18n.Known(v) {
		return i18n.Lang(v)
	}
	return i18n.EN
}
