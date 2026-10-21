package store

import "time"

// User is an account provisioned by an administrator (no self-registration).
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastLoginAt  time.Time `json:"last_login_at"`
}

// IsAdmin reports whether the user may administer the server.
func (u *User) IsAdmin() bool { return u.Role == "admin" }

// UserKey is an authorized RSA/SSH public key.
type UserKey struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
}

// Node is a public ingress point (a machine running yuki-server control port).
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
	LastSeenAt  time.Time `json:"last_seen_at"`
	MaxTunnels  int       `json:"max_tunnels"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Tunnel is the desired state of a port/domain forward. Runtime state lives in
// internal/tunnel.
type Tunnel struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	NodeID         string `json:"node_id"`
	Name           string `json:"name"`
	Proto          string `json:"proto"`
	LocalIP        string `json:"local_ip"`
	LocalPort      int    `json:"local_port"`
	PublicPort     int    `json:"public_port"`
	Domain         string `json:"domain"`
	Enabled        bool   `json:"enabled"`
	BandwidthKbps  int    `json:"bandwidth_kbps"`
	MaxConnections int    `json:"max_connections"`
	// DesiredRunning is the user's intent. The server keeps the public
	// listener up whenever an agent is connected and this flag is set.
	DesiredRunning bool      `json:"desired_running"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastStartedAt  time.Time `json:"last_started_at"`
}

// AuditEntry is one recorded administrative or security-relevant action.
type AuditEntry struct {
	ID         string    `json:"id"`
	ActorID    string    `json:"actor_id"`
	ActorName  string    `json:"actor_name"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Detail     string    `json:"detail"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
}

// APIToken is a hashed bearer token. The plaintext is only returned once.
type APIToken struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Name       string    `json:"name"`
	Scope      string    `json:"scope"`
	TokenHash  string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	RevokedAt  time.Time `json:"revoked_at"`
}

func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromTS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}
