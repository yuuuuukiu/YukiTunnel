package servercli

import (
	"context"
	"fmt"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
)

// RemoteBackend talks to a running server's admin API.
type RemoteBackend struct {
	client *cliutil.Client
}

// NewRemoteBackend builds a backend for serverURL using token or password.
func NewRemoteBackend(serverURL, token, username, password string, insecure bool) (*RemoteBackend, error) {
	cfg := &cliutil.ClientConfig{
		ServerURL:          cliutil.NormalizeServerURL(serverURL),
		Token:              token,
		Username:           username,
		InsecureSkipVerify: insecure,
	}
	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("请提供 --server 地址")
	}
	if cfg.Token == "" {
		if username == "" || password == "" {
			return nil, fmt.Errorf("远程模式需要 --token 或 --username/--password")
		}
		if _, err := cliutil.Login(cfg, username, password); err != nil {
			return nil, err
		}
	}
	return &RemoteBackend{client: cliutil.NewClient(cfg)}, nil
}

func (r *RemoteBackend) Mode() string { return "remote" }
func (r *RemoteBackend) Close() error { return nil }

func (r *RemoteBackend) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := r.client.Do("GET", "/health", nil, &out)
	return out, err
}

func (r *RemoteBackend) Settings(ctx context.Context) (*Settings, error) {
	var out struct {
		Settings Settings `json:"settings"`
	}
	if err := r.client.Do("GET", "/admin/settings", nil, &out); err != nil {
		return nil, err
	}
	return &out.Settings, nil
}

func (r *RemoteBackend) UpdateSettings(ctx context.Context, p SettingsPatch) (*Settings, error) {
	var out struct {
		Settings Settings `json:"settings"`
	}
	if err := r.client.Do("PUT", "/admin/settings", p, &out); err != nil {
		return nil, err
	}
	return &out.Settings, nil
}

func (r *RemoteBackend) ListUsers(ctx context.Context) ([]User, error) {
	var out struct {
		Users []User `json:"users"`
	}
	if err := r.client.Do("GET", "/admin/users", nil, &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

func (r *RemoteBackend) CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) {
	var out struct {
		User User `json:"user"`
	}
	if err := r.client.Do("POST", "/admin/users", req, &out); err != nil {
		return nil, err
	}
	return &out.User, nil
}

func (r *RemoteBackend) UpdateUser(ctx context.Context, id string, req UpdateUserRequest) (*User, error) {
	var out struct {
		User User `json:"user"`
	}
	if err := r.client.Do("PATCH", "/admin/users/"+id, req, &out); err != nil {
		return nil, err
	}
	return &out.User, nil
}

func (r *RemoteBackend) DeleteUser(ctx context.Context, id string) error {
	return r.client.Do("DELETE", "/admin/users/"+id, nil, nil)
}

func (r *RemoteBackend) ListKeys(ctx context.Context, userID string) ([]UserKey, error) {
	var out struct {
		Keys []UserKey `json:"keys"`
	}
	if err := r.client.Do("GET", "/admin/users/"+userID+"/keys", nil, &out); err != nil {
		return nil, err
	}
	return out.Keys, nil
}

func (r *RemoteBackend) AddKey(ctx context.Context, userID, name, publicKey string) (*UserKey, error) {
	var out struct {
		Key UserKey `json:"key"`
	}
	if err := r.client.Do("POST", "/admin/users/"+userID+"/keys", map[string]string{"name": name, "public_key": publicKey}, &out); err != nil {
		return nil, err
	}
	return &out.Key, nil
}

func (r *RemoteBackend) DeleteKey(ctx context.Context, userID, keyID string) error {
	return r.client.Do("DELETE", "/admin/users/"+userID+"/keys/"+keyID, nil, nil)
}

func (r *RemoteBackend) ListNodes(ctx context.Context) ([]Node, error) {
	var out struct {
		Nodes []Node `json:"nodes"`
	}
	if err := r.client.Do("GET", "/admin/nodes", nil, &out); err != nil {
		return nil, err
	}
	return out.Nodes, nil
}

func (r *RemoteBackend) CreateNode(ctx context.Context, req NodeRequest) (*Node, error) {
	var out struct {
		Node Node `json:"node"`
	}
	if err := r.client.Do("POST", "/admin/nodes", req, &out); err != nil {
		return nil, err
	}
	return &out.Node, nil
}

func (r *RemoteBackend) UpdateNode(ctx context.Context, id string, req NodeRequest) (*Node, error) {
	var out struct {
		Node Node `json:"node"`
	}
	if err := r.client.Do("PATCH", "/admin/nodes/"+id, req, &out); err != nil {
		return nil, err
	}
	return &out.Node, nil
}

func (r *RemoteBackend) DeleteNode(ctx context.Context, id string) error {
	return r.client.Do("DELETE", "/admin/nodes/"+id, nil, nil)
}

func (r *RemoteBackend) CheckNode(ctx context.Context, id string) (*Node, error) {
	var out struct {
		NodeID    string `json:"node_id"`
		Status    string `json:"status"`
		LatencyMS int    `json:"latency_ms"`
	}
	if err := r.client.Do("POST", "/admin/nodes/"+id+"/check", nil, &out); err != nil {
		return nil, err
	}
	return &Node{ID: out.NodeID, Status: out.Status, LatencyMS: out.LatencyMS}, nil
}

func (r *RemoteBackend) ListTunnels(ctx context.Context, userID string) ([]Tunnel, error) {
	path := "/admin/tunnels"
	if userID != "" {
		path += "?user_id=" + userID
	}
	var out struct {
		Tunnels []Tunnel `json:"tunnels"`
	}
	if err := r.client.Do("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.Tunnels, nil
}

func (r *RemoteBackend) EnableTunnel(ctx context.Context, id string, enabled bool) (*Tunnel, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var out struct {
		Tunnel Tunnel `json:"tunnel"`
	}
	if err := r.client.Do("POST", "/admin/tunnels/"+id+"/"+action, nil, &out); err != nil {
		return nil, err
	}
	return &out.Tunnel, nil
}

func (r *RemoteBackend) DeleteTunnel(ctx context.Context, id string) error {
	return r.client.Do("DELETE", "/admin/tunnels/"+id, nil, nil)
}

func (r *RemoteBackend) ListAudit(ctx context.Context, limit, offset int) ([]Audit, error) {
	var out struct {
		Entries []Audit `json:"entries"`
	}
	path := fmt.Sprintf("/admin/audit?limit=%d&offset=%d", limit, offset)
	if err := r.client.Do("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

func (r *RemoteBackend) PortUsage(ctx context.Context) (*PortUsage, error) {
	var out PortUsage
	if err := r.client.Do("GET", "/admin/ports", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
