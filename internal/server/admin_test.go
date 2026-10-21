package server

import (
	"strings"
	"testing"
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
