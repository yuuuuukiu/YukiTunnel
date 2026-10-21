// Package store is the persistence layer. It uses SQLite (pure Go driver) so a
// single binary can be deployed with no external database.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by every Get* helper when no row matches.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite handle.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at dsn.
func Open(dsn string) (*Store, error) {
	// The database stores password hashes, so create it readable only by the
	// service user (SQLite honours the process umask).
	restore := privateUmask()
	defer restore()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is single-writer; keeping the pool small avoids SQLITE_BUSY storms.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	// Tighten permissions on files created by older builds (the umask above
	// only applies to files created by this process).
	restrictDatabaseFiles(dsn)
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for advanced queries (used by tests).
func (s *Store) DB() *sql.DB { return s.db }

const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id            TEXT PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  display_name  TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'user',
  disabled      INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  last_login_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_keys (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL UNIQUE,
  public_key  TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_user_keys_user ON user_keys(user_id);

CREATE TABLE IF NOT EXISTS api_tokens (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL DEFAULT '',
  token_hash TEXT NOT NULL UNIQUE,
  scope      TEXT NOT NULL DEFAULT 'user',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  revoked_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);

CREATE TABLE IF NOT EXISTS nodes (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  region      TEXT NOT NULL DEFAULT '',
  public_host TEXT NOT NULL,
  control_host TEXT NOT NULL DEFAULT '',
  control_port INTEGER NOT NULL DEFAULT 9443,
  enabled     INTEGER NOT NULL DEFAULT 1,
  status      TEXT NOT NULL DEFAULT 'offline',
  latency_ms  INTEGER NOT NULL DEFAULT 0,
  last_seen_at INTEGER NOT NULL DEFAULT 0,
  max_tunnels INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tunnels (
  id             TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  node_id        TEXT NOT NULL REFERENCES nodes(id),
  name           TEXT NOT NULL,
  proto          TEXT NOT NULL,
  local_ip       TEXT NOT NULL,
  local_port     INTEGER NOT NULL,
  public_port    INTEGER NOT NULL DEFAULT 0,
  domain         TEXT NOT NULL DEFAULT '',
  enabled        INTEGER NOT NULL DEFAULT 1,
  bandwidth_kbps INTEGER NOT NULL DEFAULT 0,
  max_connections INTEGER NOT NULL DEFAULT 0,
  desired_running INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  last_started_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tunnels_user ON tunnels(user_id);
CREATE INDEX IF NOT EXISTS idx_tunnels_node ON tunnels(node_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tunnels_port ON tunnels(node_id, public_port) WHERE public_port > 0;

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id          TEXT PRIMARY KEY,
  actor_id    TEXT NOT NULL DEFAULT '',
  actor_name  TEXT NOT NULL DEFAULT '',
  action      TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id   TEXT NOT NULL DEFAULT '',
  detail      TEXT NOT NULL DEFAULT '',
  ip          TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at DESC);

CREATE TABLE IF NOT EXISTS cloud_profiles (
  id          TEXT PRIMARY KEY,
  vendor      TEXT NOT NULL DEFAULT '',
  name        TEXT NOT NULL DEFAULT '',
  version     TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT '',
  sha256      TEXT NOT NULL DEFAULT '',
  document    TEXT NOT NULL,
  enabled     INTEGER NOT NULL DEFAULT 1,
  installed_at INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS profile_credentials (
  id          TEXT PRIMARY KEY,
  profile_id  TEXT NOT NULL,
  label       TEXT NOT NULL DEFAULT '',
  fields_json TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_profile_cred_profile ON profile_credentials(profile_id, label);

CREATE TABLE IF NOT EXISTS profile_runs (
  id          TEXT PRIMARY KEY,
  profile_id  TEXT NOT NULL,
  profile_sha TEXT NOT NULL DEFAULT '',
  action      TEXT NOT NULL,
  node_id     TEXT NOT NULL DEFAULT '',
  port_min    INTEGER NOT NULL DEFAULT 0,
  port_max    INTEGER NOT NULL DEFAULT 0,
  protocol    TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT '',
  message     TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  actor_id    TEXT NOT NULL DEFAULT '',
  actor_name  TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_profile_runs_created ON profile_runs(created_at DESC);

CREATE TABLE IF NOT EXISTS login_attempts (
  id         TEXT PRIMARY KEY,
  username   TEXT NOT NULL,
  ip         TEXT NOT NULL,
  success    INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_login_attempts ON login_attempts(username, ip, created_at DESC);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Additive migrations for databases created by earlier builds.
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE tunnels ADD COLUMN desired_running INTEGER NOT NULL DEFAULT 0`)
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().Unix())
	return err
}
