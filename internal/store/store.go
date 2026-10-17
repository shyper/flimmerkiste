package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	DB *sql.DB
}

func Open(dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "flimmerkiste.db")
	// All transactions here write. _txlock=immediate takes the write lock at BEGIN,
	// so concurrent writers wait (busy_timeout) instead of failing with
	// SQLITE_BUSY when a transaction that has read upgrades to writing.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite serialises writers anyway; a small pool keeps memory low.
	db.SetMaxOpenConns(4)
	s := &Store{DB: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// migrations build the database step by step; append new ones, never change
// one that has shipped.
var migrations = []string{
	`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	-- provider and external_id name the account: "plex" and the plex.tv id,
	-- or "jellyfin"/"emby" and the user id on that server
	CREATE TABLE users (
		id          INTEGER PRIMARY KEY,
		provider    TEXT NOT NULL DEFAULT 'plex',
		external_id TEXT NOT NULL,
		username    TEXT NOT NULL,
		email       TEXT NOT NULL DEFAULT '',
		thumb       TEXT NOT NULL DEFAULT '',
		role        TEXT NOT NULL DEFAULT 'viewer',
		created_at  INTEGER NOT NULL,
		last_login  INTEGER NOT NULL,
		UNIQUE (provider, external_id)
	);
	CREATE TABLE sessions (
		token      TEXT PRIMARY KEY,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at INTEGER NOT NULL
	);
	CREATE TABLE libraries (
		id        TEXT PRIMARY KEY,
		title     TEXT NOT NULL,
		type      TEXT NOT NULL,
		enabled   INTEGER NOT NULL DEFAULT 1,
		synced_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE media (
		rating_key      TEXT PRIMARY KEY,
		library_id      TEXT NOT NULL,
		type            TEXT NOT NULL,
		title           TEXT NOT NULL,
		sort_title      TEXT NOT NULL DEFAULT '',
		year            INTEGER NOT NULL DEFAULT 0,
		duration_ms     INTEGER NOT NULL DEFAULT 0,
		summary         TEXT NOT NULL DEFAULT '',
		thumb           TEXT NOT NULL DEFAULT '',
		art             TEXT NOT NULL DEFAULT '',
		content_rating  TEXT NOT NULL DEFAULT '',
		rating          REAL NOT NULL DEFAULT 0,
		studio          TEXT NOT NULL DEFAULT '',
		added_at        INTEGER NOT NULL DEFAULT 0,
		released        TEXT NOT NULL DEFAULT '',
		view_count      INTEGER NOT NULL DEFAULT 0,
		show_key        TEXT NOT NULL DEFAULT '',
		show_title      TEXT NOT NULL DEFAULT '',
		show_thumb      TEXT NOT NULL DEFAULT '',
		season          INTEGER NOT NULL DEFAULT 0,
		episode         INTEGER NOT NULL DEFAULT 0,
		part_key        TEXT NOT NULL DEFAULT '',
		container       TEXT NOT NULL DEFAULT '',
		video_codec     TEXT NOT NULL DEFAULT '',
		audio_codec     TEXT NOT NULL DEFAULT '',
		resolution      TEXT NOT NULL DEFAULT '',
		tags            TEXT NOT NULL DEFAULT '{}',
		synced_at       INTEGER NOT NULL DEFAULT 0,
		guid            TEXT NOT NULL DEFAULT '',
		extras_checked  INTEGER NOT NULL DEFAULT 0,
		ext             TEXT NOT NULL DEFAULT '{}' -- external ids (tmdb, imdb, tvdb), to find titles again after an import
	);
	CREATE INDEX media_show ON media(show_key, season, episode);
	CREATE INDEX media_library ON media(library_id);
	CREATE TABLE channels (
		id         INTEGER PRIMARY KEY,
		number     INTEGER NOT NULL UNIQUE,
		name       TEXT NOT NULL,
		enabled    INTEGER NOT NULL DEFAULT 1,
		kind       TEXT NOT NULL,
		logo       TEXT NOT NULL DEFAULT '',
		color      TEXT NOT NULL DEFAULT '',
		config     TEXT NOT NULL DEFAULT '{}',
		state      TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	-- kind: "" = main program, "filler" = short clip between titles, "card" = slate,
	-- "preview" = slot for a trailer; source_id: the scheduler source that produced
	-- it ("main", "season:x", …), needed to roll back its state
	CREATE TABLE programs (
		id         INTEGER PRIMARY KEY,
		channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
		start      INTEGER NOT NULL,
		stop       INTEGER NOT NULL,
		rating_key TEXT NOT NULL,
		source     TEXT NOT NULL DEFAULT '',
		note       TEXT NOT NULL DEFAULT '',
		kind       TEXT NOT NULL DEFAULT '',
		source_id  TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX programs_channel_time ON programs(channel_id, start);
	-- Trailers: extras of library movies (origin "library"), of titles from Plex
	-- Discover lists ("discover") and TMDb slates ("tmdb"). Nothing is stored but
	-- their description; they stream from the media server or the internet.
	CREATE TABLE trailers (
		key            TEXT PRIMARY KEY,
		origin         TEXT NOT NULL,
		media_key      TEXT REFERENCES media(rating_key) ON DELETE CASCADE,
		guid           TEXT NOT NULL DEFAULT '',
		title          TEXT NOT NULL,
		kind           TEXT NOT NULL DEFAULT 'movie',
		year           INTEGER NOT NULL DEFAULT 0,
		released       TEXT NOT NULL DEFAULT '',
		genres         TEXT NOT NULL DEFAULT '[]',
		content_rating TEXT NOT NULL DEFAULT '',
		summary        TEXT NOT NULL DEFAULT '',
		thumb          TEXT NOT NULL DEFAULT '',
		name           TEXT NOT NULL DEFAULT '',
		language       TEXT NOT NULL DEFAULT '',
		duration_ms    INTEGER NOT NULL,
		parts          TEXT NOT NULL DEFAULT '[]',
		hubs           TEXT NOT NULL DEFAULT '[]',
		last_seen      INTEGER NOT NULL DEFAULT 0,
		art            TEXT NOT NULL DEFAULT '',  -- TMDb slates: backdrop of the advertised movie
		runtime_min    INTEGER NOT NULL DEFAULT 0 -- and its running time
	);
	CREATE INDEX trailers_media ON trailers(media_key);
	CREATE INDEX trailers_guid ON trailers(guid);`,
	// Full metadata of single items: media server listings shorten the cast,
	// so each movie is fetched on its own now and then (see SetMediaDetails).
	`ALTER TABLE media ADD COLUMN detail_tags TEXT NOT NULL DEFAULT '{}';
	ALTER TABLE media ADD COLUMN details TEXT NOT NULL DEFAULT '';
	ALTER TABLE media ADD COLUMN details_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE media ADD COLUMN details_due INTEGER NOT NULL DEFAULT 0;
	CREATE INDEX media_details_due ON media(library_id, details_due);`,
	// The same for shows; their episodes get the tags of the show.
	`CREATE TABLE show_details (
		key         TEXT PRIMARY KEY, -- media.show_key
		library_id  TEXT NOT NULL,
		detail_tags TEXT NOT NULL DEFAULT '{}',
		details     TEXT NOT NULL DEFAULT '',
		details_at  INTEGER NOT NULL DEFAULT 0,
		details_due INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX show_details_due ON show_details(library_id, details_due);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err := s.DB.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
