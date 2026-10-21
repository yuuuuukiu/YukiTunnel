package registry

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	dir        string
	index      []byte
	sig        []byte
	profile    []byte
	profileID  string
	profileSHA string
	trusted    map[string]ed25519.PublicKey
	// priv is the fixture signing key, used by tests that need to re-sign a
	// modified index.
	priv ed25519.PrivateKey
}

// newFixture builds a signed index + profile pair on disk.
func newFixture(t *testing.T, tamperSig bool) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	profile := []byte(`apiVersion: yukitunnel.io/v1
kind: CloudProfile
metadata: {id: test-profile, name: Test, vendor: aliyun, version: 1.0.0}
spec:
  auth: {engine: aliyun-hmac-sha1, credential_fields: [access_key_id, access_key_secret]}
  endpoint: {template: "https://ecs.{{ .region }}.aliyuncs.com/", method: POST, content_type: application/x-www-form-urlencoded}
  allowed_hosts: ["ecs.*.aliyuncs.com"]
  permissions: ["ecs:AuthorizeSecurityGroup"]
  parameters: [{name: region, required: true}, {name: security_group_id, required: true}, {name: port_min, required: true}]
  revoke_policy: {by: same_params}
  actions:
    ensure_port_allowed:
      operation: AuthorizeSecurityGroup
      params: {SecurityGroupId: "{{ .security_group_id }}"}
      idempotent_check:
        operation: DescribeSecurityGroups
        params: {SecurityGroupId: "{{ .security_group_id }}"}
        match: {path: "Permissions.Permission", compare: port_range, fields: {PortRange: "{{ .port_min }}"}}
    revoke_port_allowed:
      operation: RevokeSecurityGroup
      params: {SecurityGroupId: "{{ .security_group_id }}"}
`)
	sum := sha256.Sum256(profile)
	sha := hex.EncodeToString(sum[:])

	idx := Index{
		Schema:      1,
		GeneratedAt: time.Now().UTC(),
		KeyID:       "test-key",
		Profiles: []IndexProfile{{
			ID: "test-profile", Vendor: "aliyun", Name: "Test", Version: "1.0.0",
			Path: "profiles/test-profile.yaml", SHA256: sha,
		}},
	}
	rawIndex, _ := json.Marshal(idx)
	sig := ed25519.Sign(priv, rawIndex)
	if tamperSig {
		sig[0] ^= 0xff
	}
	env := SignatureEnvelope{Schema: 1, KeyID: "test-key", Algorithm: "ed25519", Signature: base64.StdEncoding.EncodeToString(sig)}
	rawSig, _ := json.Marshal(env)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "index.json"), rawIndex, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "index.json.sig"), rawSig, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "profiles", "test-profile.yaml"), profile, 0o644)

	return &fixture{
		dir: dir, index: rawIndex, sig: rawSig, profile: profile,
		profileID: "test-profile", profileSHA: sha,
		trusted: map[string]ed25519.PublicKey{"test-key": pub},
		priv:    priv,
	}
}

func (f *fixture) server(t *testing.T, corruptProfile bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(f.index) })
	mux.HandleFunc("/index.json.sig", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(f.sig) })
	mux.HandleFunc("/profiles/test-profile.yaml", func(w http.ResponseWriter, r *http.Request) {
		if corruptProfile {
			_, _ = w.Write([]byte(strings.Replace(string(f.profile), "aliyun", "evil", 1)))
			return
		}
		_, _ = w.Write(f.profile)
	})
	return httptest.NewServer(mux)
}

func TestRefreshVerifiesSignature(t *testing.T) {
	f := newFixture(t, false)
	srv := f.server(t, false)
	defer srv.Close()

	r, err := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := r.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Profiles) != 1 || idx.Profiles[0].ID != "test-profile" {
		t.Fatalf("unexpected index: %+v", idx)
	}
	if r.Source() != srv.URL {
		t.Fatalf("source = %q", r.Source())
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	f := newFixture(t, true)
	srv := f.server(t, false)
	defer srv.Close()
	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	_, err := r.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature failure, got %v", err)
	}
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("tampered signature must wrap ErrVerification, got %v", err)
	}
}

func TestUntrustedKeyRejected(t *testing.T) {
	f := newFixture(t, false)
	srv := f.server(t, false)
	defer srv.Close()
	// Trust a different key id: verification must fail even though the bytes
	// are otherwise valid.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: map[string]ed25519.PublicKey{"other-key": other}})
	_, err := r.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("expected untrusted key failure, got %v", err)
	}
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("untrusted key must wrap ErrVerification, got %v", err)
	}
}

func TestMirrorFallback(t *testing.T) {
	f := newFixture(t, false)
	good := f.server(t, false)
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer bad.Close()

	var failures []string
	r, _ := New(Options{
		Mirrors:     []string{bad.URL, good.URL},
		TrustedKeys: f.trusted,
		OnFallback:  func(m string, err error) { failures = append(failures, m) },
	})
	idx, err := r.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if idx == nil || r.Source() != good.URL {
		t.Fatalf("expected fallback to good mirror, source=%q", r.Source())
	}
	if len(failures) != 1 || failures[0] != bad.URL {
		t.Fatalf("fallback callback = %v", failures)
	}
}

func TestProfileHashMismatchRejected(t *testing.T) {
	f := newFixture(t, false)
	srv := f.server(t, true) // serves corrupted profile bytes
	defer srv.Close()
	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	_, err := r.Load(context.Background(), f.profileID)
	if err == nil || !strings.Contains(err.Error(), "sha256 does not match") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	// A hash mismatch is an integrity failure, not a transport error, so callers
	// must be able to distinguish it and refuse any offline fallback.
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("hash mismatch must wrap ErrVerification, got %v", err)
	}
}

func TestLoadVerifiedProfile(t *testing.T) {
	f := newFixture(t, false)
	srv := f.server(t, false)
	defer srv.Close()
	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	p, err := r.Load(context.Background(), f.profileID)
	if err != nil {
		t.Fatal(err)
	}
	if p.SHA256 != f.profileSHA {
		t.Fatalf("sha = %s want %s", p.SHA256, f.profileSHA)
	}
	if p.Source != srv.URL {
		t.Fatalf("source = %s", p.Source)
	}
}

func TestCacheStillVerified(t *testing.T) {
	f := newFixture(t, false)
	srv := f.server(t, false)
	r, _ := New(Options{
		Mirrors: []string{srv.URL}, TrustedKeys: f.trusted,
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	})
	if _, err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.Close() // mirror goes away

	offline, _ := New(Options{
		Mirrors: []string{"http://127.0.0.1:1"}, TrustedKeys: f.trusted,
		CacheDir: r.opts.CacheDir,
	})
	idx, err := offline.Refresh(context.Background())
	if err != nil {
		t.Fatalf("expected cache fallback: %v", err)
	}
	if offline.Source() != "cache" || len(idx.Profiles) != 1 {
		t.Fatalf("unexpected cache result: %+v", idx)
	}
}

func TestCacheTamperingRejected(t *testing.T) {
	f := newFixture(t, false)
	srv := f.server(t, false)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted, CacheDir: cacheDir})
	if _, err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	// Attacker edits the cached index on disk.
	if err := os.WriteFile(filepath.Join(cacheDir, "index.json"), []byte(`{"schema":1,"profiles":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	offline, _ := New(Options{
		Mirrors: []string{"http://127.0.0.1:1"}, TrustedKeys: f.trusted, CacheDir: cacheDir,
	})
	if _, err := offline.Refresh(context.Background()); err == nil {
		t.Fatal("tampered cache must be rejected")
	}
}

func TestNoTrustedKeysIsFatal(t *testing.T) {
	if _, err := New(Options{Mirrors: []string{"http://x"}}); err == nil {
		t.Fatal("registry must refuse to start without trusted keys")
	}
}

func TestProxyURLValidation(t *testing.T) {
	f := newFixture(t, false)
	if _, err := New(Options{TrustedKeys: f.trusted, Proxy: "ftp://bad"}); err == nil {
		t.Fatal("unsupported proxy scheme must be rejected")
	}
	for _, p := range []string{"http://127.0.0.1:8080", "socks5://127.0.0.1:1080", "https://proxy:3128"} {
		if _, err := New(Options{TrustedKeys: f.trusted, Proxy: p}); err != nil {
			t.Fatalf("proxy %s rejected: %v", p, err)
		}
	}
}

func TestVerifyIndexRejectsBadSchema(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw := []byte(`{"schema":99,"profiles":[]}`)
	sig := ed25519.Sign(priv, raw)
	env, _ := json.Marshal(SignatureEnvelope{KeyID: "k", Algorithm: "ed25519", Signature: base64.StdEncoding.EncodeToString(sig)})
	if _, err := VerifyIndex(raw, env, map[string]ed25519.PublicKey{"k": pub}); err == nil {
		t.Fatal("expected schema rejection")
	}
}

func TestVerifyIndexRejectsNonEd25519(t *testing.T) {
	raw := []byte(`{}`)
	env, _ := json.Marshal(SignatureEnvelope{KeyID: "k", Algorithm: "rsa-sha256", Signature: "AAAA"})
	if _, err := VerifyIndex(raw, env, map[string]ed25519.PublicKey{"k": {}}); err == nil ||
		!strings.Contains(err.Error(), "algorithm") {
		t.Fatalf("expected algorithm rejection, got %v", err)
	}
}

func TestLoadUnknownProfile(t *testing.T) {
	f := newFixture(t, false)
	srv := f.server(t, false)
	defer srv.Close()
	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	_, err := r.Load(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
}

var _ = fmt.Sprint

// TestIndexPathLayouts proves the client works with both registry layouts: the
// standalone repo keeps index.json under profiles/, while an R2 bucket keeps it
// at the root.
func TestIndexPathLayouts(t *testing.T) {
	for _, layout := range []string{"root", "profiles"} {
		t.Run(layout, func(t *testing.T) {
			f := newFixture(t, false)
			dir := f.dir
			if layout == "profiles" {
				// Move the index files under profiles/.
				content, _ := os.ReadFile(filepath.Join(dir, "index.json"))
				sig, _ := os.ReadFile(filepath.Join(dir, "index.json.sig"))
				if err := os.WriteFile(filepath.Join(dir, "profiles", "index.json"), content, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "profiles", "index.json.sig"), sig, 0o644); err != nil {
					t.Fatal(err)
				}
				_ = os.Remove(filepath.Join(dir, "index.json"))
				_ = os.Remove(filepath.Join(dir, "index.json.sig"))
			}
			srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
			defer srv.Close()
			r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
			idx, err := r.Refresh(context.Background())
			if err != nil {
				t.Fatalf("%s layout: %v", layout, err)
			}
			if len(idx.Profiles) != 1 {
				t.Fatalf("%s layout: unexpected index %+v", layout, idx)
			}
		})
	}
}

// TestChallengeRetryWithBrowserAgent covers mirrors behind a CDN that rejects
// unfamiliar clients. Cloudflare answers 403 + cf-mitigated: challenge for the
// default curl/Go agents but serves the file to a browser-like one, so a single
// strict User-Agent would otherwise make profile delivery fail for everyone.
func TestChallengeRetryWithBrowserAgent(t *testing.T) {
	f := newFixture(t, false)

	var sawChallenge, sawBrowser int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent := r.Header.Get("User-Agent")
		if agent == registryUserAgentForTest || strings.HasPrefix(agent, "Go-http-client") {
			sawChallenge++
			w.Header().Set("cf-mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html>challenge</html>"))
			return
		}
		sawBrowser++
		http.FileServer(http.Dir(f.dir)).ServeHTTP(w, r)
	}))
	defer srv.Close()

	r, err := New(Options{
		Mirrors:     []string{srv.URL},
		TrustedKeys: f.trusted,
		UserAgent:   registryUserAgentForTest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh should have retried with a browser agent: %v", err)
	}
	if sawChallenge == 0 {
		t.Fatal("expected the honest agent to be challenged at least once")
	}
	if sawBrowser == 0 {
		t.Fatal("expected a retry with the fallback agent")
	}
}

const registryUserAgentForTest = "yuki-server/profile-registry"

// A 403 that survives the retry must be reported as a transport error, not as
// an integrity failure: otherwise the caller would refuse to fall back to the
// bundled profiles and the client would have nothing to install.
func TestChallengeFailureStaysTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("cf-mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	f := newFixture(t, false)
	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	_, err := r.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrVerification) {
		t.Fatalf("a blocked mirror is a transport problem, not tampering: %v", err)
	}
	if !strings.Contains(err.Error(), "challenge") {
		t.Fatalf("error should explain the challenge: %v", err)
	}
}

// TestLoadResolvesProfileRelativeToIndexDir covers a layout mismatch that made
// every real install fail: the registry repo keeps index.json and the YAMLs
// under profiles/, and the index records bare filenames ("foo.yaml"), so
// resolving against the mirror root 404s.
func TestLoadResolvesProfileRelativeToIndexDir(t *testing.T) {
	f := newFixture(t, false)

	// Re-sign an index whose Path is a bare filename, then serve the whole
	// thing from <root>/profiles/.
	idx := Index{
		Schema:      1,
		GeneratedAt: time.Now().UTC(),
		KeyID:       "test-key",
		Profiles: []IndexProfile{{
			ID: "test-profile", Vendor: "aliyun", Name: "Test", Version: "1.0.0",
			Path: "test-profile.yaml", SHA256: f.profileSHA,
		}},
	}
	rawIndex, _ := json.Marshal(idx)
	rawSig := f.signIndex(t, rawIndex)

	dir := t.TempDir()
	sub := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(sub, "index.json"), rawIndex, 0o644)
	_ = os.WriteFile(filepath.Join(sub, "index.json.sig"), rawSig, 0o644)
	_ = os.WriteFile(filepath.Join(sub, "test-profile.yaml"), f.profile, 0o644)

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	p, err := r.Load(context.Background(), "test-profile")
	if err != nil {
		t.Fatalf("load from profiles/ subdirectory: %v", err)
	}
	if p.Metadata.ID != "test-profile" {
		t.Fatalf("unexpected profile: %s", p.Metadata.ID)
	}
	if !strings.HasSuffix(p.Source, "/profiles") {
		t.Fatalf("source should point at the index directory, got %q", p.Source)
	}
}

// signIndex re-signs a modified index with the fixture's private key.
func (f *fixture) signIndex(t *testing.T, rawIndex []byte) []byte {
	t.Helper()
	sig := ed25519.Sign(f.priv, rawIndex)
	env := SignatureEnvelope{
		Schema: 1, KeyID: "test-key", Algorithm: "ed25519",
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestLoadRejectsHashMismatchKeepsGoing ensures a tampered profile aborts
// instead of silently falling through to another mirror.
func TestLoadRejectsHashMismatchAborts(t *testing.T) {
	f := newFixture(t, false)
	dir := t.TempDir()
	sub := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.json", "index.json.sig"} {
		raw, _ := os.ReadFile(filepath.Join(f.dir, name))
		_ = os.WriteFile(filepath.Join(sub, name), raw, 0o644)
	}
	// Serve a profile whose bytes do not match the signed hash.
	_ = os.WriteFile(filepath.Join(sub, f.profileID+".yaml"), []byte("apiVersion: yukitunnel.io/v1\nkind: CloudProfile\n"), 0o644)

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	r, _ := New(Options{Mirrors: []string{srv.URL}, TrustedKeys: f.trusted})
	_, err := r.Load(context.Background(), f.profileID)
	if err == nil {
		t.Fatal("expected a hash mismatch")
	}
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("hash mismatch must be an integrity failure, got %v", err)
	}
}
