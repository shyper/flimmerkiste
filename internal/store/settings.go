package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Setting keys. Kept in one place so the API layer can whitelist them.
const (
	SetInstanceID      = "instance_id" // X-Plex-Client-Identifier and HDHomeRun DeviceID source
	SetOwnerID         = "owner_id"    // account that claimed this instance (plex.tv id or the server's user id)
	SetServerID        = "server_id"   // machineIdentifier of the linked Plex server
	SetServerName      = "server_name"
	SetServerURL       = "server_url"
	SetServerToken     = "server_token" // token used to talk to the Plex server
	SetTunerCount      = "tuner_count"
	SetNormalizeRes    = "normalize_res"   // 720 or 1080
	SetAudioLanguages  = "audio_languages" // e.g. "deu,ger,eng"
	SetSyncInterval    = "sync_interval_h"
	SetLastSync        = "last_sync"
	SetAIProvider      = "ai_provider" // "", "anthropic", "openai"
	SetAIKey           = "ai_api_key"
	SetAIModel         = "ai_model"
	SetAIBaseURL       = "ai_base_url"   // OpenAI-compatible endpoint (Ollama, OpenRouter, ...)
	SetAICandidates    = "ai_candidates" // titles offered to the model per request (prompt size, answer time)
	SetFriendlyName    = "friendly_name"
	SetAllowSharedUser = "allow_shared_users" // "1": users with access to the server may log in as viewers
	SetEPGPrefix       = "epg_prefix"         // optional prefix for XMLTV channel ids, avoids clashes between EPG sources
	SetOwnerToken      = "owner_token"        // plex.tv token of the owner (server list, Plex Discover)
	SetStreamBuffer    = "stream_buffer_s"    // seconds the stream runs ahead of real time
	SetHWAccel         = "hw_accel"           // "auto": GPU encoding when available, "off": software only
	SetHWDecode        = "hw_decode"          // "auto": the GPU also decodes if fast enough (startup test), "off": the CPU decodes
	SetTranscode       = "default_transcode"  // conversion of channels set to "default": copy | audio | full
	SetTrailerHubs     = "trailer_hubs"       // Plex Discover lists to take trailers from, comma separated ("" = off)
	SetTrailerRefresh  = "trailer_refresh"    // unix time of the last Discover refresh
	SetTrailerCaption  = "trailer_caption"    // "1": lower third with title and air time over trailers (full mode only)
	SetCardMusic       = "card_music"         // "1": background music on the "up next" slate, "0": silence
	SetCredits         = "credits_mode"       // channels set to "default": off | squeeze (end credits small, info panel; full mode only)
	SetPathMap         = "path_map"           // JSON: Plex library folder -> the same folder mounted in the container
	SetMediaServer     = "media_server_type"  // plex (default), jellyfin or emby
	SetServerUserID    = "server_user_id"     // Jellyfin/Emby: the owner's user id (watched state, libraries)
	SetTMDbKey         = "tmdb_key"           // TMDb API key for "coming to theaters" slates (Jellyfin/Emby)
	SetTMDbRegion      = "tmdb_region"        // region of theatrical releases ("" = from the language)
	SetTMDbRefresh     = "tmdb_refresh"       // unix time of the last slate refresh
	SetSlateSeconds    = "slate_seconds"      // length of a slate
	SetWeatherPlace    = "weather_place"      // JSON weather.Place of the weather slates (Jellyfin/Emby); "" = off
	SetNewsFeeds       = "news_feeds"         // RSS/Atom feeds of the news slates, one per line (Jellyfin/Emby); "" = off
	SetLanguage        = "language"           // language of the installation (de, en); unset until the owner claims it
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Setting returns a setting, or def when it is unset or empty: saving an empty
// value restores the default (an empty audio language list means "deu,ger").
func (s *Store) Setting(ctx context.Context, key, def string) string {
	v, err := s.GetSetting(ctx, key)
	if err != nil || v == "" {
		return def
	}
	return v
}

func (s *Store) SettingInt(ctx context.Context, key string, def int) int {
	if n, err := strconv.Atoi(s.Setting(ctx, key, "")); err == nil {
		return n
	}
	return def
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- users & sessions ----

type User struct {
	ID int64 `json:"id"`
	// Provider and ExternalID name the account: "plex" and the plex.tv id,
	// or "jellyfin"/"emby" and the user id on that server.
	Provider   string `json:"provider"`
	ExternalID string `json:"externalId"`
	Username   string `json:"username"`
	Email      string `json:"email"`
	Thumb      string `json:"thumb"`
	Role       string `json:"role"`
	CreatedAt  int64  `json:"createdAt"`
	LastLogin  int64  `json:"lastLogin"`
}

func (u *User) IsAdmin() bool { return u.Role == "admin" }

const userCols = `id, provider, external_id, username, email, thumb, role, created_at, last_login`

func scanUser(sc interface{ Scan(...any) error }, u *User) error {
	return sc.Scan(&u.ID, &u.Provider, &u.ExternalID, &u.Username, &u.Email, &u.Thumb, &u.Role, &u.CreatedAt, &u.LastLogin)
}

func (s *Store) UpsertUser(ctx context.Context, u *User) error {
	now := time.Now().Unix()
	if u.Provider == "" {
		u.Provider = "plex"
	}
	row := s.DB.QueryRowContext(ctx, `
		INSERT INTO users (provider, external_id, username, email, thumb, role, created_at, last_login)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, external_id) DO UPDATE SET username = excluded.username, email = excluded.email,
			thumb = excluded.thumb, last_login = excluded.last_login
		RETURNING id, role, created_at`,
		u.Provider, u.ExternalID, u.Username, u.Email, u.Thumb, u.Role, now, now)
	u.LastLogin = now
	return row.Scan(&u.ID, &u.Role, &u.CreatedAt)
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := scanUser(rows, &u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	return affected(s.DB.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id))
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return affected(s.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id))
}

// affected turns a statement that changed no row into ErrNotFound.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSession stores a session; like API tokens only the hash of the cookie
// value is kept, so a copy of the database signs nobody in.
func (s *Store) CreateSession(ctx context.Context, token string, userID int64, ttl time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash(token), userID, time.Now().Add(ttl).Unix())
	return err
}

// SessionUser returns the user of a session. Sessions stored before their
// hashes were kept still work until they expire.
func (s *Store) SessionUser(ctx context.Context, token string) (*User, error) {
	var u User
	err := scanUser(s.DB.QueryRowContext(ctx, `
		SELECT `+prefixed("u.", userCols)+`
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token IN (?, ?) AND s.expires_at > ?`, tokenHash(token), token, time.Now().Unix()), &u)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token IN (?, ?)`, tokenHash(token), token)
	return err
}

// DeleteOtherSessions signs everyone out except the user with keepID.
func (s *Store) DeleteOtherSessions(ctx context.Context, keepID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id != ?`, keepID)
	return err
}

func (s *Store) PurgeSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
