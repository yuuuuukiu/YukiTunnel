package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateAPIToken stores a hashed bearer token.
func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.ExpiresAt.IsZero() {
		t.ExpiresAt = now.Add(7 * 24 * time.Hour)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens(id, user_id, name, token_hash, scope,
		created_at, expires_at) VALUES(?,?,?,?,?,?,?)`,
		t.ID, t.UserID, t.Name, t.TokenHash, t.Scope, ts(t.CreatedAt), ts(t.ExpiresAt))
	return err
}

// GetAPIToken returns a token row by id (used for revocation checks).
func (s *Store) GetAPIToken(ctx context.Context, id string) (*APIToken, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, user_id, name, scope, created_at, expires_at, last_used_at, revoked_at
		FROM api_tokens WHERE id=?`, id)
	var t APIToken
	var created, expires, lastUsed, revoked int64
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Scope, &created, &expires, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt, t.ExpiresAt = fromTS(created), fromTS(expires)
	t.LastUsedAt, t.RevokedAt = fromTS(lastUsed), fromTS(revoked)
	return &t, nil
}

// FindAPITokenByHash looks up an active token by its SHA-256 hash.
func (s *Store) FindAPITokenByHash(ctx context.Context, hash string) (*APIToken, *User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT t.id, t.user_id, t.name, t.scope, t.created_at, t.expires_at,
		t.last_used_at, t.revoked_at,
		u.id, u.username, u.display_name, u.password_hash, u.role, u.disabled, u.created_at, u.updated_at, u.last_login_at
		FROM api_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=?`, hash)
	var t APIToken
	var created, expires, lastUsed, revoked int64
	var u User
	var disabled int
	var uCreated, uUpdated, uLast int64
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Scope, &created, &expires, &lastUsed, &revoked,
		&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Role, &disabled, &uCreated, &uUpdated, &uLast)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	t.CreatedAt, t.ExpiresAt, t.LastUsedAt, t.RevokedAt = fromTS(created), fromTS(expires), fromTS(lastUsed), fromTS(revoked)
	u.Disabled = disabled != 0
	u.CreatedAt, u.UpdatedAt, u.LastLoginAt = fromTS(uCreated), fromTS(uUpdated), fromTS(uLast)
	return &t, &u, nil
}

// TouchAPIToken updates last_used_at.
func (s *Store) TouchAPIToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=? WHERE id=?`, ts(time.Now()), id)
	return err
}

// RevokeAPIToken marks a token revoked.
func (s *Store) RevokeAPIToken(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=? WHERE id=? AND revoked_at=0`,
		ts(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAPITokens returns token metadata for a user (never the hash).
func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	q := `SELECT id, user_id, name, scope, created_at, expires_at, last_used_at, revoked_at FROM api_tokens`
	var args []any
	if userID != "" {
		q += ` WHERE user_id=?`
		args = append(args, userID)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created, expires, lastUsed, revoked int64
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Scope, &created, &expires, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		t.CreatedAt, t.ExpiresAt, t.LastUsedAt, t.RevokedAt = fromTS(created), fromTS(expires), fromTS(lastUsed), fromTS(revoked)
		out = append(out, t)
	}
	return out, rows.Err()
}
