package servercli

import (
	"context"
)

// Backend abstracts local (direct DB) and remote (HTTPS API) administration.
type Backend interface {
	// Mode reports "local" or "remote".
	Mode() string
	// Close releases resources (database handles, idle connections).
	Close() error

	Health(ctx context.Context) (map[string]any, error)
	Settings(ctx context.Context) (*Settings, error)
	UpdateSettings(ctx context.Context, p SettingsPatch) (*Settings, error)

	ListUsers(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, req CreateUserRequest) (*User, error)
	UpdateUser(ctx context.Context, id string, req UpdateUserRequest) (*User, error)
	DeleteUser(ctx context.Context, id string) error
	ListKeys(ctx context.Context, userID string) ([]UserKey, error)
	AddKey(ctx context.Context, userID, name, publicKey string) (*UserKey, error)
	DeleteKey(ctx context.Context, userID, keyID string) error

	ListNodes(ctx context.Context) ([]Node, error)
	CreateNode(ctx context.Context, req NodeRequest) (*Node, error)
	UpdateNode(ctx context.Context, id string, req NodeRequest) (*Node, error)
	DeleteNode(ctx context.Context, id string) error
	CheckNode(ctx context.Context, id string) (*Node, error)

	ListTunnels(ctx context.Context, userID string) ([]Tunnel, error)
	EnableTunnel(ctx context.Context, id string, enabled bool) (*Tunnel, error)
	DeleteTunnel(ctx context.Context, id string) error

	ListAudit(ctx context.Context, limit, offset int) ([]Audit, error)
	PortUsage(ctx context.Context) (*PortUsage, error)
}
