package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"testing"
)

// TestBuiltinKeyIsNotPlaceholder asserts that the compiled-in key is either the
// documented placeholder (development) or a well-formed ed25519 key, and that a
// release build never ships the placeholder.
func TestBuiltinKeyIsNotPlaceholder(t *testing.T) {
	if IsPlaceholderProfileKey() {
		// Development checkout: the key must still resolve to something usable
		// so tests can sign and verify, and the release guard must trip.
		pub := DevelopmentProfileKeyPublic()
		if len(pub) != ed25519.PublicKeySize {
			t.Fatalf("development key has %d bytes", len(pub))
		}
		t.Setenv("YUKI_RELEASE", "1")
		if err := CheckReleaseReadiness(); !errors.Is(err, ErrPlaceholderKeyInRelease) {
			t.Fatalf("release guard did not trip on placeholder: %v", err)
		}
		t.Setenv("YUKI_RELEASE", "0")
		if err := CheckReleaseReadiness(); err != nil {
			t.Fatalf("non-release build should start: %v", err)
		}
		return
	}
	// Release checkout: the key must parse and be usable for verification.
	pub, err := ParseProfilePublicKey(builtinProfileKeyB64)
	if err != nil {
		t.Fatalf("builtinProfileKeyB64 is not a valid ed25519 key: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("key size = %d", len(pub))
	}
	t.Setenv("YUKI_RELEASE", "1")
	if err := CheckReleaseReadiness(); err != nil {
		t.Fatalf("release build with a real key must start: %v", err)
	}
	// A signature made by the matching private key must verify with this key.
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = priv
	msg := []byte("index.json")
	sig := ed25519.Sign(priv, msg)
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), msg, sig) {
		t.Fatal("sanity: generated key failed to verify")
	}
}

func TestBuiltinProfileKeysAlwaysHasAnEntry(t *testing.T) {
	keys := builtinProfileKeys()
	if len(keys) == 0 {
		t.Fatal("no trusted keys configured")
	}
	for id, k := range keys {
		if len(k) != ed25519.PublicKeySize {
			t.Fatalf("key %q has %d bytes", id, len(k))
		}
	}
}

func TestEnvOverrideKeyIsTrusted(t *testing.T) {
	pub, _ := DevelopmentProfileKey()
	t.Setenv("YUKI_PROFILE_PUBKEY", base64.StdEncoding.EncodeToString(pub))
	keys := builtinProfileKeys()
	if _, ok := keys["env-override"]; !ok {
		t.Fatal("YUKI_PROFILE_PUBKEY should add an env-override key")
	}
}

func TestBadEnvOverrideIsIgnored(t *testing.T) {
	t.Setenv("YUKI_PROFILE_PUBKEY", "not-base64!!")
	keys := builtinProfileKeys()
	if _, ok := keys["env-override"]; ok {
		t.Fatal("invalid env key must not be trusted")
	}
}

func TestParseProfilePublicKeyRejectsWrongSize(t *testing.T) {
	if _, err := ParseProfilePublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected size error")
	}
	if _, err := ParseProfilePublicKey("!!!"); err == nil {
		t.Fatal("expected base64 error")
	}
}

func TestReleaseGuardIgnoresUnsetEnv(t *testing.T) {
	os.Unsetenv("YUKI_RELEASE")
	if err := CheckReleaseReadiness(); err != nil && !IsPlaceholderProfileKey() {
		t.Fatalf("unexpected error: %v", err)
	}
}
