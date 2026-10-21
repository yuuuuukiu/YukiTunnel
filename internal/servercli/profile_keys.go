package servercli

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
)

const devProfileKeyID = "yuki-profiles-2026"

// trustedKeys mirrors the server's pinned key logic so CLI and API agree.
func trustedKeys() map[string]ed25519.PublicKey {
	keys := map[string]ed25519.PublicKey{}
	if raw := os.Getenv("YUKI_PROFILE_PUBKEY"); raw != "" {
		if pub, err := base64.StdEncoding.DecodeString(raw); err == nil && len(pub) == ed25519.PublicKeySize {
			keys["env-override"] = ed25519.PublicKey(pub)
		}
	}
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, []byte("yukitunnel-development-profile-key-do-not-use-in-prod"))
	keys[devProfileKeyID] = ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	return keys
}
