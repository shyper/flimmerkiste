package library

import (
	"context"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

const (
	discoverPerHub = 50
	discoverMax    = 200 // titles per refresh
	// staleDiscover: trailers of titles that left all lists are kept this long.
	staleDiscover = 14 * 24 * time.Hour
)

// DiscoverResult summarises a refresh of the Discover trailers.
type DiscoverResult struct {
	Titles   int   `json:"titles"`   // titles found in the lists
	Trailers int   `json:"trailers"` // trailers in a preferred language
	Removed  int64 `json:"removed"`  // outdated trailers deleted
}

type discoverTitle struct {
	mediaserver.CatalogTitle
	hubs []string
}

// SyncDiscover collects the titles of the given Discover lists (hub ids such
// as "home.coming-soon"), looks up their trailers in the preferred languages
// and stores them. Titles in the library are skipped (their trailers come from
// the library sync). Trailers of titles that left all lists expire.
func (l *Library) SyncDiscover(ctx context.Context, d mediaserver.TrailerCatalog, hubIDs []string, langs []string) (DiscoverResult, error) {
	var res DiscoverResult
	hubs, err := d.Lists(ctx)
	if err != nil {
		return res, i18n.E("discover.load", err)
	}
	owned, err := l.store.MediaGUIDs(ctx)
	if err != nil {
		return res, err
	}
	wanted := map[string]bool{}
	for _, id := range hubIDs {
		wanted[id] = true
	}
	titles := map[string]*discoverTitle{}
	var order []string
	for _, h := range hubs {
		if !wanted[h.ID] {
			continue
		}
		items, err := d.Titles(ctx, h, discoverPerHub)
		if err != nil {
			l.log.Warn("discover list", "list", h.Title, "err", err)
			continue
		}
		for _, it := range items {
			if owned[it.GUID] {
				continue
			}
			if cur, ok := titles[it.ID]; ok {
				cur.hubs = append(cur.hubs, h.ID)
				continue
			}
			titles[it.ID] = &discoverTitle{CatalogTitle: it, hubs: []string{h.ID}}
			order = append(order, it.ID)
		}
	}
	if len(order) > discoverMax {
		order = order[:discoverMax]
	}
	res.Titles = len(order)

	now := time.Now()
	var mu sync.Mutex
	var found []*store.Trailer
	jobs := make(chan *discoverTitle)
	var wg sync.WaitGroup
	for range extrasWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				ts, err := discoverTrailers(ctx, d, t, langs, now)
				if err != nil {
					l.log.Debug("discover trailers", "title", t.Title, "err", err)
					continue
				}
				mu.Lock()
				found = append(found, ts...)
				mu.Unlock()
			}
		}()
	}
feed:
	for _, id := range order {
		select {
		case jobs <- titles[id]:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if err := l.store.UpsertTrailers(ctx, found); err != nil {
		return res, err
	}
	res.Trailers = len(found)
	res.Removed, err = l.store.DeleteStaleTrailers(ctx, store.OriginDiscover, now.Add(-staleDiscover))
	l.log.Info("discover trailers synced", "titles", res.Titles, "trailers", res.Trailers, "removed", res.Removed)
	return res, err
}

// discoverTrailers fetches metadata and extras of a title and returns its
// trailers in preferred languages.
func discoverTrailers(ctx context.Context, d mediaserver.TrailerCatalog, t *discoverTitle, langs []string, now time.Time) ([]*store.Trailer, error) {
	extras, err := d.Extras(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	var out []*store.Trailer
	for i := range extras {
		tr := TrailerFrom(&extras[i])
		if tr == nil || store.LanguageRank(tr.Language, langs) < 0 {
			continue
		}
		out = append(out, tr)
		if len(out) == maxTrailersPerTitle {
			break
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	md, err := d.Details(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	genres := localGenres(md.Genres)
	for _, tr := range out {
		tr.Origin, tr.GUID, tr.Kind, tr.Title = store.OriginDiscover, t.GUID, t.Kind, t.Title
		tr.Year, tr.Released, tr.ContentRating = md.Year, md.Released, md.ContentRating
		tr.Summary, tr.Thumb, tr.Genres = md.Summary, md.Thumb, genres
		tr.Hubs, tr.LastSeen = t.hubs, now.Unix()
	}
	return out, nil
}

// germanGenres translates the catalog's English genres to the names German
// Plex libraries use, so filter rules written for the library match too.
var germanGenres = map[string]string{
	"Adventure": "Abenteuer", "Comedy": "Komödie", "Crime": "Krimi", "Documentary": "Dokumentarfilm",
	"Family": "Familie", "History": "Historie", "Music": "Musik", "Romance": "Liebesfilm",
	"Sci-Fi": "Science-Fiction", "Science Fiction": "Science-Fiction", "War": "Kriegsfilm",
	"Biography": "Biografie", "Children": "Kinder", "Musical": "Musical", "Sport": "Sport",
}

// localGenres returns the German names first (shown on slates), followed by
// the original names of translated genres (still matched by filter rules).
func localGenres(in []string) []string {
	out := make([]string, 0, 2*len(in))
	var orig []string
	for _, g := range in {
		if de, ok := germanGenres[g]; ok && de != g {
			out = append(out, de)
			orig = append(orig, g)
		} else {
			out = append(out, g)
		}
	}
	return append(out, orig...)
}
