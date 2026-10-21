package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const userCols = `id, username, display_name, password_hash, role, disabled, created_at, updated_at, last_login_at`

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	var u User
	var disabled int
	var created, updated, lastLogin int64
	err := sc.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Role,
		&disabled, &created, &updated, &lastLogin)
	if err != nil {
		return nil, err
	}
	u.Disabled = disabled != 0
	u.CreatedAt, u.UpdatedAt, u.LastLoginAt = fromTS(created), fromTS(updated), fromTS(lastLogin)
	return &u, nil
}

// CreateUser inserts a user. The caller must hash the password first.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	if u.Role == "" {
		u.Role = "user"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(`+userCols+`)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash, u.Role,
		boolInt(u.Disabled), ts(u.CreatedAt), ts(u.UpdatedAt), ts(u.LastLoginAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("user %q: %w", u.Username, ErrConflict)
		}
		return err
	}
	return nil
}

// ErrConflict marks a uniqueness violation.
var ErrConflict = errors.New("already exists")

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetUserByUsername looks a user up case-insensitively.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// GetUser looks a user up by id.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// ListUsers returns all users ordered by creation time.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateUser persists mutable fields (display name, role, disabled, password).
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET display_name=?, password_hash=?, role=?, disabled=?, updated_at=? WHERE id=?`,
		u.DisplayName, u.PasswordHash, u.Role, boolInt(u.Disabled), ts(time.Now()), u.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLogin records a successful login timestamp.
func (s *Store) TouchLogin(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, ts(time.Now()), id)
	return err
}

// DeleteUser removes a user and, by cascade, its tunnels and keys.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAdmins returns the number of enabled admin accounts.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0`).Scan(&n)
	return n, err
}

// ---- user keys ----

// CreateUserKey registers an SSH public key for a user.
func (s *Store) CreateUserKey(ctx context.Context, k *UserKey) error {
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_keys(id, user_id, name, fingerprint, public_key, created_at)
		VALUES(?,?,?,?,?,?)`, k.ID, k.UserID, k.Name, k.Fingerprint, k.PublicKey, ts(k.CreatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrConflict
	}
	return err
}

// ListUserKeys lists keys for a user.
func (s *Store) ListUserKeys(ctx context.Context, userID string) ([]UserKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, fingerprint, public_key, created_at, last_used_at
		FROM user_keys WHERE user_id=? ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserKey
	for rows.Next() {
		var k UserKey
		var created, lastUsed int64
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Fingerprint, &k.PublicKey, &created, &lastUsed); err != nil {
			return nil, err
		}
		k.CreatedAt, k.LastUsedAt = fromTS(created), fromTS(lastUsed)
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteUserKey removes a key owned by userID.
func (s *Store) DeleteUserKey(ctx context.Context, userID, keyID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_keys WHERE id=? AND user_id=?`, keyID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FindUserByFingerprint returns the enabled user owning a key fingerprint.
func (s *Store) FindUserByFingerprint(ctx context.Context, fp string) (*User, *UserKey, error) {
	row := s.db.QueryRowContext(ctx, `SELECT k.id, k.user_id, k.name, k.fingerprint, k.public_key, k.created_at, k.last_used_at,
		u.id, u.username, u.display_name, u.password_hash, u.role, u.disabled, u.created_at, u.updated_at, u.last_login_at
		FROM user_keys k JOIN users u ON u.id = k.user_id
		WHERE k.fingerprint = ? AND u.disabled = 0`, fp)
	var k UserKey
	var kCreated, kUsed int64
	var u User
	var disabled int
	var uCreated, uUpdated, uLast int64
	err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.Fingerprint, &k.PublicKey, &kCreated, &kUsed,
		&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Role, &disabled, &uCreated, &uUpdated, &uLast)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	k.CreatedAt, k.LastUsedAt = fromTS(kCreated), fromTS(kUsed)
	u.Disabled = disabled != 0
	u.CreatedAt, u.UpdatedAt, u.LastLoginAt = fromTS(uCreated), fromTS(uUpdated), fromTS(uLast)
	return &u, &k, nil
}

// TouchUserKey records key usage.
func (s *Store) TouchUserKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE user_keys SET last_used_at=? WHERE id=?`, ts(time.Now()), id)
	return err
}
