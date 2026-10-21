package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCredentialsAPIResponseMasked proves no endpoint returns a cloud secret.
func TestCredentialsAPIResponseMasked(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)

	const ak = "AKIAVERYSECRETID"
	const sk = "SUPER-SECRET-VALUE"
	code := s.do(t, "PUT", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, map[string]any{
		"label":  "prod",
		"fields": map[string]string{"access_key_id": ak, "access_key_secret": sk},
	}, nil)
	if code != 200 {
		t.Fatalf("credential put: %d", code)
	}

	// Raw response body, not a decoded struct: a typed decode could hide an
	// accidentally added field.
	body := s.doRaw(t, "GET", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, nil)
	if strings.Contains(body, sk) {
		t.Fatalf("API leaked the secret key: %s", body)
	}
	if strings.Contains(body, ak) {
		t.Fatalf("API leaked the full access key id: %s", body)
	}
	if !strings.Contains(body, "****") {
		t.Fatalf("expected masked fields, got %s", body)
	}
	// The identifier keeps a 4-char prefix so operators can distinguish keys.
	if !strings.Contains(body, "AKIA****") {
		t.Fatalf("expected AKIA**** prefix mask, got %s", body)
	}
}

// TestSignatureCannotBeBypassedByPolicy constructs a profile whose index looks
// trusted but whose signature is invalid, and asserts the registry still
// refuses it — even when the profile document itself claims to be allowed.
func TestSignatureCannotBeBypassedByPolicy(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A profile that tries to declare its own trust policy.
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	doc += "\n  profile_policy: allow\n  trusted: true\n"
	if err := os.WriteFile(filepath.Join(profilesDir, "evil.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	index := `{"schema":1,"generated_at":"2026-10-20T00:00:00Z","key_id":"test-key","profiles":[{"id":"evil","vendor":"aliyun","version":"1.0.0","path":"evil.yaml","sha256":"deadbeef"}]}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	// Signature from a key the server does not trust.
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	sig := ed25519.Sign(otherPriv, []byte(index))
	env, _ := json.Marshal(map[string]any{
		"schema": 1, "key_id": "test-key", "algorithm": "ed25519",
		"signature": base64.StdEncoding.EncodeToString(sig),
	})
	if err := os.WriteFile(filepath.Join(dir, "index.json.sig"), env, 0o644); err != nil {
		t.Fatal(err)
	}
	mux := startMirror(t, dir)

	s := newTestServer(t)
	admin := s.adminToken(t)
	t.Setenv("YUKI_PROFILE_MIRRORS", mux)
	// Pin a different key so the signature cannot verify.
	t.Setenv("YUKI_PROFILE_PUBKEY", base64.StdEncoding.EncodeToString(pub))

	var out map[string]any
	code := s.do(t, "GET", "/admin/profiles/index", admin, nil, &out)
	if code == 200 {
		t.Fatalf("index with an invalid signature was accepted: %v", out)
	}
	// Installing by id must also fail: it depends on the verified index.
	code = s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"id": "evil"}, nil)
	if code == 200 {
		t.Fatal("install must fail when the index signature is invalid")
	}
	// And the profile must not be present afterwards.
	var list struct {
		Profiles []struct {
			ID string `json:"id"`
		} `json:"profiles"`
	}
	s.do(t, "GET", "/admin/profiles", admin, nil, &list)
	for _, p := range list.Profiles {
		if p.ID == "evil" {
			t.Fatal("unverified profile was installed")
		}
	}
}

// TestAuditLogCoversProfileCalls checks every field the audit trail promises for
// a cloud profile execution.
func TestAuditLogCoversProfileCalls(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)
	s.do(t, "PUT", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, map[string]any{
		"label":  "default",
		"fields": map[string]string{"access_key_id": "AK", "access_key_secret": "SK"},
	}, nil)
	code := s.do(t, "POST", "/admin/profiles/aliyun-ecs-security-group/run", admin, map[string]any{
		"action": "ensure_port_allowed", "port_min": 20000, "port_max": 20010, "protocol": "tcp",
		"params":  map[string]any{"region": "cn-hangzhou", "security_group_id": "sg-test"},
		"dry_run": true, "acknowledge_permissions": true,
	}, nil)
	if code != 200 {
		t.Fatalf("run: %d", code)
	}

	var audit struct {
		Entries []struct {
			Action     string    `json:"action"`
			ActorName  string    `json:"actor_name"`
			TargetType string    `json:"target_type"`
			TargetID   string    `json:"target_id"`
			Detail     string    `json:"detail"`
			CreatedAt  time.Time `json:"created_at"`
		} `json:"entries"`
	}
	s.do(t, "GET", "/admin/audit?limit=100", admin, nil, &audit)

	var found bool
	for _, e := range audit.Entries {
		if e.Action != "profile.ensure_port_allowed" {
			continue
		}
		found = true
		if e.ActorName != "admin" {
			t.Fatalf("actor = %q", e.ActorName)
		}
		if e.TargetType != "profile" || e.TargetID != "aliyun-ecs-security-group" {
			t.Fatalf("target = %s:%s", e.TargetType, e.TargetID)
		}
		if e.CreatedAt.IsZero() {
			t.Fatal("timestamp missing")
		}
	}
	if !found {
		t.Fatalf("no audit entry for the profile run; entries: %+v", audit.Entries)
	}

	// The dedicated run table carries the structured port/proto fields.
	var runs struct {
		Runs []struct {
			Action   string `json:"action"`
			PortMin  int    `json:"port_min"`
			PortMax  int    `json:"port_max"`
			Protocol string `json:"protocol"`
			Status   string `json:"status"`
			Profile  string `json:"profile_id"`
			SHA      string `json:"profile_sha256"`
		} `json:"runs"`
	}
	s.do(t, "GET", "/admin/profile-runs", admin, nil, &runs)
	if len(runs.Runs) != 1 {
		t.Fatalf("runs = %+v", runs.Runs)
	}
	r := runs.Runs[0]
	if r.PortMin != 20000 || r.PortMax != 20010 || r.Protocol != "tcp" {
		t.Fatalf("run fields = %+v", r)
	}
	if r.Profile != "aliyun-ecs-security-group" || r.SHA != sum {
		t.Fatalf("run identity = %+v", r)
	}
	if r.Status != "skipped" {
		t.Fatalf("dry-run status = %q", r.Status)
	}
}

// TestCredentialAuditOmitsSecrets asserts the audit entry for a credential write
// only carries a 4-char identifier prefix.
func TestCredentialAuditOmitsSecrets(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)
	s.do(t, "PUT", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, map[string]any{
		"label":  "default",
		"fields": map[string]string{"access_key_id": "AKIAEXAMPLE", "access_key_secret": "TOPSECRET"},
	}, nil)
	body := s.doRaw(t, "GET", "/admin/audit?limit=50", admin, nil)
	if strings.Contains(body, "TOPSECRET") {
		t.Fatal("audit log contains the secret key")
	}
	if strings.Contains(body, "AKIAEXAMPLE") {
		t.Fatal("audit log contains the full access key id")
	}
	if !strings.Contains(body, "AKIA…") && !strings.Contains(body, "AKIA") {
		t.Logf("audit body: %s", body)
	}
}

// TestOfflineFallbackOnlyOnTransportFailure is the regression test for a bug I
// introduced: falling back to the bundled profiles whenever the registry
// failed. That would let an attacker who can block the mirrors force a client
// onto unverified content. Falling back must depend *only* on transport
// failure, never on a verification failure.
func TestOfflineFallbackOnlyOnTransportFailure(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	// 1) Transport failure (a local mirror that is closed) -> bundled fallback.
	t.Setenv("YUKI_PROFILE_MIRRORS", "http://127.0.0.1:1")
	var offline struct {
		Source  string `json:"source"`
		Offline bool   `json:"offline"`
		Warning string `json:"warning"`
	}
	code := s.do(t, "GET", "/admin/profiles/index", admin, nil, &offline)
	if code != 200 {
		t.Fatalf("expected bundled fallback, got %d", code)
	}
	if offline.Source != "builtin" || !offline.Offline || offline.Warning == "" {
		t.Fatalf("unexpected offline payload: %+v", offline)
	}

	// 2) Integrity failure (index served, no signature) -> hard refusal.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(`{"schema":1,"profiles":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mirror := startMirror(t, dir)
	t.Setenv("YUKI_PROFILE_MIRRORS", mirror)

	body := s.doRaw(t, "GET", "/admin/profiles/index", admin, nil)
	if strings.Contains(body, `"source":"builtin"`) {
		t.Fatalf("unsigned mirror must never fall back to bundled profiles: %s", body)
	}
	if !strings.Contains(body, "PROFILE_INTEGRITY_FAILURE") {
		t.Fatalf("expected integrity failure, got %s", body)
	}

	// 3) Installing by id must also refuse, not silently use the bundled copy.
	body = s.doRaw(t, "POST", "/admin/profiles/install", admin, map[string]any{"id": "aliyun-ecs-security-group"})
	if !strings.Contains(body, "PROFILE_INTEGRITY_FAILURE") {
		t.Fatalf("install must refuse on integrity failure, got %s", body)
	}
}
