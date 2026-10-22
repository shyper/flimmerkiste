package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
)

// APIToken lets a program (an MCP client) act as a user without a session,
// restricted to its scopes.
type APIToken struct {
	ID        int64    `json:"id"`
	UserID    int64    `json:"userId"`
	Username  string   `json:"username"`
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	CreatedAt int64    `json:"createdAt"`
	LastUsed  int64    `json:"lastUsed"`
}

func (t *APIToken) Has(scope string) bool { return slices.Contains(t.Scopes, scope) }

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateAPIToken stores a new token; the caller shows token once, only its hash is kept.
func (s *Store) CreateAPIToken(ctx context.Context, userID int64, name, token string, scopes []string) (*APIToken, error) {
	t := &APIToken{UserID: userID, Name: name, Scopes: scopes, CreatedAt: time.Now().Unix()}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO api_tokens (user_id, name, hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, tokenHash(token), strings.Join(scopes, " "), t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.ID, err = res.LastInsertId()
	return t, err
}

const tokenCols = `t.id, t.user_id, u.username, t.name, t.scopes, t.created_at, t.last_used`

func scanToken(sc interface{ Scan(...any) error }) (*APIToken, error) {
	var t APIToken
	var scopes string
	if err := sc.Scan(&t.ID, &t.UserID, &t.Username, &t.Name, &scopes, &t.CreatedAt, &t.LastUsed); err != nil {
		return nil, err
	}
	t.Scopes = strings.Fields(scopes)
	return &t, nil
}

func (s *Store) ListAPITokens(ctx context.Context) ([]*APIToken, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+tokenCols+` FROM api_tokens t JOIN users u ON u.id = t.user_id ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TokenUser returns the token and its user, and notes the use (at most once a minute).
func (s *Store) TokenUser(ctx context.Context, token string) (*APIToken, *User, error) {
	var u User
	row := s.DB.QueryRowContext(ctx, `SELECT `+tokenCols+`, `+prefixed("u.", userCols)+`
		FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE t.hash = ?`, tokenHash(token))
	var t APIToken
	var scopes string
	err := row.Scan(&t.ID, &t.UserID, &t.Username, &t.Name, &scopes, &t.CreatedAt, &t.LastUsed,
		&u.ID, &u.Provider, &u.ExternalID, &u.Username, &u.Email, &u.Thumb, &u.Role, &u.CreatedAt, &u.LastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	t.Scopes = strings.Fields(scopes)
	if now := time.Now().Unix(); now-t.LastUsed >= 60 {
		_, _ = s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used = ? WHERE id = ?`, now, t.ID)
		t.LastUsed = now
	}
	return &t, &u, nil
}

func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOtherTokens removes the tokens of everyone except the user with keepID.
func (s *Store) DeleteOtherTokens(ctx context.Context, keepID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id != ?`, keepID)
	return err
}
