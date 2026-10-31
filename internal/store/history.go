package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// HistoryEntry is a movie or episode that aired on a channel.
type HistoryEntry struct {
	ID          int64  `json:"id"`
	ChannelID   int64  `json:"channelId"`
	ChannelName string `json:"channelName"`
	Start       int64  `json:"start"` // unix milliseconds
	Stop        int64  `json:"stop"`
	RatingKey   string `json:"ratingKey"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	ShowTitle   string `json:"showTitle,omitempty"`
	Season      int    `json:"season,omitempty"`
	Episode     int    `json:"episode,omitempty"`
	Year        int    `json:"year,omitempty"`
	Watched     bool   `json:"watched"`
	Failed      bool   `json:"failed"`
	// Times counts how often the title aired in all, Previous is when it
	// aired the time before (0 = never), to judge how well the shuffle works.
	Times    int   `json:"times"`
	Previous int64 `json:"previous,omitempty"`
}

// RecordHistory adds the movies and episodes whose start has passed by now to
// the history. Programs still ahead are left alone: until they start the
// schedule may change them.
func (s *Store) RecordHistory(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT p.channel_id, c.name, p.start, p.stop, p.rating_key, m.type, m.title, m.show_title,
			m.season, m.episode, m.year
		FROM programs p JOIN channels c ON c.id = p.channel_id JOIN media m ON m.rating_key = p.rating_key
		LEFT JOIN history h ON h.channel_id = p.channel_id AND h.start = p.start
		WHERE p.start <= ? AND p.kind = '' AND m.type IN ('movie', 'episode') AND h.id IS NULL`, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	var add []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ChannelID, &e.ChannelName, &e.Start, &e.Stop, &e.RatingKey, &e.Type, &e.Title, &e.ShowTitle,
			&e.Season, &e.Episode, &e.Year); err != nil {
			rows.Close()
			return 0, err
		}
		add = append(add, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(add) == 0 {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, e := range add {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO history (channel_id, channel_name, start, stop, rating_key, type, title,
				show_title, season, episode, year, search) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ChannelID, e.ChannelName, e.Start, e.Stop, e.RatingKey, e.Type, e.Title, e.ShowTitle, e.Season, e.Episode, e.Year,
			strings.ToLower(e.Title+" "+e.ShowTitle)); err != nil {
			return 0, err
		}
	}
	return len(add), tx.Commit()
}

// MarkHistory notes that a stream of the channel sent the program starting at
// start, or that it failed and was replaced.
func (s *Store) MarkHistory(ctx context.Context, channelID, start int64, failed bool) error {
	mark := func() (int64, error) {
		res, err := s.DB.ExecContext(ctx, `UPDATE history SET watched = 1, failed = MAX(failed, ?) WHERE channel_id = ? AND start = ?`,
			failed, channelID, start)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	n, err := mark()
	if err != nil || n > 0 {
		return err
	}
	// not recorded yet: it has started, so record it now
	if _, err := s.RecordHistory(ctx, time.Now()); err != nil {
		return err
	}
	_, err = mark()
	return err
}

// LastWatched returns when a stream last sent the title (start of that
// program, unix milliseconds; 0 = never).
func (s *Store) LastWatched(ctx context.Context, ratingKey string) (int64, error) {
	var last sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT MAX(start) FROM history WHERE rating_key = ? AND watched = 1 AND failed = 0`,
		ratingKey).Scan(&last)
	return last.Int64, err
}

// HistoryQuery selects history entries, newest first.
type HistoryQuery struct {
	ChannelID int64  // 0 = all channels
	Search    string // part of the title or show
	From, To  int64  // start in [From, To) in unix milliseconds; 0 = open
	Before    int64  // only entries that started before (the next page); 0 = none
	// BeforeChannel is the channel of the last entry of the page before: with
	// it, Before also lets through later channels that started at the same time.
	BeforeChannel int64
	Limit         int
}

// PurgeHistoryBefore deletes what started before t.
func (s *Store) PurgeHistoryBefore(ctx context.Context, t time.Time) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM history WHERE start < ?`, t.UnixMilli())
	return err
}

// History returns history entries with how often each title aired.
func (s *Store) History(ctx context.Context, q HistoryQuery) ([]HistoryEntry, error) {
	where := []string{"1 = 1"}
	var args []any
	if q.ChannelID != 0 {
		where, args = append(where, "h.channel_id = ?"), append(args, q.ChannelID)
	}
	if q.Search = strings.TrimSpace(q.Search); q.Search != "" {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q.Search))
		where, args = append(where, `h.search LIKE ? ESCAPE '\'`), append(args, "%"+esc+"%")
	}
	if q.From != 0 {
		where, args = append(where, "h.start >= ?"), append(args, q.From)
	}
	if q.To != 0 {
		where, args = append(where, "h.start < ?"), append(args, q.To)
	}
	if q.Before != 0 && q.BeforeChannel != 0 {
		// several channels start on the same minute: continue right after the last entry
		where, args = append(where, "(h.start < ? OR h.start = ? AND h.channel_id > ?)"), append(args, q.Before, q.Before, q.BeforeChannel)
	} else if q.Before != 0 {
		where, args = append(where, "h.start < ?"), append(args, q.Before)
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT h.id, h.channel_id, h.channel_name, h.start, h.stop, h.rating_key, h.type, h.title,
			h.show_title, h.season, h.episode, h.year, h.watched, h.failed,
			(SELECT COUNT(*) FROM history t WHERE t.rating_key = h.rating_key),
			COALESCE((SELECT MAX(t.start) FROM history t WHERE t.rating_key = h.rating_key AND t.start < h.start), 0)
		FROM history h WHERE `+strings.Join(where, " AND ")+` ORDER BY h.start DESC, h.channel_id LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.ChannelID, &e.ChannelName, &e.Start, &e.Stop, &e.RatingKey, &e.Type, &e.Title, &e.ShowTitle,
			&e.Season, &e.Episode, &e.Year, &e.Watched, &e.Failed, &e.Times, &e.Previous); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
