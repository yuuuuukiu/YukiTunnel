// Package servercli implements `yuki-server`, the server administration CLI.
// Every command works in two modes:
//
//   - local:  opens the SQLite database directly (bootstrap, air-gapped hosts)
//   - remote: talks to a running server over its HTTPS API (containers, remote
//     hosts, CI)
//
// Both modes share the same JSON shapes so scripts behave identically.
package servercli

import "time"

// User is an account row.
type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`
	KeyCount    int       `json:"key_count"`
	TunnelCount int       `json:"tunnel_count"`
}

// UserKey is an RSA/SSH public key row.
type UserKey struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	CreatedAt   time.Time `json:"created_at"`
}

// Node is a public ingress point.
type Node struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Region      string    `json:"region"`
	PublicHost  string    `json:"public_host"`
	ControlHost string    `json:"control_host"`
	ControlPort int       `json:"control_port"`
	Enabled     bool      `json:"enabled"`
	Status      string    `json:"status"`
	LatencyMS   int       `json:"latency_ms"`
	MaxTunnels  int       `json:"max_tunnels"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// Tunnel is a tunnel row with its public address.
type Tunnel struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	UserName   string `json:"user_name,omitempty"`
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name"`
	Name       string `json:"name"`
	Proto      string `json:"proto"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	PublicPort int    `json:"public_port"`
	Domain     string `json:"domain"`
	Address    string `json:"address"`
	Enabled    bool   `json:"enabled"`
	Active     bool   `json:"active"`
	RxBytes    int64  `json:"rx_bytes"`
	TxBytes    int64  `json:"tx_bytes"`
}

// PortUsage is the allocatable port range plus per-node allocations.
type PortUsage struct {
	Range struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"range"`
	Nodes []struct {
		NodeID string `json:"node_id"`
		Name   string `json:"name"`
		Used   []int  `json:"used"`
	} `json:"nodes"`
}

// Settings is the runtime server configuration exposed to admins.
type Settings struct {
	DomainBinding bool   `json:"domain_binding"`
	DomainSuffix  string `json:"domain_suffix"`
	PublicHost    string `json:"public_host"`
	PortMin       int    `json:"port_min"`
	PortMax       int    `json:"port_max"`
	APIAddr       string `json:"api_addr"`
	ControlAddr   string `json:"control_addr"`
	Version       string `json:"version"`
}

// Audit is one audit log line.
type Audit struct {
	ID         string    `json:"id"`
	ActorName  string    `json:"actor_name"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Detail     string    `json:"detail"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateUserRequest is shared by both backends.
type CreateUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	PublicKey   string `json:"public_key,omitempty"`
	KeyName     string `json:"key_name,omitempty"`
}

// UpdateUserRequest patches a user.
type UpdateUserRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	Password    *string `json:"password,omitempty"`
	Role        *string `json:"role,omitempty"`
	Disabled    *bool   `json:"disabled,omitempty"`
}

// NodeRequest creates or patches a node.
type NodeRequest struct {
	Name        string `json:"name"`
	Region      string `json:"region"`
	PublicHost  string `json:"public_host"`
	ControlHost string `json:"control_host"`
	ControlPort int    `json:"control_port"`
	MaxTunnels  int    `json:"max_tunnels"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// SettingsPatch updates runtime settings.
type SettingsPatch struct {
	DomainBinding *bool   `json:"domain_binding,omitempty"`
	DomainSuffix  *string `json:"domain_suffix,omitempty"`
	PublicHost    *string `json:"public_host,omitempty"`
	PortMin       *int    `json:"port_min,omitempty"`
	PortMax       *int    `json:"port_max,omitempty"`
}
