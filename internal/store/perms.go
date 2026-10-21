package store

import (
	"os"
	"path/filepath"
	"strings"
)

// restrictDatabaseFiles best-effort chmods the SQLite database (and its WAL
// sidecars) to 0600 so password hashes and token hashes are not world-readable.
func restrictDatabaseFiles(dsn string) {
	path := sqlitePath(dsn)
	if path == "" {
		return
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(p, 0o600)
	}
}

// sqlitePath extracts the file path from a modernc.org/sqlite DSN such as
// "file:/data/yukitunnel.db?_pragma=...".
func sqlitePath(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" || s == ":memory:" || strings.Contains(s, "mode=memory") {
		return ""
	}
	s = strings.TrimPrefix(s, "file:")
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if s == "" || strings.HasPrefix(s, "//") {
		return ""
	}
	if abs, err := filepath.Abs(s); err == nil {
		return abs
	}
	return s
}
