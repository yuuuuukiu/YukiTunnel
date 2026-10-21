package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/config"
)

// testServer boots a full service on ephemeral ports.
type testServer struct {
	svc     *Service
	baseURL string
	ctlURL  string
	client  *http.Client
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	// Ports are chosen by binding :0 and closing the listener, which leaves a
	// small window where another process (or a parallel test package) can take
	// the port. Retry a few times instead of failing the suite spuriously.
	for attempt := 0; attempt < 5; attempt++ {
		ts, err := tryNewTestServer(t)
		if err == nil {
			return ts
		}
		t.Logf("test server start attempt %d failed: %v", attempt+1, err)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("could not start test server after 5 attempts")
	return nil
}

func tryNewTestServer(t *testing.T) (*testServer, error) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.APIAddr = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cfg.ControlAddr = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cfg.PublicHost = "127.0.0.1"
	cfg.Ports.Min = 40000
	cfg.Ports.Max = 40100
	cfg.Domain.Suffix = "test.local"
	cfg.Domain.Binding = true
	cfg.Normalize()
	if err := cfg.Save(config.Path(dir)); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := New(cfg, logger)
	if err != nil {
		return nil, err
	}
	started := false
	t.Cleanup(func() {
		if !started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	})
	if err := svc.Start(); err != nil {
		_ = svc.Shutdown(context.Background())
		return nil, err
	}
	started = true
	// Wait for the listener.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", cfg.APIAddr)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	return &testServer{
		svc:     svc,
		baseURL: "https://" + cfg.APIAddr + "/api/v1",
		ctlURL:  "https://" + cfg.ControlAddr,
		client:  &http.Client{Transport: tr, Timeout: 10 * time.Second},
	}, nil
}

// doRaw returns the response body verbatim, for assertions about masking and
// leaked fields that a typed decode would hide.
func (s *testServer) doRaw(t *testing.T, method, path, token string, body any) string {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.baseURL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return string(raw)
}

func (s *testServer) do(t *testing.T, method, path, token string, body any, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.baseURL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

func (s *testServer) adminToken(t *testing.T) string {
	t.Helper()
	// The bootstrap admin uses the default password unless overridden.
	var res struct {
		Token string `json:"token"`
	}
	code := s.do(t, "POST", "/auth/login", "", map[string]string{"username": "admin", "password": "admin12345"}, &res)
	if code != 200 || res.Token == "" {
		t.Fatalf("admin login failed: %d", code)
	}
	return res.Token
}

func TestHealthAndMeta(t *testing.T) {
	s := newTestServer(t)
	var health map[string]any
	if code := s.do(t, "GET", "/health", "", nil, &health); code != 200 {
		t.Fatalf("health: %d", code)
	}
	if health["status"] != "ok" {
		t.Fatalf("unexpected health: %v", health)
	}
	var meta struct {
		DomainBinding bool `json:"domain_binding"`
		PortRange     struct{ Min, Max int }
	}
	s.do(t, "GET", "/meta", "", nil, &meta)
	if !meta.DomainBinding {
		t.Fatal("expected domain binding to be enabled")
	}
}

func TestLoginLockoutAndBadPassword(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 3; i++ {
		var out map[string]any
		if code := s.do(t, "POST", "/auth/login", "", map[string]string{"username": "admin", "password": "wrong"}, &out); code != 401 {
			t.Fatalf("expected 401, got %d", code)
		}
	}
	token := s.adminToken(t)
	var me struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if code := s.do(t, "GET", "/me", token, nil, &me); code != 200 || me.User.Username != "admin" {
		t.Fatalf("me failed: %d %v", code, me)
	}
}

func TestAdminCreatesUserAndTunnel(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	var created struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if code := s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "alice", "password": "alice-pass-1", "display_name": "Alice",
	}, &created); code != 201 {
		t.Fatalf("create user: %d", code)
	}
	var login struct {
		Token string
	}
	if code := s.do(t, "POST", "/auth/login", "", map[string]string{"username": "alice", "password": "alice-pass-1"}, &login); code != 200 {
		t.Fatalf("user login: %d", code)
	}
	var servers struct {
		Servers []struct {
			ID     string `json:"id"`
			Online bool   `json:"online"`
		} `json:"servers"`
	}
	s.do(t, "GET", "/servers", login.Token, nil, &servers)
	if len(servers.Servers) == 0 {
		t.Fatal("expected a bootstrap node")
	}
	var tunnel struct {
		Tunnel struct {
			ID      string `json:"id"`
			Address string `json:"address"`
			Domain  string `json:"domain"`
		} `json:"tunnel"`
	}
	code := s.do(t, "POST", "/tunnels", login.Token, map[string]any{
		"node_id": servers.Servers[0].ID, "proto": "tcp", "local_ip": "127.0.0.1", "local_port": 8080,
	}, &tunnel)
	if code != 201 {
		t.Fatalf("create tunnel: %d", code)
	}
	if tunnel.Tunnel.Domain == "" || !strings.HasSuffix(tunnel.Tunnel.Address, ".test.local:"+portOf(tunnel.Tunnel.Address)) {
		t.Fatalf("expected domain address, got %q", tunnel.Tunnel.Address)
	}
}

func portOf(addr string) string {
	i := strings.LastIndex(addr, ":")
	return addr[i+1:]
}

func TestDomainBindingToggle(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	var settings struct {
		Settings struct {
			DomainBinding bool `json:"domain_binding"`
		} `json:"settings"`
	}
	s.do(t, "GET", "/admin/settings", admin, nil, &settings)
	if !settings.Settings.DomainBinding {
		t.Fatal("expected binding on")
	}
	if code := s.do(t, "PUT", "/admin/settings", admin, map[string]any{"domain_binding": false}, &settings); code != 200 {
		t.Fatalf("update settings: %d", code)
	}
	if settings.Settings.DomainBinding {
		t.Fatal("expected binding off")
	}
	// New tunnels must now use IP:port.
	var servers struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"servers"`
	}
	s.do(t, "GET", "/servers", admin, nil, &servers)
	var tunnel struct {
		Tunnel struct {
			Domain  string `json:"domain"`
			Address string `json:"address"`
		} `json:"tunnel"`
	}
	code := s.do(t, "POST", "/tunnels", admin, map[string]any{
		"node_id": servers.Servers[0].ID, "proto": "tcp", "local_ip": "127.0.0.1", "local_port": 9090,
	}, &tunnel)
	if code != 201 {
		t.Fatalf("create tunnel: %d", code)
	}
	if tunnel.Tunnel.Domain != "" {
		t.Fatalf("expected no domain, got %q", tunnel.Tunnel.Domain)
	}
	if !strings.HasPrefix(tunnel.Tunnel.Address, "127.0.0.1:") {
		t.Fatalf("expected IP address, got %q", tunnel.Tunnel.Address)
	}
}

func TestKeyAuthFlow(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, fp, err := auth.PublicKeyFromPrivatePEM(encodePKCS1PEM(t, key))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if code := s.do(t, "POST", "/admin/users", admin, map[string]any{
		"username": "bob", "public_key": pub, "key_name": "bob-key",
	}, &created); code != 201 {
		t.Fatalf("create key user: %d", code)
	}
	if fp == "" {
		t.Fatal("expected fingerprint")
	}

	var challenge struct{ Nonce string }
	if code := s.do(t, "POST", "/auth/key/challenge", "", map[string]string{"username": "bob"}, &challenge); code != 200 {
		t.Fatalf("challenge: %d", code)
	}
	sig, err := auth.SignChallenge(key, challenge.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	var login struct{ Token string }
	code := s.do(t, "POST", "/auth/key/login", "", map[string]string{
		"username": "bob", "nonce": challenge.Nonce, "signature": sig,
	}, &login)
	if code != 200 || login.Token == "" {
		t.Fatalf("key login: %d", code)
	}

	// Replaying the same nonce must fail.
	code = s.do(t, "POST", "/auth/key/login", "", map[string]string{
		"username": "bob", "nonce": challenge.Nonce, "signature": sig,
	}, nil)
	if code != 401 {
		t.Fatalf("expected replay rejection, got %d", code)
	}
}

func TestAuditTrail(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	s.do(t, "POST", "/admin/users", admin, map[string]any{"username": "carol", "password": "carol-pass-1"}, nil)
	var audit struct {
		Entries []struct {
			Action    string `json:"action"`
			ActorName string `json:"actor_name"`
		} `json:"entries"`
	}
	if code := s.do(t, "GET", "/admin/audit?limit=50", admin, nil, &audit); code != 200 {
		t.Fatalf("audit: %d", code)
	}
	found := false
	for _, e := range audit.Entries {
		if e.Action == "user.created" && e.ActorName == "admin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected user.created audit entry, got %+v", audit.Entries)
	}
}

func TestNonAdminCannotAdminister(t *testing.T) {
	s := newTestServer(t)
	admin := s.adminToken(t)
	s.do(t, "POST", "/admin/users", admin, map[string]any{"username": "dave", "password": "dave-pass-1"}, nil)
	var login struct{ Token string }
	s.do(t, "POST", "/auth/login", "", map[string]string{"username": "dave", "password": "dave-pass-1"}, &login)
	if code := s.do(t, "GET", "/admin/users", login.Token, nil, nil); code != 403 {
		t.Fatalf("expected 403, got %d", code)
	}
}

func TestBootstrapIdempotent(t *testing.T) {
	s := newTestServer(t)
	// Calling EnsureBootstrap again must not create duplicate admins/nodes.
	if err := s.svc.EnsureBootstrap(); err != nil {
		t.Fatal(err)
	}
	users, _ := s.svc.Store.ListUsers(context.Background())
	nodes, _ := s.svc.Store.ListNodes(context.Background())
	if len(users) != 1 || len(nodes) != 1 {
		t.Fatalf("expected 1 user and 1 node, got %d/%d", len(users), len(nodes))
	}
}

func encodePKCS1PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
