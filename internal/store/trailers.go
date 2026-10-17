package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Trailer origins.
const (
	OriginLibrary  = "library"  // extra of a movie or show in the library
	OriginDiscover = "discover" // extra of a title from a Plex Discover list
	OriginTMDb     = "tmdb"     // "coming to theaters" slate of a title from TMDb (no video)
	OriginInfo     = "info"     // weather or news slate (not stored; see InfoPrefix)
)

// TrailerPrefix marks trailer keys in programs ("t:<rating key of the extra>").
const TrailerPrefix = "t:"

// Trailer is one trailer together with the title it advertises.
type Trailer struct {
	Key           string        `json:"key"`
	Origin        string        `json:"origin"`
	MediaKey      string        `json:"mediaKey,omitempty"` // library movie (origin library)
	ShowKey       string        `json:"showKey,omitempty"`  // library show (origin library)
	GUID          string        `json:"guid"`               // plex://movie/…
	Title         string        `json:"title"`              // movie / show title
	Kind          string        `json:"kind"`               // movie | show
	Year          int           `json:"year,omitempty"`
	Released      string        `json:"released,omitempty"`
	Genres        []string      `json:"genres,omitempty"`
	ContentRating string        `json:"contentRating,omitempty"`
	Summary       string        `json:"summary,omitempty"`
	Thumb         string        `json:"thumb,omitempty"` // Plex path (library) or absolute URL (discover)
	Name          string        `json:"name"`            // e.g. "Avengers: Doomsday (German Trailer 1)"
	Language      string        `json:"language"`        // deu, de, eng …
	DurationMs    int64         `json:"durationMs"`
	Parts         []TrailerPart `json:"parts"`
	Hubs          []string      `json:"hubs,omitempty"`
	LastSeen      int64         `json:"lastSeen,omitempty"`
	Art           string        `json:"art,omitempty"`        // backdrop (absolute URL), TMDb slates
	RuntimeMin    int           `json:"runtimeMin,omitempty"` // length of the movie, TMDb slates
	Season        int           `json:"season,omitempty"`     // TMDb slates of a show: the new season (0 = a new show)
}

// TrailerPart is one quality of a trailer. URL is a Plex path for library
// trailers (needs the server address and token) or an absolute URL.
type TrailerPart struct {
	Height int    `json:"h"`
	URL    string `json:"u"`
}

// Part picks the best quality not above height (or the smallest one).
func (t *Trailer) Part(height int) string {
	best, bestH := "", 0
	low, lowH := "", 1<<30
	for _, p := range t.Parts {
		if p.Height <= height && p.Height > bestH {
			best, bestH = p.URL, p.Height
		}
		if p.Height < lowH {
			low, lowH = p.URL, p.Height
		}
	}
	if best != "" {
		return best
	}
	return low
}

// Advertised names the library title a trailer advertises: the rating key of
// a movie, "show:<key>" for a show, "" for titles not in the library.
func (t *Trailer) Advertised() string {
	if t.ShowKey != "" {
		return "show:" + t.ShowKey
	}
	return t.MediaKey
}

// Item describes the advertised title like a library item, for filter rules.
func (t *Trailer) Item() *Media {
	return &Media{RatingKey: t.Key, GUID: t.GUID, Type: t.Kind, Title: t.Title, SortTitle: strings.ToLower(t.Title),
		Year: t.Year, Released: t.Released, ContentRating: t.ContentRating, Summary: t.Summary,
		DurationMs: t.DurationMs, Tags: Tags{Genre: t.Genres}}
}

// Media returns the trailer as a playable item in the given quality.
// LibraryID carries the origin, as trailers belong to no library.
func (t *Trailer) Media(height int) *Media {
	return &Media{RatingKey: t.Key, LibraryID: t.Origin, GUID: t.GUID, Type: TypeTrailer, Title: t.Title, Year: t.Year,
		DurationMs: t.DurationMs, Released: t.Released, Summary: t.Summary, Thumb: t.Thumb,
		ContentRating: t.ContentRating, PartKey: t.Part(height), Tags: Tags{Genre: t.Genres}}
}

const trailerCols = `key, origin, COALESCE(media_key, ''), guid, title, kind, year, released, genres, content_rating,
	summary, thumb, name, language, duration_ms, parts, hubs, last_seen, art, runtime_min, show_key, season`

func scanTrailer(sc interface{ Scan(...any) error }) (*Trailer, error) {
	var t Trailer
	var genres, parts, hubs string
	if err := sc.Scan(&t.Key, &t.Origin, &t.MediaKey, &t.GUID, &t.Title, &t.Kind, &t.Year, &t.Released, &genres,
		&t.ContentRating, &t.Summary, &t.Thumb, &t.Name, &t.Language, &t.DurationMs, &parts, &hubs, &t.LastSeen, &t.Art, &t.RuntimeMin, &t.ShowKey, &t.Season); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(genres), &t.Genres)
	_ = json.Unmarshal([]byte(parts), &t.Parts)
	_ = json.Unmarshal([]byte(hubs), &t.Hubs)
	return &t, nil
}

func insertTrailer(ctx context.Context, tx *sql.Tx, t *Trailer) error {
	genres, _ := json.Marshal(nonNil(t.Genres))
	parts, _ := json.Marshal(t.Parts)
	hubs, _ := json.Marshal(nonNil(t.Hubs))
	var mediaKey any
	if t.MediaKey != "" {
		mediaKey = t.MediaKey
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO trailers (`+strings.Replace(trailerCols, "COALESCE(media_key, '')", "media_key", 1)+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET origin=excluded.origin, media_key=excluded.media_key, guid=excluded.guid,
			title=excluded.title, kind=excluded.kind, year=excluded.year, released=excluded.released,
			genres=excluded.genres, content_rating=excluded.content_rating, summary=excluded.summary,
			thumb=excluded.thumb, name=excluded.name, language=excluded.language, duration_ms=excluded.duration_ms,
			parts=excluded.parts, hubs=excluded.hubs, last_seen=excluded.last_seen, art=excluded.art, runtime_min=excluded.runtime_min,
			show_key=excluded.show_key, season=excluded.season`,
		t.Key, t.Origin, mediaKey, t.GUID, t.Title, t.Kind, t.Year, t.Released, string(genres), t.ContentRating,
		t.Summary, t.Thumb, t.Name, t.Language, t.DurationMs, string(parts), string(hubs), t.LastSeen, t.Art, t.RuntimeMin, t.ShowKey, t.Season)
	return err
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// MoviesWithoutExtras returns library movies whose extras were never looked up
// or not since before (new trailers appear for a while after a release).
func (s *Store) MoviesWithoutExtras(ctx context.Context, before time.Time) ([]*Media, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+selectCols("m.")+` FROM media m
		JOIN libraries l ON l.id = m.library_id WHERE l.enabled = 1 AND m.type = 'movie' AND m.extras_checked < ?
		ORDER BY m.extras_checked, m.added_at DESC`, before.Unix())
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

// SetLibraryTrailers replaces the trailers of a library movie and marks it as checked.
func (s *Store) SetLibraryTrailers(ctx context.Context, mediaKey string, trailers []*Trailer, checked time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM trailers WHERE media_key = ?`, mediaKey); err != nil {
		return err
	}
	for _, t := range trailers {
		if err := insertTrailer(ctx, tx, t); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media SET extras_checked = ? WHERE rating_key = ?`, checked.Unix(), mediaKey); err != nil {
		return err
	}
	return tx.Commit()
}

// ShowsWithoutExtras returns the keys of shows of enabled libraries whose
// extras were never looked up or not since before.
func (s *Store) ShowsWithoutExtras(ctx context.Context, before time.Time) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sd.key FROM show_details sd
		JOIN libraries l ON l.id = sd.library_id WHERE l.enabled = 1 AND sd.extras_checked < ?
		ORDER BY sd.extras_checked`, before.Unix())
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

// SetShowTrailers replaces the trailers of a library show and marks it as checked.
func (s *Store) SetShowTrailers(ctx context.Context, showKey string, trailers []*Trailer, checked time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM trailers WHERE show_key = ?`, showKey); err != nil {
		return err
	}
	for _, t := range trailers {
		if err := insertTrailer(ctx, tx, t); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE show_details SET extras_checked = ? WHERE key = ?`, checked.Unix(), showKey); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertTrailers writes trailers (used for Discover titles).
func (s *Store) UpsertTrailers(ctx context.Context, trailers []*Trailer) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range trailers {
		if err := insertTrailer(ctx, tx, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteStaleTrailers removes Discover trailers not seen in any list since
// before, unless they are still scheduled on a channel.
func (s *Store) DeleteStaleTrailers(ctx context.Context, origin string, before time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM trailers WHERE origin = ? AND last_seen < ?
		AND key NOT IN (SELECT rating_key FROM programs WHERE stop > ?) AND key NOT IN (SELECT note FROM programs WHERE stop > ?)`,
		origin, before.Unix(), time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Trailers returns all trailers (of enabled libraries for origin library).
func (s *Store) Trailers(ctx context.Context) ([]*Trailer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+trailerCols+` FROM trailers
		WHERE (media_key IS NULL AND show_key = '')
			OR media_key IN (SELECT m.rating_key FROM media m JOIN libraries l ON l.id = m.library_id WHERE l.enabled = 1)
			OR show_key IN (SELECT sd.key FROM show_details sd JOIN libraries l ON l.id = sd.library_id WHERE l.enabled = 1)
		ORDER BY title, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Trailer
	for rows.Next() {
		t, err := scanTrailer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTrailer(ctx context.Context, key string) (*Trailer, error) {
	t, err := scanTrailer(s.DB.QueryRowContext(ctx, `SELECT `+trailerCols+` FROM trailers WHERE key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// TrailerStats counts trailers per origin and language.
type TrailerStats struct {
	Origin   string `json:"origin"`
	Language string `json:"language"`
	Count    int    `json:"count"`
	Titles   int    `json:"titles"`
}

func (s *Store) TrailerStats(ctx context.Context) ([]TrailerStats, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT origin, language, COUNT(*), COUNT(DISTINCT guid || COALESCE(media_key, '') || show_key) FROM trailers
		GROUP BY origin, language ORDER BY origin, COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrailerStats{}
	for rows.Next() {
		var t TrailerStats
		if err := rows.Scan(&t.Origin, &t.Language, &t.Count, &t.Titles); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// iso639 maps two-letter and bibliographic codes to the terminology codes Plex uses for audio tracks.
var iso639 = map[string]string{
	"de": "deu", "ger": "deu", "en": "eng", "fr": "fra", "fre": "fra", "es": "spa", "it": "ita",
	"nl": "nld", "dut": "nld", "pt": "por", "pl": "pol", "ru": "rus", "tr": "tur", "ja": "jpn",
	"ko": "kor", "zh": "zho", "chi": "zho", "sv": "swe", "da": "dan", "no": "nor", "fi": "fin",
	"cs": "ces", "cze": "ces", "hu": "hun", "el": "ell", "gre": "ell", "hi": "hin", "ar": "ara",
}

func normLang(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if c, ok := iso639[code]; ok {
		return c
	}
	return code
}

// LanguageRank returns the position of a language in the preference list
// (-1 = not wanted). Without preferences every language ranks 0.
func LanguageRank(code string, prefs []string) int {
	if len(prefs) == 0 {
		return 0
	}
	c := normLang(code)
	for i, p := range prefs {
		if normLang(p) == c {
			return i
		}
	}
	return -1
}

// LanguageTag returns the two-letter code of a language ("deu" -> "de"), as
// Plex's online services expect it.
func LanguageTag(code string) string {
	c := normLang(code)
	for two, three := range iso639 {
		if three == c && len(two) == 2 {
			return two
		}
	}
	return c
}

// MediaGUIDs returns the plex:// guids of all library items.
func (s *Store) MediaGUIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT guid FROM media WHERE guid != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out[g] = true
	}
	return out, rows.Err()
}
