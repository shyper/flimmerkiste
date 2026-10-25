package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
)

// TypeTrailer marks trailers played as filler. They are not part of the
// library catalog; see Trailer.Media.
const TypeTrailer = "trailer"

// Media is one playable item: a movie or a single episode.
type Media struct {
	RatingKey     string  `json:"ratingKey"`
	GUID          string  `json:"-"` // plex://movie/… (matches Plex Discover titles)
	LibraryID     string  `json:"libraryId"`
	Type          string  `json:"type"` // "movie" or "episode"
	Title         string  `json:"title"`
	SortTitle     string  `json:"-"`
	Year          int     `json:"year,omitempty"`
	DurationMs    int64   `json:"durationMs"`
	Summary       string  `json:"summary,omitempty"`
	Thumb         string  `json:"thumb,omitempty"`
	Art           string  `json:"-"`
	ContentRating string  `json:"contentRating,omitempty"`
	Rating        float64 `json:"rating,omitempty"`
	Studio        string  `json:"studio,omitempty"`
	AddedAt       int64   `json:"addedAt,omitempty"`
	Released      string  `json:"released,omitempty"`
	ViewCount     int     `json:"viewCount"`
	ShowKey       string  `json:"showKey,omitempty"`
	ShowTitle     string  `json:"showTitle,omitempty"`
	ShowThumb     string  `json:"-"`
	Season        int     `json:"season,omitempty"`
	Episode       int     `json:"episode,omitempty"`
	PartKey       string  `json:"-"`
	Container     string  `json:"-"`
	VideoCodec    string  `json:"videoCodec,omitempty"`
	AudioCodec    string  `json:"audioCodec,omitempty"`
	Resolution    string  `json:"resolution,omitempty"`
	Tags          Tags    `json:"tags"`
	// ShowSummary is the description of the show of an episode.
	ShowSummary string `json:"-"`
	// ShowFirstYear and ShowLastYear: the years the first and last episode of
	// the show in the library aired (AllMedia only).
	ShowFirstYear int `json:"-"`
	ShowLastYear  int `json:"-"`
	// Ext holds external ids (tmdb, imdb, tvdb; for episodes also show.tmdb …),
	// used to find the same title in another library.
	Ext map[string]string `json:"-"`
}

// Tags holds the multi-valued metadata used by the rule engine.
type Tags struct {
	Genre      []string `json:"genre,omitempty"`
	Actor      []string `json:"actor,omitempty"`
	Director   []string `json:"director,omitempty"`
	Writer     []string `json:"writer,omitempty"`
	Collection []string `json:"collection,omitempty"`
	Label      []string `json:"label,omitempty"`
	Country    []string `json:"country,omitempty"`
}

// DisplayTitle is what we show in guides: "Show – S01E02 · Title" for episodes.
func (m *Media) DisplayTitle() string {
	if m.Type == "episode" {
		return m.ShowTitle
	}
	return m.Title
}

const mediaCols = `rating_key, library_id, type, title, sort_title, year, duration_ms, summary, thumb, art,
	content_rating, rating, studio, added_at, released, view_count, show_key, show_title, show_thumb,
	season, episode, part_key, container, video_codec, audio_codec, resolution, tags, guid, ext`

// selectCols returns the columns scanMedia reads, those of a sync plus the
// tags of the full metadata of the item and of its show, with the media
// table named p (e.g. "m." or "").
func selectCols(p string) string {
	cols := mediaCols + `, detail_tags`
	if p != "" {
		cols = prefixed(p, cols)
	}
	return cols + `, (SELECT sd.detail_tags FROM show_details sd WHERE sd.key = ` + p + `show_key)` +
		`, (SELECT sd.summary FROM show_details sd WHERE sd.key = ` + p + `show_key)`
}

func scanMedia(sc interface{ Scan(...any) error }) (*Media, error) {
	var m Media
	var tags, ext, detailTags string
	var showTags, showSummary sql.NullString
	err := sc.Scan(&m.RatingKey, &m.LibraryID, &m.Type, &m.Title, &m.SortTitle, &m.Year, &m.DurationMs,
		&m.Summary, &m.Thumb, &m.Art, &m.ContentRating, &m.Rating, &m.Studio, &m.AddedAt, &m.Released,
		&m.ViewCount, &m.ShowKey, &m.ShowTitle, &m.ShowThumb, &m.Season, &m.Episode, &m.PartKey,
		&m.Container, &m.VideoCodec, &m.AudioCodec, &m.Resolution, &tags, &m.GUID, &ext, &detailTags, &showTags, &showSummary)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tags), &m.Tags); err != nil && tags != "" {
		// the title stays playable, but rules on its tags will not find it
		slog.Warn("tags of a title unreadable", "key", m.RatingKey, "err", err)
	}
	if err := json.Unmarshal([]byte(ext), &m.Ext); err != nil && ext != "" {
		slog.Warn("external ids of a title unreadable", "key", m.RatingKey, "err", err)
	}
	var d Tags
	if json.Unmarshal([]byte(detailTags), &d) == nil {
		m.Tags.Add(d)
	}
	if showTags.Valid {
		var st Tags
		if json.Unmarshal([]byte(showTags.String), &st) == nil {
			// Like the sync does with the listed show (see library.convert).
			m.Tags.Add(Tags{Genre: st.Genre, Actor: st.Actor, Director: st.Director, Writer: st.Writer, Country: st.Country})
		}
	}
	m.ShowSummary = showSummary.String
	return &m, nil
}

// Add merges the values of o that t lacks, keeping the order of t first.
func (t *Tags) Add(o Tags) {
	t.Genre = union(t.Genre, o.Genre)
	t.Actor = union(t.Actor, o.Actor)
	t.Director = union(t.Director, o.Director)
	t.Writer = union(t.Writer, o.Writer)
	t.Collection = union(t.Collection, o.Collection)
	t.Label = union(t.Label, o.Label)
	t.Country = union(t.Country, o.Country)
}

func union(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(a[:len(a):len(a)], b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// UpsertMedia writes a batch of items in one transaction.
func (s *Store) UpsertMedia(ctx context.Context, items []*Media, syncedAt int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO media (`+mediaCols+`, synced_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(rating_key) DO UPDATE SET library_id=excluded.library_id, type=excluded.type,
			title=excluded.title, sort_title=excluded.sort_title, year=excluded.year,
			duration_ms=excluded.duration_ms, summary=excluded.summary, thumb=excluded.thumb, art=excluded.art,
			content_rating=excluded.content_rating, rating=excluded.rating, studio=excluded.studio,
			added_at=excluded.added_at, released=excluded.released, view_count=excluded.view_count,
			show_key=excluded.show_key, show_title=excluded.show_title, show_thumb=excluded.show_thumb,
			season=excluded.season, episode=excluded.episode, part_key=excluded.part_key,
			container=excluded.container, video_codec=excluded.video_codec, audio_codec=excluded.audio_codec,
			resolution=excluded.resolution, tags=excluded.tags, guid=excluded.guid, ext=excluded.ext, synced_at=excluded.synced_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range items {
		tags, _ := json.Marshal(m.Tags)
		ext, _ := json.Marshal(m.Ext)
		if m.Ext == nil {
			ext = []byte("{}")
		}
		if _, err := stmt.ExecContext(ctx, m.RatingKey, m.LibraryID, m.Type, m.Title, m.SortTitle, m.Year,
			m.DurationMs, m.Summary, m.Thumb, m.Art, m.ContentRating, m.Rating, m.Studio, m.AddedAt, m.Released,
			m.ViewCount, m.ShowKey, m.ShowTitle, m.ShowThumb, m.Season, m.Episode, m.PartKey, m.Container,
			m.VideoCodec, m.AudioCodec, m.Resolution, string(tags), m.GUID, string(ext), syncedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MediaKeys returns the rating keys currently stored for a library.
func (s *Store) MediaKeys(ctx context.Context, libraryID string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT rating_key FROM media WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// DeleteStaleMedia removes items of a library that were not seen in the latest sync.
func (s *Store) DeleteStaleMedia(ctx context.Context, libraryID string, syncedAt int64) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM media WHERE library_id = ? AND synced_at < ?`, libraryID, syncedAt)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) GetMedia(ctx context.Context, key string) (*Media, error) {
	m, err := scanMedia(s.DB.QueryRowContext(ctx, `SELECT `+selectCols("")+` FROM media WHERE rating_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// AllMedia loads every playable item of enabled libraries. Libraries of a few
// ten-thousand items fit comfortably in memory, which keeps the rule engine simple.
func (s *Store) AllMedia(ctx context.Context) ([]*Media, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+selectCols("m.")+` FROM media m
		JOIN libraries l ON l.id = m.library_id WHERE l.enabled = 1
		ORDER BY m.sort_title, m.show_title, m.season, m.episode`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Media
	// Episodes of a show share one copy of its description.
	summaries := map[string]string{}
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		if m.ShowSummary != "" {
			if s, ok := summaries[m.ShowKey]; ok {
				m.ShowSummary = s
			} else {
				summaries[m.ShowKey] = m.ShowSummary
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	showYears(out)
	return out, nil
}

// showYears sets the years the episodes of each show span.
func showYears(items []*Media) {
	type span struct{ first, last int }
	spans := map[string]span{}
	for _, m := range items {
		if m.Type != "episode" || m.Year == 0 {
			continue
		}
		sp, ok := spans[m.ShowKey]
		if !ok || m.Year < sp.first {
			sp.first = m.Year
		}
		sp.last = max(sp.last, m.Year)
		spans[m.ShowKey] = sp
	}
	for _, m := range items {
		if sp, ok := spans[m.ShowKey]; ok && m.Type == "episode" {
			m.ShowFirstYear, m.ShowLastYear = sp.first, sp.last
		}
	}
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// ---- full metadata ----

// Details is the full metadata of one item as fetched on its own.
type Details struct {
	RatingKey string
	Tags      Tags   // complete cast, genres … – added to the tags of the sync
	Raw       []byte // everything the server returned, as JSON
	FetchedAt int64
	DueAt     int64 // when to fetch it again
}

// Full metadata is kept for movies (table media) and shows (show_details).
type detailsTable struct{ name, key string }

var (
	movieDetails = detailsTable{"media", "rating_key"}
	showDetails  = detailsTable{"show_details", "key"}
)

// DueDetails returns the movies of a library whose full metadata is missing or
// due at now, those never fetched first.
func (s *Store) DueDetails(ctx context.Context, libraryID string, now int64) ([]string, error) {
	return s.dueDetails(ctx, movieDetails, libraryID, now)
}

// DueShowDetails is DueDetails for the shows of a library.
func (s *Store) DueShowDetails(ctx context.Context, libraryID string, now int64) ([]string, error) {
	return s.dueDetails(ctx, showDetails, libraryID, now)
}

func (s *Store) dueDetails(ctx context.Context, t detailsTable, libraryID string, now int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+t.key+` FROM `+t.name+` WHERE library_id = ? AND details_due <= ?
		ORDER BY details_at, details_due`, libraryID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// SetDetails stores the full metadata of a batch of movies in one transaction.
func (s *Store) SetDetails(ctx context.Context, items []Details) error {
	return s.setDetails(ctx, movieDetails, items)
}

// SetShowDetails is SetDetails for shows.
func (s *Store) SetShowDetails(ctx context.Context, items []Details) error {
	return s.setDetails(ctx, showDetails, items)
}

func (s *Store) setDetails(ctx context.Context, t detailsTable, items []Details) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE `+t.name+` SET detail_tags = ?, details = ?, details_at = ?, details_due = ?
		WHERE `+t.key+` = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, d := range items {
		tags, _ := json.Marshal(d.Tags)
		if _, err := stmt.ExecContext(ctx, string(tags), string(d.Raw), d.FetchedAt, d.DueAt, d.RatingKey); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Show is a show as a library lists it.
type Show struct {
	Key     string
	Summary string
}

// SyncShows records the shows a library lists now: new ones become due,
// those gone are dropped with their metadata.
func (s *Store) SyncShows(ctx context.Context, libraryID string, shows []Show) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT key FROM show_details WHERE library_id = ?`, libraryID)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		known[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sh := range shows {
		delete(known, sh.Key)
		if _, err := tx.ExecContext(ctx, `INSERT INTO show_details (key, library_id, summary) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET library_id = excluded.library_id, summary = excluded.summary`,
			sh.Key, libraryID, sh.Summary); err != nil {
			return err
		}
	}
	for k := range known {
		if _, err := tx.ExecContext(ctx, `DELETE FROM trailers WHERE show_key = ?`, k); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM show_details WHERE key = ?`, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResetDetails makes the full metadata of every movie and show of a library
// due, so the next sync fetches all of it again.
func (s *Store) ResetDetails(ctx context.Context, libraryID string) error {
	for _, t := range []detailsTable{movieDetails, showDetails} {
		if _, err := s.DB.ExecContext(ctx, `UPDATE `+t.name+` SET details_due = 0 WHERE library_id = ?`, libraryID); err != nil {
			return err
		}
	}
	return nil
}

// MediaDetails returns the stored full metadata of an item as JSON (nil if
// it was not fetched yet).
func (s *Store) MediaDetails(ctx context.Context, key string) ([]byte, error) {
	return s.rawDetails(ctx, movieDetails, key)
}

// ShowDetails is MediaDetails for a show.
func (s *Store) ShowDetails(ctx context.Context, key string) ([]byte, error) {
	return s.rawDetails(ctx, showDetails, key)
}

func (s *Store) rawDetails(ctx context.Context, t detailsTable, key string) ([]byte, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT details FROM `+t.name+` WHERE `+t.key+` = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || raw == "" {
		return nil, err
	}
	return []byte(raw), nil
}

// ---- libraries ----

type Library struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Enabled  bool   `json:"enabled"`
	SyncedAt int64  `json:"syncedAt"`
	Items    int    `json:"items"`
	Detailed int    `json:"detailed"` // movies or episodes with full metadata
	Shows    int    `json:"shows,omitempty"`
	// ShowsDetailed counts the shows with full metadata.
	ShowsDetailed int `json:"showsDetailed,omitempty"`
}

func (s *Store) ListLibraries(ctx context.Context) ([]Library, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT l.id, l.title, l.type, l.enabled, l.synced_at,
		(SELECT COUNT(*) FROM media m WHERE m.library_id = l.id),
		(SELECT COUNT(*) FROM media m WHERE m.library_id = l.id AND m.details_at > 0),
		(SELECT COUNT(*) FROM show_details sd WHERE sd.library_id = l.id),
		(SELECT COUNT(*) FROM show_details sd WHERE sd.library_id = l.id AND sd.details_at > 0)
		FROM libraries l ORDER BY l.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Title, &l.Type, &l.Enabled, &l.SyncedAt, &l.Items, &l.Detailed, &l.Shows, &l.ShowsDetailed); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) UpsertLibrary(ctx context.Context, l Library) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO libraries (id, title, type) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET title = excluded.title, type = excluded.type`, l.ID, l.Title, l.Type)
	return err
}

func (s *Store) MarkLibrarySynced(ctx context.Context, id string, at int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE libraries SET synced_at = ? WHERE id = ?`, at, id)
	return err
}

func (s *Store) SetLibraryEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE libraries SET enabled = ? WHERE id = ?`, enabled, id)
	return err
}

func (s *Store) DeleteLibrary(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM media WHERE library_id = ?`,
		`DELETE FROM trailers WHERE show_key IN (SELECT key FROM show_details WHERE library_id = ?)`,
		`DELETE FROM show_details WHERE library_id = ?`,
		`DELETE FROM libraries WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
