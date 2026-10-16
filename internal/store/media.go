package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func scanMedia(sc interface{ Scan(...any) error }) (*Media, error) {
	var m Media
	var tags, ext string
	err := sc.Scan(&m.RatingKey, &m.LibraryID, &m.Type, &m.Title, &m.SortTitle, &m.Year, &m.DurationMs,
		&m.Summary, &m.Thumb, &m.Art, &m.ContentRating, &m.Rating, &m.Studio, &m.AddedAt, &m.Released,
		&m.ViewCount, &m.ShowKey, &m.ShowTitle, &m.ShowThumb, &m.Season, &m.Episode, &m.PartKey,
		&m.Container, &m.VideoCodec, &m.AudioCodec, &m.Resolution, &tags, &m.GUID, &ext)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tags), &m.Tags)
	_ = json.Unmarshal([]byte(ext), &m.Ext)
	return &m, nil
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
	m, err := scanMedia(s.DB.QueryRowContext(ctx, `SELECT `+mediaCols+` FROM media WHERE rating_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// AllMedia loads every playable item of enabled libraries. Libraries of a few
// ten-thousand items fit comfortably in memory, which keeps the rule engine simple.
func (s *Store) AllMedia(ctx context.Context) ([]*Media, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+prefixed("m.", mediaCols)+` FROM media m
		JOIN libraries l ON l.id = m.library_id WHERE l.enabled = 1
		ORDER BY m.sort_title, m.show_title, m.season, m.episode`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Media
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// ---- libraries ----

type Library struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Enabled  bool   `json:"enabled"`
	SyncedAt int64  `json:"syncedAt"`
	Items    int    `json:"items"`
}

func (s *Store) ListLibraries(ctx context.Context) ([]Library, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT l.id, l.title, l.type, l.enabled, l.synced_at,
		(SELECT COUNT(*) FROM media m WHERE m.library_id = l.id) FROM libraries l ORDER BY l.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Title, &l.Type, &l.Enabled, &l.SyncedAt, &l.Items); err != nil {
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
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM media WHERE library_id = ?`, id); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	return err
}
