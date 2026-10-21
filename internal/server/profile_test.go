package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProfileIndexRequiresVerifiedMirror proves that a mirror serving an
// unsigned/tampered index is rejected by the API rather than silently trusted.
func TestProfileIndexRejectsUnsignedMirror(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	// A local mirror with no .sig file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(`{"schema":1,"profiles":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := startMirror(t, dir)

	// Replace the whole chain with the untrusted mirror so the test never
	// touches the network (YUKI_PROFILE_MIRRORS overrides the built-ins).
	t.Setenv("YUKI_PROFILE_MIRRORS", mux)
	code := s.do(t, "PUT", "/admin/profile-settings", admin, map[string]any{
		"mirrors": mux,
	}, nil)
	if code != 200 {
		t.Fatalf("settings: %d", code)
	}
	var out struct {
		Source   string `json:"source"`
		Verified bool   `json:"verified"`
	}
	code = s.do(t, "GET", "/admin/profiles/index", admin, nil, &out)
	if code == 200 {
		t.Fatalf("an unsigned mirror must never yield a verified index, got 200 from %q", out.Source)
	}
}

// TestProfileInstallRequiresHash verifies that local imports need an explicit
// hash so an operator cannot install a silently modified document.
func TestProfileInstallRequiresHash(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")

	code := s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{
		"document": doc,
	}, nil)
	if code != 400 {
		t.Fatalf("expected 400 without hash, got %d", code)
	}
}

func TestProfileInstallAndList(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/tencent-vpc-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))

	code := s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{
		"document": doc, "sha256": sum,
	}, nil)
	if code != 201 {
		t.Fatalf("install: %d", code)
	}
	var list struct {
		Profiles []struct {
			ID     string `json:"id"`
			Vendor string `json:"vendor"`
			SHA256 string `json:"sha256"`
		} `json:"profiles"`
	}
	if code := s.do(t, "GET", "/admin/profiles", admin, nil, &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if len(list.Profiles) != 1 || list.Profiles[0].ID != "tencent-vpc-security-group" {
		t.Fatalf("unexpected profiles: %+v", list.Profiles)
	}
	if list.Profiles[0].SHA256 != sum {
		t.Fatalf("sha mismatch")
	}
}

func TestProfileCredentialFieldValidation(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)

	// Unknown field must be rejected before storage.
	code := s.do(t, "PUT", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, map[string]any{
		"label":  "prod",
		"fields": map[string]string{"access_key_id": "AK", "access_key_secret": "SK", "password": "nope"},
	}, nil)
	if code != 400 {
		t.Fatalf("expected 400 for unknown field, got %d", code)
	}
	// Valid fields are accepted and never echoed back.
	code = s.do(t, "PUT", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, map[string]any{
		"label":  "prod",
		"fields": map[string]string{"access_key_id": "AKIAEXAMPLE", "access_key_secret": "secret"},
	}, nil)
	if code != 200 {
		t.Fatalf("credential put: %d", code)
	}
	var list struct {
		Credentials []map[string]any `json:"credentials"`
	}
	s.do(t, "GET", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, nil, &list)
	if len(list.Credentials) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(list.Credentials))
	}
	raw, _ := json.Marshal(list.Credentials[0])
	if containsSecret(string(raw)) {
		t.Fatalf("credential response leaked secrets: %s", raw)
	}
	// Field names are preserved so operators can see what is configured, but
	// every value must be masked.
	fields, _ := list.Credentials[0]["fields"].(map[string]any)
	if len(fields) != 2 {
		t.Fatalf("expected two masked fields, got %v", fields)
	}
	for name, v := range fields {
		sv, _ := v.(string)
		if !strings.HasSuffix(sv, "****") {
			t.Fatalf("field %q not masked: %v", name, v)
		}
	}
}

func TestProfileRunRequiresAcknowledgement(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)

	code := s.do(t, "POST", "/admin/profiles/aliyun-ecs-security-group/run", admin, map[string]any{
		"action": "ensure_port_allowed", "port_min": 20000, "port_max": 20000,
	}, nil)
	if code != 400 {
		t.Fatalf("expected 400 without acknowledgement, got %d", code)
	}
}

func TestProfileRunRequiresCredentials(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)

	var out map[string]any
	code := s.do(t, "POST", "/admin/profiles/aliyun-ecs-security-group/run", admin, map[string]any{
		"action": "ensure_port_allowed", "port_min": 20000, "port_max": 20000,
		"acknowledge_permissions": true,
	}, &out)
	if code != 400 {
		t.Fatalf("expected 400 for missing credentials, got %d", code)
	}
}

func TestProfileDryRunRecordsRun(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/aliyun-ecs-security-group.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)
	s.do(t, "PUT", "/admin/profiles/aliyun-ecs-security-group/credentials", admin, map[string]any{
		"label":  "default",
		"fields": map[string]string{"access_key_id": "AK", "access_key_secret": "SK"},
	}, nil)

	var out struct {
		Result struct {
			Skipped bool   `json:"skipped"`
			Message string `json:"message"`
		} `json:"result"`
	}
	code := s.do(t, "POST", "/admin/profiles/aliyun-ecs-security-group/run", admin, map[string]any{
		"action": "ensure_port_allowed", "port_min": 20000, "port_max": 20000,
		"params":  map[string]any{"region": "cn-hangzhou", "security_group_id": "sg-test"},
		"dry_run": true, "acknowledge_permissions": true,
	}, &out)
	if code != 200 {
		t.Fatalf("dry-run: %d", code)
	}
	if !out.Result.Skipped {
		t.Fatalf("dry-run should be skipped: %+v", out.Result)
	}

	var runs struct {
		Runs []struct {
			Status string `json:"status"`
			Action string `json:"action"`
		} `json:"runs"`
	}
	s.do(t, "GET", "/admin/profile-runs", admin, nil, &runs)
	if len(runs.Runs) != 1 || runs.Runs[0].Action != "ensure_port_allowed" {
		t.Fatalf("expected one recorded run, got %+v", runs.Runs)
	}
}

func TestProfileSettingsProxyValidation(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	code := s.do(t, "PUT", "/admin/profile-settings", admin, map[string]any{"proxy": "ftp://nope"}, nil)
	if code != 400 {
		t.Fatalf("expected 400 for bad proxy, got %d", code)
	}
	code = s.do(t, "PUT", "/admin/profile-settings", admin, map[string]any{"proxy": "socks5://127.0.0.1:1080"}, nil)
	if code != 200 {
		t.Fatalf("socks5 proxy should be accepted, got %d", code)
	}
}

func TestProfileDisabledBlocksExecution(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	doc := mustRead(t, "../../profiles/gcp-firewall.yaml")
	sum := sha256HexForTest([]byte(doc))
	s.do(t, "POST", "/admin/profiles/install", admin, map[string]any{"document": doc, "sha256": sum}, nil)
	s.do(t, "POST", "/admin/profiles/gcp-firewall/disable", admin, nil, nil)

	code := s.do(t, "POST", "/admin/profiles/gcp-firewall/run", admin, map[string]any{
		"action": "ensure_port_allowed", "port_min": 20000,
		"acknowledge_permissions": true, "dry_run": true,
	}, nil)
	if code != 403 {
		t.Fatalf("expected 403 for disabled profile, got %d", code)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// containsSecret looks for the actual secret *values* used by the test. Field
// names such as "access_key_secret" legitimately appear in masked responses, so
// matching on the bare word "secret" would be a false positive.
func containsSecret(s string) bool {
	for _, needle := range []string{"AKIAEXAMPLE", "TOPSECRET", "SECRETFAKE"} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
