// Package registry fetches, verifies and caches signed cloud profiles.
package registry

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile"
)

// ErrVerification marks an integrity failure (bad signature, untrusted key,
// hash mismatch). It is deliberately distinguishable from a transport error so
// callers can *never* treat "cannot verify" as "offline, use the bundled copy".
var ErrVerification = errors.New("signature verification failed")

// Index is the signed catalogue published by CI.
type Index struct {
	Schema      int            `json:"schema"`
	GeneratedAt time.Time      `json:"generated_at"`
	KeyID       string         `json:"key_id"`
	Profiles    []IndexProfile `json:"profiles"`
	// Signature is detached and lives in index.json.sig; kept here for
	// in-memory convenience after verification.
	Signature string `json:"-"`
}

// IndexProfile is one entry of the signed catalogue.
type IndexProfile = cloudprofile.IndexEntry

// SignatureEnvelope is the detached signature document.
type SignatureEnvelope struct {
	Schema    int    `json:"schema"`
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	// Signature is base64(ed25519(index.json bytes)).
	Signature string `json:"signature"`
}

// VerifyIndex checks the detached signature over rawIndex using the trusted key
// for the key id in the envelope.
//
// The signature is ALWAYS verified, for every mirror and the cache. There is no
// opt-out flag: a profile that cannot be verified is never executed.
func VerifyIndex(rawIndex, rawSig []byte, trusted map[string]ed25519.PublicKey) (*Index, error) {
	var env SignatureEnvelope
	if err := json.Unmarshal(rawSig, &env); err != nil {
		return nil, fmt.Errorf("index signature is not valid JSON: %w", err)
	}
	if env.Algorithm != "" && env.Algorithm != "ed25519" {
		return nil, fmt.Errorf("%w: unsupported algorithm %q", ErrVerification, env.Algorithm)
	}
	pub, ok := trusted[env.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w: signing key %q is not trusted", ErrVerification, env.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature is not valid base64: %v", ErrVerification, err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature has wrong length %d", ErrVerification, len(sig))
	}
	if !ed25519.Verify(pub, rawIndex, sig) {
		return nil, fmt.Errorf("%w: index signature does not match (key %s)", ErrVerification, env.KeyID)
	}
	var idx Index
	if err := json.Unmarshal(rawIndex, &idx); err != nil {
		return nil, fmt.Errorf("index is not valid JSON: %w", err)
	}
	if idx.Schema != 1 {
		return nil, fmt.Errorf("%w: unsupported index schema %d", ErrVerification, idx.Schema)
	}
	idx.Signature = env.Signature
	return &idx, nil
}
