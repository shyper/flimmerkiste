package library

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

// FolderFile is the description scripts/trailers.py writes next to each
// trailer video ("<name>.json"). A video without one is not used.
type FolderFile struct {
	Video         string   `json:"video"` // file name of the video in the same folder
	Kind          string   `json:"kind"`  // movie
	TMDb          int      `json:"tmdb"`
	IMDb          string   `json:"imdb"`
	Title         string   `json:"title"`
	Year          int      `json:"year"`
	Released      string   `json:"released"` // release date in the region, YYYY-MM-DD
	Genres        []string `json:"genres"`
	ContentRating string   `json:"content_rating"` // "de/12" like Plex
	Summary       string   `json:"summary"`
	Poster        string   `json:"poster"`   // absolute URL
	Backdrop      string   `json:"backdrop"` // absolute URL
	RuntimeMin    int      `json:"runtime_min"`
	Language      string   `json:"language"` // spoken language of the trailer: de, en …
	Name          string   `json:"name"`     // name of the video, e.g. "Offizieller Trailer"
	Height        int      `json:"height"`
	DurationMs    int64    `json:"duration_ms"`
}

// FolderPrefix marks the trailer keys of the trailer folder ("t:folder-…").
const FolderPrefix = store.TrailerPrefix + "folder-"

// SyncFolder stores the trailers of the trailer folder (FLIMMERKISTE_TRAILER),
// which scripts/trailers.py fills with trailers of new movies. Like Discover
// trailers they advertise titles that are not in the library; titles in the
// library are left out. Trailers whose files are gone are removed.
func (l *Library) SyncFolder(ctx context.Context, dir string) (DiscoverResult, error) {
	var res DiscoverResult
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, err
	}
	owned := map[string]bool{}
	all, err := l.All(ctx)
	if err != nil {
		return res, err // without the library, owned titles would come back as new
	}
	for _, m := range all {
		if m.Type == "movie" {
			for _, p := range []string{"tmdb", "imdb"} {
				if id := m.Ext[p]; id != "" {
					owned[p+":"+id] = true
				}
			}
		}
	}

	now := time.Now()
	var found []*store.Trailer
	for _, e := range entries {
		// hidden files: the state of trailers.py, partial downloads
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		t, err := folderTrailer(dir, e.Name(), now)
		if err != nil {
			l.log.Debug("trailer folder", "file", e.Name(), "err", err)
			continue
		}
		res.Titles++
		if owned["tmdb:"+strconv.Itoa(t.tmdb)] || (t.imdb != "" && owned["imdb:"+t.imdb]) {
			continue
		}
		found = append(found, t.Trailer)
	}
	if err := l.store.UpsertTrailers(ctx, found); err != nil {
		return res, err
	}
	res.Trailers = len(found)
	keep := make([]string, len(found))
	for i, t := range found {
		keep[i] = t.Key
	}
	res.Removed, err = l.store.DeleteMissingTrailers(ctx, store.OriginFolder, keep)
	if res.Trailers > 0 || res.Removed > 0 {
		l.log.Debug("trailer folder synced", "trailers", res.Trailers, "removed", res.Removed)
	}
	return res, err
}

type folderEntry struct {
	*store.Trailer
	tmdb int
	imdb string
}

// folderTrailer reads one description and checks that its video is there.
func folderTrailer(dir, name string, now time.Time) (*folderEntry, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	var f FolderFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Video == "" || f.Title == "" || f.DurationMs <= 0 || (f.TMDb == 0 && f.IMDb == "") {
		return nil, fmt.Errorf("incomplete description")
	}
	video := filepath.Join(dir, filepath.Base(f.Video))
	if st, err := os.Stat(video); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("video missing: %s", f.Video)
	}
	id, guid := "imdb-"+f.IMDb, "imdb://"+f.IMDb
	if f.TMDb != 0 {
		// the same guid as the TMDb slate of the title, which the video replaces
		id, guid = strconv.Itoa(f.TMDb), fmt.Sprintf("tmdb://%d", f.TMDb)
	}
	kind := f.Kind
	if kind == "" {
		kind = "movie"
	}
	name = f.Name
	if name == "" {
		name = f.Title
	}
	return &folderEntry{tmdb: f.TMDb, imdb: f.IMDb, Trailer: &store.Trailer{
		Key: FolderPrefix + id + "-" + strings.ToLower(f.Language), Origin: store.OriginFolder, GUID: guid,
		Title: f.Title, Kind: kind, Year: f.Year, Released: f.Released, Genres: f.Genres, ContentRating: f.ContentRating,
		Summary: f.Summary, Thumb: f.Poster, Art: f.Backdrop, RuntimeMin: f.RuntimeMin, Name: name, Language: f.Language,
		DurationMs: f.DurationMs, Parts: []store.TrailerPart{{Height: f.Height, URL: store.FileURL(video)}}, LastSeen: now.Unix(),
	}}, nil
}
