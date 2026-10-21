// Command yuki-profile-sign is the CI signer for the profile registry.
//
// It scans a profiles/ directory, writes index.json, and signs it with an
// ed25519 key so that clients can verify the catalogue offline.
//
// Usage:
//
//	yuki-profile-sign -dir profiles -key key.pem -key-id yuki-profiles-2026
//	yuki-profile-sign -dir profiles -key key.pem -key-id dev -generate-key
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile"
)

func main() {
	dir := flag.String("dir", "profiles", "directory containing *.yaml profiles")
	keyPath := flag.String("key", "profile-signing.key", "ed25519 private key (PEM); generated when missing")
	keyID := flag.String("key-id", "yuki-profiles-2026", "key id published in the index")
	generate := flag.Bool("generate-key", false, "generate a key pair if the key file does not exist")
	pubOut := flag.String("pub-out", "", "write the base64 public key to this file (for pinning)")
	flag.Parse()

	priv, pub, err := loadOrCreateKey(*keyPath, *generate)
	if err != nil {
		fatal(err)
	}
	if *pubOut != "" {
		if err := os.WriteFile(*pubOut, []byte(base64.StdEncoding.EncodeToString(pub)), 0o644); err != nil {
			fatal(err)
		}
	}

	entries, err := scan(*dir)
	if err != nil {
		fatal(err)
	}
	idx := struct {
		Schema      int                       `json:"schema"`
		GeneratedAt time.Time                 `json:"generated_at"`
		KeyID       string                    `json:"key_id"`
		Profiles    []cloudprofile.IndexEntry `json:"profiles"`
	}{
		Schema: 1, GeneratedAt: time.Now().UTC(), KeyID: *keyID, Profiles: entries,
	}
	rawIndex, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		fatal(err)
	}
	rawIndex = append(rawIndex, '\n')
	sig := ed25519.Sign(priv, rawIndex)

	env := map[string]any{
		"schema":    1,
		"key_id":    *keyID,
		"algorithm": "ed25519",
		"signature": base64.StdEncoding.EncodeToString(sig),
	}
	rawSig, _ := json.MarshalIndent(env, "", "  ")
	rawSig = append(rawSig, '\n')

	if err := os.WriteFile(filepath.Join(*dir, "index.json"), rawIndex, 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*dir, "index.json.sig"), rawSig, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("signed %d profiles with key %q\n", len(entries), *keyID)
	fmt.Printf("index: %s/index.json\nsignature: %s/index.json.sig\n", *dir, *dir)
	fmt.Printf("public key (pin this in the binary): %s\n", base64.StdEncoding.EncodeToString(pub))
}

func scan(dir string) ([]cloudprofile.IndexEntry, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	out := make([]cloudprofile.IndexEntry, 0, len(matches))
	ids := map[string]bool{}
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var p cloudprofile.Profile
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if ids[p.Metadata.ID] {
			return nil, fmt.Errorf("%s: duplicate profile id %q", path, p.Metadata.ID)
		}
		ids[p.Metadata.ID] = true
		sum := sha256.Sum256(raw)
		rel, _ := filepath.Rel(dir, path)
		out = append(out, cloudprofile.IndexEntry{
			ID: p.Metadata.ID, Vendor: p.Metadata.Vendor, Name: p.Metadata.Name,
			Version: p.Metadata.Version, Path: filepath.ToSlash(rel),
			SHA256: hex.EncodeToString(sum[:]), Docs: p.Metadata.Docs,
			MinEngine: p.Metadata.MinEngine,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no profiles found in %s", dir)
	}
	return out, nil
}

func loadOrCreateKey(path string, generate bool) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, nil, fmt.Errorf("%s is not a PEM file", path)
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, err
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("%s is not an ed25519 key", path)
		}
		return priv, priv.Public().(ed25519.PublicKey), nil
	}
	if !generate {
		return nil, nil, fmt.Errorf("key %s not found (pass -generate-key to create one)", path)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(os.Stderr, "generated new signing key at %s (keep it secret, back it up)\n", path)
	return priv, pub, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

var _ = strings.TrimSpace
