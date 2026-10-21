package cliutil

import (
	"crypto/rsa"
	"fmt"
	"os"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/auth"
)

// User mirrors the API user object.
type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

// LoginResult is the API login response.
type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

// Login performs password authentication and persists the token.
func Login(cfg *ClientConfig, username, password string) (*LoginResult, error) {
	c := NewClient(cfg)
	var res LoginResult
	if err := c.Do("POST", "/auth/login", map[string]string{"username": username, "password": password}, &res); err != nil {
		return nil, err
	}
	cfg.Token = res.Token
	cfg.TokenExpiry = res.ExpiresAt
	cfg.Username = res.User.Username
	return &res, nil
}

// LoginWithKey performs RSA challenge-response authentication.
func LoginWithKey(cfg *ClientConfig, username, keyPath string) (*LoginResult, error) {
	pem, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取私钥文件 %s：%w", keyPath, err)
	}
	key, err := auth.ParsePrivateKeyPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("私钥格式错误：%w", err)
	}
	return loginWithRSA(cfg, username, key)
}

func loginWithRSA(cfg *ClientConfig, username string, key *rsa.PrivateKey) (*LoginResult, error) {
	c := NewClient(cfg)
	var ch struct {
		Nonce string `json:"nonce"`
	}
	if err := c.Do("POST", "/auth/key/challenge", map[string]string{"username": username}, &ch); err != nil {
		return nil, err
	}
	if ch.Nonce == "" {
		return nil, fmt.Errorf("服务端未返回挑战码")
	}
	sig, err := auth.SignChallenge(key, ch.Nonce)
	if err != nil {
		return nil, err
	}
	var res LoginResult
	if err := c.Do("POST", "/auth/key/login", map[string]string{
		"username": username, "nonce": ch.Nonce, "signature": sig,
	}, &res); err != nil {
		return nil, err
	}
	cfg.Token = res.Token
	cfg.TokenExpiry = res.ExpiresAt
	cfg.Username = res.User.Username
	return &res, nil
}

// ServerView mirrors the API node listing.
type ServerView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Region     string `json:"region"`
	PublicHost string `json:"public_host"`
	Status     string `json:"status"`
	Online     bool   `json:"online"`
	LatencyMS  int    `json:"latency_ms"`
	Domain     string `json:"domain,omitempty"`
	PortRange  [2]int `json:"port_range"`
	MaxTunnels int    `json:"max_tunnels"`
}

// Tunnel mirrors the API tunnel object.
type Tunnel struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	NodeID      string    `json:"node_id"`
	NodeName    string    `json:"node_name"`
	Name        string    `json:"name"`
	Proto       string    `json:"proto"`
	LocalIP     string    `json:"local_ip"`
	LocalPort   int       `json:"local_port"`
	PublicPort  int       `json:"public_port"`
	Domain      string    `json:"domain"`
	Address     string    `json:"address"`
	PublicHost  string    `json:"public_host"`
	Active      bool      `json:"active"`
	Enabled     bool      `json:"enabled"`
	RxBytes     int64     `json:"rx_bytes"`
	TxBytes     int64     `json:"tx_bytes"`
	Connections int64     `json:"connections"`
	ActiveConns int       `json:"active_connections"`
	CreatedAt   time.Time `json:"created_at"`
}

// ServerList is the GET /servers response.
type ServerList struct {
	Servers       []ServerView `json:"servers"`
	DomainBinding bool         `json:"domain_binding"`
	DomainSuffix  string       `json:"domain_suffix"`
}

// ListServers fetches nodes with latency.
func ListServers(cfg *ClientConfig) (*ServerList, error) {
	c := NewClient(cfg)
	var out ServerList
	if err := c.Do("GET", "/servers", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTunnels fetches the caller's tunnels.
func ListTunnels(cfg *ClientConfig) ([]Tunnel, error) {
	c := NewClient(cfg)
	var out struct {
		Tunnels []Tunnel `json:"tunnels"`
	}
	if err := c.Do("GET", "/tunnels", nil, &out); err != nil {
		return nil, err
	}
	return out.Tunnels, nil
}

// CreateTunnelRequest is the payload for POST /tunnels.
type CreateTunnelRequest struct {
	Name       string `json:"name,omitempty"`
	NodeID     string `json:"node_id"`
	Proto      string `json:"proto"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	PublicPort int    `json:"public_port,omitempty"`
	UseDomain  *bool  `json:"use_domain,omitempty"`
}

// CreateTunnel creates a tunnel and returns the API view.
func CreateTunnel(cfg *ClientConfig, req CreateTunnelRequest) (*Tunnel, error) {
	c := NewClient(cfg)
	var out struct {
		Tunnel Tunnel `json:"tunnel"`
	}
	if err := c.Do("POST", "/tunnels", req, &out); err != nil {
		return nil, err
	}
	return &out.Tunnel, nil
}

// TunnelAction starts or stops a tunnel.
func TunnelAction(cfg *ClientConfig, id, action string) (*Tunnel, error) {
	c := NewClient(cfg)
	var out struct {
		Tunnel Tunnel `json:"tunnel"`
	}
	if err := c.Do("POST", "/tunnels/"+id+"/"+action, nil, &out); err != nil {
		return nil, err
	}
	return &out.Tunnel, nil
}

// DeleteTunnel removes a tunnel.
func DeleteTunnel(cfg *ClientConfig, id string) error {
	c := NewClient(cfg)
	return c.Do("DELETE", "/tunnels/"+id, nil, nil)
}

// Logout revokes the current token server-side and clears it locally.
func Logout(cfg *ClientConfig) error {
	c := NewClient(cfg)
	if cfg.Token != "" {
		_ = c.Do("POST", "/auth/logout", nil, nil)
	}
	cfg.Token = ""
	cfg.TokenExpiry = time.Time{}
	return nil
}
