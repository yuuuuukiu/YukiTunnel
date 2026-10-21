package store

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialsEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	st, err := Open("file:" + filepath.Join(dir, "yuki.db") + "?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	secret := "SUPER-SECRET-ACCESS-KEY"
	id := "cred-1"
	sealed, err := SealCredentials(dir, map[string]string{
		"access_key_id":     "AKIAEXAMPLE123",
		"access_key_secret": secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("sealed blob contains plaintext")
	}
	if err := st.UpsertProfileCredential(context.Background(), &ProfileCredential{
		ID: id, ProfileID: "aliyun-ecs-security-group", Label: "prod", FieldsJSON: sealed,
	}); err != nil {
		t.Fatal(err)
	}

	// Read the raw column, bypassing the API, to prove nothing is stored in clear.
	var raw string
	if err := st.DB().QueryRow(`SELECT fields_json FROM profile_credentials WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, secret) {
		t.Fatalf("database stores plaintext secret: %s", raw)
	}
	if strings.Contains(raw, "AKIAEXAMPLE123") {
		t.Fatalf("database stores plaintext access key id: %s", raw)
	}
	// Also scan the whole database file: SQLite pages must not leak it either.
	dbBytes, err := os.ReadFile(filepath.Join(dir, "yuki.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dbBytes), secret) {
		t.Fatal("database file contains the plaintext secret")
	}

	// Round-trip still works.
	fields, err := OpenCredentials(dir, raw)
	if err != nil {
		t.Fatal(err)
	}
	if fields["access_key_secret"] != secret {
		t.Fatalf("decrypted = %q", fields["access_key_secret"])
	}
}

func TestCredentialKeyFromEnv(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	t.Setenv("YUKI_CREDENTIAL_KEY", key)
	dir := t.TempDir()
	sealed, err := SealCredentials(dir, map[string]string{"secret_id": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	// A different data dir with the same env key must decrypt successfully:
	// the key comes from the environment, not the directory.
	other := t.TempDir()
	fields, err := OpenCredentials(other, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if fields["secret_id"] != "abc" {
		t.Fatalf("fields = %v", fields)
	}
}

func TestCredentialKeyRejectsShortEnvKey(t *testing.T) {
	t.Setenv("YUKI_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := SealCredentials(t.TempDir(), map[string]string{"a": "b"}); err == nil {
		t.Fatal("short key must be rejected")
	}
}

func TestCredentialKeyFilePermissions(t *testing.T) {
	dir := t.TempDir()
	if _, err := SealCredentials(dir, map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "credential.key"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key file mode = %o, want 600", mode)
	}
}

func TestOpenCredentialsRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "v1:!!!", "v1:AAAA"} {
		if _, err := OpenCredentials(dir, bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestMaskCredentials(t *testing.T) {
	m := MaskCredentials(map[string]string{"access_key_id": "AKIAEXAMPLE", "access_key_secret": "s"})
	if m["access_key_id"] != "****" || m["access_key_secret"] != "****" {
		t.Fatalf("masked = %v", m)
	}
	if MaskSecret("AKIAEXAMPLE") != "AKIA****" {
		t.Fatalf("MaskSecret = %q", MaskSecret("AKIAEXAMPLE"))
	}
	if MaskSecret("ab") != "****" {
		t.Fatalf("short secret should be fully masked")
	}
}

func TestRotateCredentialKey(t *testing.T) {
	dir := t.TempDir()
	st, err := Open("file:" + filepath.Join(dir, "yuki.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sealed, err := SealCredentials(dir, map[string]string{"secret_id": "S1", "secret_key": "K1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProfileCredential(context.Background(), &ProfileCredential{
		ID: "c1", ProfileID: "p", Label: "default", FieldsJSON: sealed,
	}); err != nil {
		t.Fatal(err)
	}
	// Rotate from the file key to a brand-new env key.
	newKey := base64.StdEncoding.EncodeToString([]byte("ffffffffffffffffffffffffffffffff"))
	t.Setenv("YUKI_CREDENTIAL_KEY", newKey)
	n, err := st.RotateCredentialKey(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rotated %d rows", n)
	}
	var raw string
	if err := st.DB().QueryRow(`SELECT fields_json FROM profile_credentials WHERE id='c1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	fields, err := OpenCredentials(dir, raw)
	if err != nil {
		t.Fatal(err)
	}
	if fields["secret_key"] != "K1" {
		t.Fatalf("after rotation fields = %v", fields)
	}
}
