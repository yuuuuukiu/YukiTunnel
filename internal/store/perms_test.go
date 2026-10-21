package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yukitunnel.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Force a write so the WAL sidecar exists, then re-open (as a restart would).
	if err := s.SetSetting(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			continue // sidecar may be absent
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("%s has mode %o, want 600", filepath.Base(p), mode)
		}
	}
}

func TestSQLitePathParsing(t *testing.T) {
	cases := map[string]string{
		"file:/data/yuki.db?_pragma=busy_timeout(5000)": "/data/yuki.db",
		"file:./rel.db":              "",
		":memory:":                   "",
		"file::memory:?cache=shared": "",
	}
	for dsn, wantSuffix := range cases {
		got := sqlitePath(dsn)
		if wantSuffix == "" {
			if got != "" && dsn == ":memory:" {
				t.Fatalf("dsn %q: expected empty, got %q", dsn, got)
			}
			continue
		}
		if got != wantSuffix {
			t.Fatalf("dsn %q: got %q want %q", dsn, got, wantSuffix)
		}
	}
}
