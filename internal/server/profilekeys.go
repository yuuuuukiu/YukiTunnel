package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// ed25519PublicKey is an alias so api_profiles.go stays readable.
type ed25519PublicKey = ed25519.PublicKey

// builtinProfileKeyID is the key id published in index.json.sig.
const builtinProfileKeyID = "yuki-profiles-2026"

// placeholderProfileKey marks a build that has not been given a real signing key.
const placeholderProfileKey = "PLACEHOLDER_REPLACE_AT_RELEASE"

// builtinProfileKeyB64 is the pinned ed25519 public key used to verify
// index.json. It is compiled in on purpose: a registry that trusts whatever key
// the mirror advertises offers no protection at all.
//
// Release builds MUST replace this constant (see scripts/gen-profile-signing-key.sh).
// Development builds fall back to a deterministic key so tests and local runs
// work, but refuse to start when YUKI_RELEASE=1.
const builtinProfileKeyB64 = placeholderProfileKey

// ErrPlaceholderKeyInRelease is returned when a release build still carries the
// development placeholder key.
var ErrPlaceholderKeyInRelease = errors.New(
	"builtinProfileKeyB64 仍是开发占位值，正式构建拒绝启动：" +
		"请运行 scripts/gen-profile-signing-key.sh 并把公钥写入 internal/server/profilekeys.go")

// IsPlaceholderProfileKey reports whether the compiled-in key is the placeholder.
func IsPlaceholderProfileKey() bool { return builtinProfileKeyB64 == placeholderProfileKey }

// CheckReleaseReadiness fails when a release build would ship a placeholder key.
//
// This is deliberately a hard failure rather than a warning: a binary with the
// development key cannot verify the real registry, so every profile download
// would fail at runtime with a confusing signature error.
func CheckReleaseReadiness() error {
	if os.Getenv("YUKI_RELEASE") == "1" && IsPlaceholderProfileKey() {
		return ErrPlaceholderKeyInRelease
	}
	return nil
}

// builtinProfileKeys returns the trusted key set.
func builtinProfileKeys() map[string]ed25519.PublicKey {
	keys := map[string]ed25519.PublicKey{}
	if raw := os.Getenv("YUKI_PROFILE_PUBKEY"); raw != "" {
		if pub, err := base64.StdEncoding.DecodeString(raw); err == nil && len(pub) == ed25519.PublicKeySize {
			keys["env-override"] = ed25519.PublicKey(pub)
		}
	}
	if IsPlaceholderProfileKey() {
		keys[builtinProfileKeyID] = DevelopmentProfileKeyPublic()
		return keys
	}
	pub, err := base64.StdEncoding.DecodeString(builtinProfileKeyB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return keys
	}
	keys[builtinProfileKeyID] = ed25519.PublicKey(pub)
	return keys
}

// devProfileKeySeed is the deterministic development seed. It is public by
// design and must never be used to sign a published index.
var devProfileKeySeed = func() []byte {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, []byte("yukitunnel-development-profile-key-do-not-use-in-prod"))
	return seed
}()

// DevelopmentProfileKey returns the dev key pair (used by tests and the CI
// signer's self-check).
func DevelopmentProfileKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(devProfileKeySeed)
	return priv.Public().(ed25519.PublicKey), priv
}

// DevelopmentProfileKeyPublic returns only the dev public key.
func DevelopmentProfileKeyPublic() ed25519.PublicKey {
	pub, _ := DevelopmentProfileKey()
	return pub
}

// ParseProfilePublicKey decodes a base64 ed25519 public key.
func ParseProfilePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("public key is not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key has %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
