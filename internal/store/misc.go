package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"strconv"
	"time"
)

// ---- settings ----

// SetSetting upserts a key/value setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// GetSetting reads a setting, returning def when unset.
func (s *Store) GetSetting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	return v, err
}

// GetSettingBool reads a boolean setting.
func (s *Store) GetSettingBool(ctx context.Context, key string, def bool) (bool, error) {
	v, err := s.GetSetting(ctx, key, strconv.FormatBool(def))
	if err != nil {
		return def, err
	}
	return strconv.ParseBool(v)
}

// GetSettingInt reads an integer setting.
func (s *Store) GetSettingInt(ctx context.Context, key string, def int) (int, error) {
	v, err := s.GetSetting(ctx, key, strconv.Itoa(def))
	if err != nil {
		return def, err
	}
	return strconv.Atoi(v)
}

// ---- audit ----

// AddAudit appends an audit record.
func (s *Store) AddAudit(ctx context.Context, a *AuditEntry) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs(id, actor_id, actor_name, action, target_type,
		target_id, detail, ip, created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ActorID, a.ActorName, a.Action, a.TargetType, a.TargetID, a.Detail, a.IP, ts(a.CreatedAt))
	return err
}

// ListAudit returns the newest entries first.
func (s *Store) ListAudit(ctx context.Context, limit, offset int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, actor_id, actor_name, action, target_type, target_id,
		detail, ip, created_at FROM audit_logs ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		var created int64
		if err := rows.Scan(&a.ID, &a.ActorID, &a.ActorName, &a.Action, &a.TargetType, &a.TargetID,
			&a.Detail, &a.IP, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = fromTS(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- login attempts / brute-force protection ----

// AddLoginAttempt records an authentication attempt.
func (s *Store) AddLoginAttempt(ctx context.Context, username, ip string, success bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO login_attempts(id, username, ip, success, created_at)
		VALUES(?,?,?,?,?)`, ids.Hex(12), username, ip, boolInt(success), ts(time.Now()))
	return err
}

// RecentFailures counts failed logins for username+ip within window.
func (s *Store) RecentFailures(ctx context.Context, username, ip string, window time.Duration) (int, error) {
	since := time.Now().Add(-window).Unix()
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_attempts
		WHERE username=? AND ip=? AND success=0 AND created_at>=?`, username, ip, since).Scan(&n)
	return n, err
}

// PruneLoginAttempts deletes attempts older than window.
func (s *Store) PruneLoginAttempts(ctx context.Context, window time.Duration) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE created_at < ?`,
		time.Now().Add(-window).Unix())
	return err
}
