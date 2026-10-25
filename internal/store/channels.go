package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Channel is stored with its configuration and scheduler state as opaque JSON;
// the schedule package owns their structure.
type Channel struct {
	ID        int64           `json:"id"`
	Number    int             `json:"number"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Kind      string          `json:"kind"`
	Logo      string          `json:"logo"`
	Color     string          `json:"color"`
	Config    json.RawMessage `json:"config"`
	State     json.RawMessage `json:"-"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

const channelCols = `id, number, name, enabled, kind, logo, color, config, state, created_at, updated_at`

func scanChannel(sc interface{ Scan(...any) error }) (*Channel, error) {
	var c Channel
	var cfg, state string
	if err := sc.Scan(&c.ID, &c.Number, &c.Name, &c.Enabled, &c.Kind, &c.Logo, &c.Color, &cfg, &state, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Config = json.RawMessage(cfg)
	c.State = json.RawMessage(state)
	return &c, nil
}

func (s *Store) ListChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+channelCols+` FROM channels ORDER BY number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	c, err := scanChannel(s.DB.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) GetChannelByNumber(ctx context.Context, number int) (*Channel, error) {
	c, err := scanChannel(s.DB.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE number = ?`, number))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) NextChannelNumber(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM channels`).Scan(&n)
	return n, err
}

func (s *Store) CreateChannel(ctx context.Context, c *Channel) error {
	now := time.Now().Unix()
	c.CreatedAt, c.UpdatedAt = now, now
	if len(c.Config) == 0 {
		c.Config = json.RawMessage("{}")
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO channels (number, name, enabled, kind, logo, color, config, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '{}', ?, ?)`, c.Number, c.Name, c.Enabled, c.Kind, c.Logo, c.Color, string(c.Config), now, now)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateChannel(ctx context.Context, c *Channel) error {
	c.UpdatedAt = time.Now().Unix()
	// ErrNotFound when the channel was deleted meanwhile
	return affected(s.DB.ExecContext(ctx, `UPDATE channels SET number=?, name=?, enabled=?, kind=?, logo=?, color=?, config=?, updated_at=? WHERE id=?`,
		c.Number, c.Name, c.Enabled, c.Kind, c.Logo, c.Color, string(c.Config), c.UpdatedAt, c.ID))
}

// SetChannelConfig rewrites the stored configuration (format migrations), leaving
// updated_at alone: the channel itself did not change.
func (s *Store) SetChannelConfig(ctx context.Context, id int64, config json.RawMessage) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE channels SET config = ? WHERE id = ?`, string(config), id)
	return err
}

func (s *Store) SaveChannelState(ctx context.Context, id int64, state json.RawMessage) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE channels SET state = ? WHERE id = ?`, string(state), id)
	return err
}

func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id)
	return err
}

// ---- programs (the materialised schedule) ----

type Program struct {
	ID        int64  `json:"id"`
	ChannelID int64  `json:"channelId"`
	Start     int64  `json:"start"` // unix milliseconds
	Stop      int64  `json:"stop"`
	RatingKey string `json:"ratingKey"`
	Source    string `json:"source,omitempty"` // which phase / source produced it
	Note      string `json:"note,omitempty"`   // e.g. AI reasoning
	Kind      string `json:"kind,omitempty"`   // "", "filler", "card" or "preview"
	SourceID  string `json:"-"`                // scheduler source that produced it
}

const (
	KindFiller = "filler"
	KindCard   = "card"
	CardKey    = "card" // rating key placeholder for slates
	// KindPreview is a slot for a trailer of a title airing later on the channel.
	// It is bound shortly before it airs (see Scheduler.BindPreviews), when the
	// guide ahead is known, and replaced by the trailer plus slates.
	KindPreview = "preview"
	PreviewKey  = "preview"
	// PromoKey marks the slate after a trailer ("Today at 10:00 PM …"). Note holds
	// the start (unix ms) of the advertised program, or the trailer key.
	PromoKey = "promo"
	// FaultKey marks the slate shown when a title cannot be played and nothing
	// else can fill its slot. Note holds the rating key of the failed title.
	FaultKey = "fault"
	// InfoPrefix marks weather and news slates in the Note of a PromoKey
	// program ("i:weather-now", "i:news-2"); they are drawn when they air.
	InfoPrefix = "i:"
)

// IsMain reports whether this is a real program (not a filler clip or slate).
func (p *Program) IsMain() bool { return p.Kind == "" }

func (p *Program) StartTime() time.Time { return time.UnixMilli(p.Start) }
func (p *Program) StopTime() time.Time  { return time.UnixMilli(p.Stop) }

func (s *Store) InsertPrograms(ctx context.Context, progs []Program) error {
	if len(progs) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO programs (channel_id, start, stop, rating_key, source, note, kind, source_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range progs {
		if _, err := stmt.ExecContext(ctx, p.ChannelID, p.Start, p.Stop, p.RatingKey, p.Source, p.Note, p.Kind, p.SourceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const programCols = `id, channel_id, start, stop, rating_key, source, note, kind, source_id`

func scanPrograms(rows *sql.Rows) ([]Program, error) {
	defer rows.Close()
	var out []Program
	for rows.Next() {
		var p Program
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Start, &p.Stop, &p.RatingKey, &p.Source, &p.Note, &p.Kind, &p.SourceID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProgramsBetween returns programs of a channel overlapping [from, to). channelID 0 = all channels.
func (s *Store) ProgramsBetween(ctx context.Context, channelID int64, from, to time.Time) ([]Program, error) {
	q := `SELECT ` + programCols + ` FROM programs WHERE stop > ? AND start < ?`
	args := []any{from.UnixMilli(), to.UnixMilli()}
	if channelID != 0 {
		q += ` AND channel_id = ?`
		args = append(args, channelID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY channel_id, start`, args...)
	if err != nil {
		return nil, err
	}
	return scanPrograms(rows)
}

// ProgramAt returns the program airing on a channel at time t.
func (s *Store) ProgramAt(ctx context.Context, channelID int64, t time.Time) (*Program, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+programCols+` FROM programs
		WHERE channel_id = ? AND start <= ? AND stop > ? ORDER BY start DESC LIMIT 1`, channelID, t.UnixMilli(), t.UnixMilli())
	if err != nil {
		return nil, err
	}
	ps, err := scanPrograms(rows)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, ErrNotFound
	}
	return &ps[0], nil
}

// LastProgram returns the latest scheduled program of a channel.
func (s *Store) LastProgram(ctx context.Context, channelID int64) (*Program, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+programCols+` FROM programs
		WHERE channel_id = ? ORDER BY stop DESC LIMIT 1`, channelID)
	if err != nil {
		return nil, err
	}
	ps, err := scanPrograms(rows)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, ErrNotFound
	}
	return &ps[0], nil
}

// RecentKeys returns rating keys aired on a channel since t (for repeat avoidance).
func (s *Store) RecentKeys(ctx context.Context, channelID int64, since time.Time) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT rating_key FROM programs WHERE channel_id = ? AND stop > ?`, channelID, since.UnixMilli())
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

// ReplaceProgram swaps one program for others covering the same time.
func (s *Store) ReplaceProgram(ctx context.Context, id int64, progs []Program) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM programs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // re-planned meanwhile
	}
	for _, p := range progs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO programs (channel_id, start, stop, rating_key, source, note, kind, source_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ChannelID, p.Start, p.Stop, p.RatingKey, p.Source, p.Note, p.Kind, p.SourceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteProgramsAfter drops all programs starting at or after t (used when a channel is edited).
func (s *Store) DeleteProgramsAfter(ctx context.Context, channelID int64, t time.Time) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM programs WHERE channel_id = ? AND start >= ?`, channelID, t.UnixMilli())
	return err
}

// TakeProgramsAfter deletes and returns all programs starting at or after t, in airing order.
func (s *Store) TakeProgramsAfter(ctx context.Context, channelID int64, t time.Time) ([]Program, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+programCols+` FROM programs WHERE channel_id = ? AND start >= ? ORDER BY start`, channelID, t.UnixMilli())
	if err != nil {
		return nil, err
	}
	progs, err := scanPrograms(rows)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM programs WHERE channel_id = ? AND start >= ?`, channelID, t.UnixMilli()); err != nil {
		return nil, err
	}
	return progs, tx.Commit()
}

// FirstProgramWith returns the start of the earliest program at or after t that airs one of the keys.
func (s *Store) FirstProgramWith(ctx context.Context, channelID int64, t time.Time, keys map[string]bool) (time.Time, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT start, stop, rating_key FROM programs WHERE channel_id = ? AND stop > ? ORDER BY start`, channelID, t.UnixMilli())
	if err != nil {
		return time.Time{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var start, stop int64
		var key string
		if err := rows.Scan(&start, &stop, &key); err != nil {
			return time.Time{}, false, err
		}
		if keys[key] {
			return time.UnixMilli(start), true, nil
		}
	}
	return time.Time{}, false, rows.Err()
}

func (s *Store) PurgeProgramsBefore(ctx context.Context, t time.Time) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM programs WHERE stop < ?`, t.UnixMilli())
	return err
}
