package server

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestAdminUserCRUD covers the full user lifecycle the WebUI and desktop
// admin console drive: create, list, update, and delete.
func TestAdminUserCRUD(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	var created struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Role     string `json:"role"`
			Disabled bool   `json:"disabled"`
		} `json:"user"`
	}
	code := s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "carol", "password": "carol12345", "role": "user",
	}, &created)
	if code != 201 && code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if created.User.ID == "" || created.User.Role != "user" {
		t.Fatalf("unexpected create response: %+v", created.User)
	}
	uid := created.User.ID

	var list struct {
		Users []struct {
			Username string `json:"username"`
		} `json:"users"`
	}
	if code := s.do(t, "GET", "/admin/users", admin, nil, &list); code != 200 {
		t.Fatalf("list users: %d", code)
	}
	found := false
	for _, u := range list.Users {
		if u.Username == "carol" {
			found = true
		}
	}
	if !found {
		t.Fatalf("carol missing from list: %+v", list.Users)
	}

	// Disable, then confirm the flag round-trips.
	if code := s.do(t, "PATCH", "/admin/users/"+uid, admin, map[string]any{"disabled": true}, nil); code != 200 {
		t.Fatalf("disable user: %d", code)
	}
	var after struct {
		User struct {
			Disabled bool `json:"disabled"`
		} `json:"user"`
	}
	_ = s.do(t, "GET", "/admin/users", admin, nil, &after)
	body := s.doRaw(t, "GET", "/admin/users", admin, nil)
	if !strings.Contains(body, `"disabled":true`) {
		t.Fatalf("disabled flag not persisted: %s", body)
	}

	// A disabled account must not be able to log in.
	var login struct {
		Token string `json:"token"`
	}
	if code := s.do(t, "POST", "/auth/login", "", map[string]string{
		"username": "carol", "password": "carol12345",
	}, &login); code == 200 && login.Token != "" {
		t.Fatal("disabled user could still log in")
	}

	if code := s.do(t, "DELETE", "/admin/users/"+uid, admin, nil, nil); code != 200 && code != 204 {
		t.Fatalf("delete user: %d", code)
	}
}

// TestAdminNodeCRUD covers node management, which is how an operator adds a
// second ingress host.
func TestAdminNodeCRUD(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	code := s.do(t, "POST", "/admin/nodes", admin, map[string]any{
		"name": "sg-1", "region": "sg", "public_host": "203.0.113.9",
		"control_host": "203.0.113.9", "control_port": 9443, "max_tunnels": 20,
	}, nil)
	if code != 201 && code != 200 {
		t.Fatalf("create node: %d", code)
	}

	body := s.doRaw(t, "GET", "/admin/nodes", admin, nil)
	if !strings.Contains(body, "sg-1") {
		t.Fatalf("node missing from list: %s", body)
	}

	// Duplicate names must be rejected rather than silently shadowing.
	if code := s.do(t, "POST", "/admin/nodes", admin, map[string]any{
		"name": "sg-1", "public_host": "203.0.113.9",
		"control_host": "203.0.113.9", "control_port": 9443,
	}, nil); code == 200 || code == 201 {
		t.Fatal("duplicate node name was accepted")
	}

	// Invalid input should be a 4xx, not a 500.
	if code := s.do(t, "POST", "/admin/nodes", admin, map[string]any{"name": ""}, nil); code < 400 || code >= 500 {
		t.Fatalf("invalid node should be a client error, got %d", code)
	}
}

// TestAdminOverview checks the dashboard aggregate the UI renders on login.
func TestAdminOverview(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	var ov struct {
		Counts struct {
			Users         int `json:"users"`
			Nodes         int `json:"nodes"`
			NodesOnline   int `json:"nodes_online"`
			Tunnels       int `json:"tunnels"`
			TunnelsActive int `json:"tunnels_active"`
		} `json:"counts"`
		Settings struct {
			PortMin int `json:"port_min"`
			PortMax int `json:"port_max"`
		} `json:"settings"`
		Version string `json:"version"`
	}
	if code := s.do(t, "GET", "/admin/overview", admin, nil, &ov); code != 200 {
		t.Fatalf("overview: %d", code)
	}
	if ov.Counts.Users < 1 || ov.Counts.Nodes < 1 {
		t.Fatalf("overview should count the bootstrap admin and default node: %+v", ov.Counts)
	}
	if ov.Settings.PortMin == 0 || ov.Settings.PortMax == 0 {
		t.Fatalf("port range missing from overview: %+v", ov.Settings)
	}
	if ov.Version == "" {
		t.Fatal("version missing from overview")
	}
}

// TestNonAdminCannotReachAdminAPI is the security boundary: the desktop
// RequireAdmin guard is only UX, this is the real check.
func TestNonAdminCannotReachAdminAPI(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	_ = s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "dave", "password": "dave12345", "role": "user",
	}, nil)

	var login struct {
		Token string `json:"token"`
	}
	if code := s.do(t, "POST", "/auth/login", "", map[string]string{
		"username": "dave", "password": "dave12345",
	}, &login); code != 200 {
		t.Fatalf("user login: %d", code)
	}

	for _, ep := range []string{"/admin/users", "/admin/nodes", "/admin/tunnels", "/admin/overview"} {
		if code := s.do(t, "GET", ep, login.Token, nil, nil); code != 403 {
			t.Fatalf("%s should be 403 for a regular user, got %d", ep, code)
		}
	}
	// No token at all is 401.
	if code := s.do(t, "GET", "/admin/users", "", nil, nil); code != 401 {
		t.Fatalf("unauthenticated admin call should be 401, got %d", code)
	}
}

// TestDisableRegularUserNotBlockedByLastAdminGuard is the regression test for a
// bug where disabling *any* non-admin account failed with LAST_ADMIN, because
// the guard fired whenever the target was not an admin instead of only when an
// admin was being removed.
func TestDisableRegularUserNotBlockedByLastAdminGuard(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if code := s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "erin", "password": "erin12345", "role": "user",
	}, &created); code != 201 {
		t.Fatalf("create: %d", code)
	}

	body := s.doRaw(t, "PATCH", "/admin/users/"+created.User.ID, admin, map[string]any{"disabled": true})
	if strings.Contains(body, "LAST_ADMIN") {
		t.Fatalf("disabling a regular user must not hit the last-admin guard: %s", body)
	}
	if !strings.Contains(body, `"disabled":true`) {
		t.Fatalf("user was not disabled: %s", body)
	}
}

// TestLastAdminCannotBeRemoved keeps the protection the guard exists for.
func TestLastAdminCannotBeRemoved(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	// The bootstrap admin is the only admin in a fresh store.
	if code := s.do(t, "GET", "/admin/users", admin, nil, nil); code != 200 {
		t.Fatalf("list: %d", code)
	}
	body := s.doRaw(t, "GET", "/admin/users", admin, nil)
	if !strings.Contains(body, `"role":"admin"`) {
		t.Fatalf("expected a bootstrap admin: %s", body)
	}
	_ = me

	// Demoting yourself away from admin is refused earlier, with its own error;
	// deleting the last admin must also be refused.
	var list struct {
		Users []struct {
			ID       string `json:"id"`
			Role     string `json:"role"`
			Username string `json:"username"`
		} `json:"users"`
	}
	if code := s.do(t, "GET", "/admin/users", admin, nil, &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	for _, u := range list.Users {
		if u.Role == "admin" {
			if code := s.do(t, "DELETE", "/admin/users/"+u.ID, admin, nil, nil); code < 400 {
				t.Fatalf("deleting the last admin should fail, got %d", code)
			}
			break
		}
	}
}

// TestAdminTunnelLifecycle covers the tunnel controls the admin console exposes:
// list, disable, enable, delete.
func TestAdminTunnelLifecycle(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	var nodes struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if code := s.do(t, "GET", "/admin/nodes", admin, nil, &nodes); code != 200 || len(nodes.Nodes) == 0 {
		t.Fatalf("need at least one node: %d", code)
	}
	nodeID := nodes.Nodes[0].ID

	_ = s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "frank", "password": "frank12345", "role": "user",
	}, nil)
	var login struct {
		Token string `json:"token"`
	}
	if code := s.do(t, "POST", "/auth/login", "", map[string]string{
		"username": "frank", "password": "frank12345",
	}, &login); code != 200 {
		t.Fatalf("user login: %d", code)
	}

	var created struct {
		Tunnel struct {
			ID string `json:"id"`
		} `json:"tunnel"`
	}
	if code := s.do(t, "POST", "/tunnels", login.Token, map[string]any{
		"node_id": nodeID, "proto": "tcp", "local_ip": "127.0.0.1", "local_port": 8080,
	}, &created); code != 201 {
		t.Fatalf("create tunnel: %d", code)
	}
	id := created.Tunnel.ID

	var list struct {
		Tunnels []struct {
			ID string `json:"id"`
		} `json:"tunnels"`
	}
	if code := s.do(t, "GET", "/admin/tunnels", admin, nil, &list); code != 200 {
		t.Fatalf("admin list tunnels: %d", code)
	}
	found := false
	for _, tt := range list.Tunnels {
		if tt.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("tunnel %s missing from admin list", id)
	}

	for _, op := range []struct{ path, method string }{
		{"/admin/tunnels/" + id + "/disable", "POST"},
		{"/admin/tunnels/" + id + "/enable", "POST"},
	} {
		if code := s.do(t, op.method, op.path, admin, nil, nil); code != 200 {
			t.Fatalf("%s: %d", op.path, code)
		}
	}

	if code := s.do(t, "DELETE", "/admin/tunnels/"+id, admin, nil, nil); code != 200 && code != 204 {
		t.Fatalf("delete tunnel: %d", code)
	}
}

// TestTunnelCreateRequiresNode documents that omitting node_id is a client
// error with a clear code, not a 500.
func TestTunnelCreateRequiresNode(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	_ = s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "gina", "password": "gina12345", "role": "user",
	}, nil)
	var login struct {
		Token string `json:"token"`
	}
	_ = s.do(t, "POST", "/auth/login", "", map[string]string{
		"username": "gina", "password": "gina12345",
	}, &login)

	body := s.doRaw(t, "POST", "/tunnels", login.Token, map[string]any{
		"proto": "tcp", "local_ip": "127.0.0.1", "local_port": 8080,
	})
	if !strings.Contains(body, "NODE_REQUIRED") {
		t.Fatalf("expected NODE_REQUIRED, got %s", body)
	}
}

// TestAdminKeyManagement covers adding, listing and deleting a user's RSA key,
// which is the alternative to password login.
func TestAdminKeyManagement(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if code := s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "hank", "password": "hank12345", "role": "user",
	}, &created); code != 201 {
		t.Fatalf("create user: %d", code)
	}
	uid := created.User.ID

	// An empty key list must serialise as [] rather than null, so the UI can
	// call .map without a guard.
	body := s.doRaw(t, "GET", "/admin/users/"+uid+"/keys", admin, nil)
	if !strings.Contains(body, `"keys":[]`) {
		t.Fatalf("empty key list should be [], got %s", body)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// The API expects an OpenSSH authorized_keys line, not PEM.
	sshPub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	authKey := ssh.MarshalAuthorizedKey(sshPub)

	var added struct {
		Key struct {
			ID          string `json:"id"`
			Fingerprint string `json:"fingerprint"`
		} `json:"key"`
	}
	if code := s.do(t, "POST", "/admin/users/"+uid+"/keys", admin, map[string]any{
		"name": "hank-key", "public_key": string(authKey),
	}, &added); code != 201 {
		t.Fatalf("add key: %d", code)
	}
	if added.Key.ID == "" || added.Key.Fingerprint == "" {
		t.Fatalf("key response incomplete: %+v", added.Key)
	}

	body = s.doRaw(t, "GET", "/admin/users/"+uid+"/keys", admin, nil)
	if !strings.Contains(body, "hank-key") {
		t.Fatalf("key missing from list: %s", body)
	}

	// Garbage must be rejected with a client error, not a 500.
	if code := s.do(t, "POST", "/admin/users/"+uid+"/keys", admin, map[string]any{
		"name": "bad", "public_key": "not-a-pem",
	}, nil); code < 400 || code >= 500 {
		t.Fatalf("invalid key should be a client error, got %d", code)
	}

	if code := s.do(t, "DELETE", "/admin/users/"+uid+"/keys/"+added.Key.ID, admin, nil, nil); code != 200 {
		t.Fatalf("delete key: %d", code)
	}
	body = s.doRaw(t, "GET", "/admin/users/"+uid+"/keys", admin, nil)
	if strings.Contains(body, "hank-key") {
		t.Fatalf("key still present after delete: %s", body)
	}
}
