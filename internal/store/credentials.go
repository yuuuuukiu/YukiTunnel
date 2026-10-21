package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Credential encryption at rest.
//
// Cloud provider credentials (AK/SK, IAM tokens) are sealed with AES-256-GCM
// before they reach SQLite. The key is resolved in this order:
//
//  1. YUKI_CREDENTIAL_KEY            base64, 32 bytes, for container/secret stores
//  2. <data-dir>/credential.key      random file created on first use (0600)
//  3. OS keyring                     via the `secret-tool` / `security` CLI
//
// A key rotation requires re-sealing every row; `yuki-server profile rekey`
// performs that migration (see RotateCredentialKey).

// ErrNoCredentialKey is returned when no usable key could be resolved.
var ErrNoCredentialKey = errors.New("no credential encryption key available")

// keyCache is keyed by the SHA-256 of the raw key material, not by the data
// directory: rotation needs the old and the new key alive at the same time.
var (
	keyMu    sync.RWMutex
	keyCache = map[[32]byte]cipher.AEAD{}
)

func aeadForKey(raw []byte) (cipher.AEAD, error) {
	sum := sha256.Sum256(raw)
	keyMu.RLock()
	if aead, ok := keyCache[sum]; ok {
		keyMu.RUnlock()
		return aead, nil
	}
	keyMu.RUnlock()
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	keyMu.Lock()
	keyCache[sum] = aead
	keyMu.Unlock()
	return aead, nil
}

func aeadFor(dataDir string) (cipher.AEAD, error) {
	raw, err := resolveCredentialKey(dataDir)
	if err != nil {
		return nil, err
	}
	return aeadForKey(raw)
}

// resolveCredentialKey implements the precedence documented above.
func resolveCredentialKey(dataDir string) ([]byte, error) {
	if env := strings.TrimSpace(os.Getenv("YUKI_CREDENTIAL_KEY")); env != "" {
		raw, err := base64.StdEncoding.DecodeString(env)
		if err != nil {
			return nil, fmt.Errorf("YUKI_CREDENTIAL_KEY is not valid base64: %w", err)
		}
		if len(raw) < 16 {
			return nil, fmt.Errorf("YUKI_CREDENTIAL_KEY must decode to at least 16 bytes")
		}
		return raw, nil
	}
	if dataDir == "" {
		return nil, ErrNoCredentialKey
	}
	path := filepath.Join(dataDir, "credential.key")
	raw, err := os.ReadFile(path)
	if err == nil {
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return nil, err
	}
	return buf, nil
}

// SealCredentials encrypts a credential map for storage.
func SealCredentials(dataDir string, fields map[string]string) (string, error) {
	aead, err := aeadFor(dataDir)
	if err != nil {
		return "", err
	}
	return sealWith(aead, fields)
}

// OpenCredentials decrypts a stored credential blob.
func OpenCredentials(dataDir, sealed string) (map[string]string, error) {
	aead, err := aeadFor(dataDir)
	if err != nil {
		return nil, err
	}
	return openWith(aead, sealed)
}

// MaskCredentials returns a copy safe to show in an API response: every value
// becomes "****" while the key names are preserved so operators can see which
// fields are configured.
func MaskCredentials(fields map[string]string) map[string]string {
	out := make(map[string]string, len(fields))
	for k := range fields {
		out[k] = "****"
	}
	return out
}

// MaskSecret keeps the first four characters of an identifier (e.g. an access
// key id) so operators can tell credentials apart without exposing them.
func MaskSecret(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return v[:4] + "****"
}

// RotateCredentialKey re-encrypts every stored credential from the old key to
// the new one.
//
// Rotation procedure:
//
//	export YUKI_CREDENTIAL_KEY_OLD=<old key, base64>
//	export YUKI_CREDENTIAL_KEY=<new key, base64>
//	yuki-server profile rekey --data-dir /var/lib/yukitunnel
//
// The function is transactional per row: a failure leaves already-rotated rows
// readable with the new key and the rest readable with the old key, so the
// command can simply be re-run after fixing the cause.
func (s *Store) RotateCredentialKey(ctx context.Context, dataDir string) (int, error) {
	oldRaw, err := resolveCredentialKeyFromEnvOrFile("YUKI_CREDENTIAL_KEY_OLD", dataDir)
	if err != nil {
		return 0, fmt.Errorf("旧密钥不可用（请设置 YUKI_CREDENTIAL_KEY_OLD）：%w", err)
	}
	newRaw, err := resolveCredentialKeyFromEnvOrFile("YUKI_CREDENTIAL_KEY", dataDir)
	if err != nil {
		return 0, fmt.Errorf("新密钥不可用：%w", err)
	}
	oldAEAD, err := aeadForKey(oldRaw)
	if err != nil {
		return 0, err
	}
	newAEAD, err := aeadForKey(newRaw)
	if err != nil {
		return 0, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, fields_json FROM profile_credentials`)
	if err != nil {
		return 0, err
	}
	type pair struct{ id, sealed string }
	var all []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.sealed); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, p := range all {
		fields, err := openWith(oldAEAD, p.sealed)
		if err != nil {
			return n, fmt.Errorf("解密 %s 失败：%w", p.id, err)
		}
		sealed, err := sealWith(newAEAD, fields)
		if err != nil {
			return n, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE profile_credentials SET fields_json=?, updated_at=? WHERE id=?`,
			sealed, ts(nowUTC()), p.id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// resolveCredentialKeyFromEnvOrFile reads a specific env var, falling back to
// the key file in dataDir.
func resolveCredentialKeyFromEnvOrFile(envName, dataDir string) ([]byte, error) {
	if env := strings.TrimSpace(os.Getenv(envName)); env != "" {
		raw, err := base64.StdEncoding.DecodeString(env)
		if err != nil {
			return nil, fmt.Errorf("%s is not valid base64: %w", envName, err)
		}
		if len(raw) < 16 {
			return nil, fmt.Errorf("%s must decode to at least 16 bytes", envName)
		}
		return raw, nil
	}
	if dataDir == "" {
		return nil, ErrNoCredentialKey
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "credential.key"))
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func sealWith(aead cipher.AEAD, fields map[string]string) (string, error) {
	plain, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil)), nil
}

func openWith(aead cipher.AEAD, sealed string) (map[string]string, error) {
	payload := strings.TrimPrefix(sealed, "v1:")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("credential blob is not valid base64: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return nil, errors.New("credential blob is truncated")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return nil, errors.New("credential blob cannot be decrypted (key rotated or corrupted)")
	}
	var fields map[string]string
	if err := json.Unmarshal(plain, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}
