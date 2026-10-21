// Package config loads and persists YukiTunnel server configuration.
//
// Precedence (highest wins): command line flag > environment variable > config
// file > built-in default. The file format is YAML but end users are not
// expected to edit it: the desktop installer and `yuki-server init` write it.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults for a fresh installation.
const (
	DefaultAPIAddr      = "0.0.0.0:8443"
	DefaultControlAddr  = "0.0.0.0:9443"
	DefaultDataDirName  = "yukitunnel"
	DefaultPublicHost   = ""
	DefaultPortRangeLo  = 20000
	DefaultPortRangeHi  = 30000
	DefaultTokenTTLHour = 24 * 7
)

// Config is the on-disk server configuration.
type Config struct {
	// APIAddr is the HTTPS listen address for REST API + WebUI.
	APIAddr string `yaml:"api_addr"`
	// ControlAddr is the mTLS/token authenticated tunnel control listener.
	ControlAddr string `yaml:"control_addr"`
	// PublicHost is the address advertised to clients when domain binding is
	// disabled. Empty means "auto-detect from the node record / request host".
	PublicHost string `yaml:"public_host"`
	// DataDir holds the SQLite database, TLS material, logs and the server
	// identity key.
	DataDir string `yaml:"data_dir"`
	// DatabaseURL is an explicit SQLite DSN. Empty derives it from DataDir.
	DatabaseURL string `yaml:"database_url"`

	TLS    TLSConfig    `yaml:"tls"`
	Domain DomainConfig `yaml:"domain"`
	Ports  PortsConfig  `yaml:"ports"`
	Auth   AuthConfig   `yaml:"auth"`
	Limits LimitsConfig `yaml:"limits"`
	Log    LogConfig    `yaml:"log"`
	WebUI  WebUIConfig  `yaml:"webui"`
}

type TLSConfig struct {
	// CertFile/KeyFile may be provided by the operator (e.g. Let's Encrypt).
	// When empty the server generates and persists a self-signed certificate.
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// AutoSelfSigned enables generation when CertFile is empty.
	AutoSelfSigned bool `yaml:"auto_self_signed"`
}

type DomainConfig struct {
	// Binding is the global "bind domain instead of public IP" switch. It can be
	// toggled at runtime from the WebUI / desktop app / server CLI.
	Binding bool `yaml:"binding"`
	// Suffix is appended to tunnel names to build a public hostname, e.g.
	// "t-1a2b.tunnel.example.com" when Suffix is "tunnel.example.com".
	Suffix string `yaml:"suffix"`
	// BaseDomain is used for the WebUI when a reverse proxy terminates TLS.
	BaseDomain string `yaml:"base_domain"`
}

type PortsConfig struct {
	// Range is the allocatable public port range for TCP/UDP tunnels.
	Min int `yaml:"min"`
	Max int `yaml:"max"`
	// Allowlist restricts the explicitly requested ports (empty = whole range).
	Allowlist []int `yaml:"allowlist"`
}

type AuthConfig struct {
	// TokenTTLHours is the lifetime of an issued API session token.
	TokenTTLHours int `yaml:"token_ttl_hours"`
	// MaxFailedLogins per account+IP before a temporary lockout.
	MaxFailedLogins int `yaml:"max_failed_logins"`
	// LockoutMinutes is the lockout duration.
	LockoutMinutes int `yaml:"lockout_minutes"`
	// RequireTLS refuses to start without TLS material.
	RequireTLS bool `yaml:"require_tls"`
}

type LimitsConfig struct {
	// DefaultBandwidthKbps is applied to new tunnels (0 = unlimited).
	DefaultBandwidthKbps int `yaml:"default_bandwidth_kbps"`
	// DefaultMaxConnections is applied to new tunnels (0 = unlimited).
	DefaultMaxConnections int `yaml:"default_max_connections"`
}

type LogConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

type WebUIConfig struct {
	// Dir is an optional directory with built WebUI assets. Empty falls back to
	// the embedded bundle.
	Dir string `yaml:"dir"`
}

// Default returns a fully populated configuration rooted at dataDir.
func Default(dataDir string) *Config {
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	return &Config{
		APIAddr:     DefaultAPIAddr,
		ControlAddr: DefaultControlAddr,
		DataDir:     dataDir,
		TLS:         TLSConfig{AutoSelfSigned: true},
		Domain:      DomainConfig{Binding: false},
		Ports:       PortsConfig{Min: DefaultPortRangeLo, Max: DefaultPortRangeHi},
		Auth: AuthConfig{
			TokenTTLHours:   DefaultTokenTTLHour,
			MaxFailedLogins: 10,
			LockoutMinutes:  15,
			RequireTLS:      true,
		},
		Log: LogConfig{Level: "info"},
	}
}

// DefaultDataDir returns the platform-appropriate default data directory.
func DefaultDataDir() string {
	if v := os.Getenv("YUKI_DATA_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("YUKI_HOME"); v != "" {
		return v
	}
	if os.Geteuid() == 0 {
		return "/var/lib/yukitunnel"
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, DefaultDataDirName)
}

// Path returns the config file path inside dataDir.
func Path(dataDir string) string { return filepath.Join(dataDir, "config.yaml") }

// Load reads a config file, applies env overrides and normalizes values.
func Load(path string) (*Config, error) {
	cfg := Default(filepath.Dir(path))
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.ApplyEnv()
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadOrEmpty loads a config or returns defaults when the file is missing.
func LoadOrEmpty(path string) (*Config, error) {
	cfg, err := Load(path)
	if errors.Is(err, os.ErrNotExist) {
		c := Default(filepath.Dir(path))
		c.ApplyEnv()
		c.Normalize()
		return c, nil
	}
	return cfg, err
}

// Save writes the config atomically with 0600 permissions.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ApplyEnv overlays YUKI_* environment variables (12-factor friendly for
// Docker Compose).
func (c *Config) ApplyEnv() {
	setStr(&c.APIAddr, "YUKI_API_ADDR")
	setStr(&c.ControlAddr, "YUKI_CONTROL_ADDR")
	setStr(&c.PublicHost, "YUKI_PUBLIC_HOST")
	setStr(&c.DataDir, "YUKI_DATA_DIR")
	setStr(&c.DatabaseURL, "YUKI_DATABASE_URL")
	setStr(&c.TLS.CertFile, "YUKI_TLS_CERT")
	setStr(&c.TLS.KeyFile, "YUKI_TLS_KEY")
	setStr(&c.Domain.Suffix, "YUKI_DOMAIN_SUFFIX")
	setStr(&c.Domain.BaseDomain, "YUKI_BASE_DOMAIN")
	setStr(&c.Log.Level, "YUKI_LOG_LEVEL")
	setStr(&c.Log.File, "YUKI_LOG_FILE")
	setStr(&c.WebUI.Dir, "YUKI_WEBUI_DIR")
	setBool(&c.Domain.Binding, "YUKI_DOMAIN_BINDING")
	setInt(&c.Ports.Min, "YUKI_PORT_MIN")
	setInt(&c.Ports.Max, "YUKI_PORT_MAX")
	setInt(&c.Auth.TokenTTLHours, "YUKI_TOKEN_TTL_HOURS")
	setInt(&c.Auth.MaxFailedLogins, "YUKI_MAX_FAILED_LOGINS")
}

func setStr(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		*dst = v
	}
}

func setInt(dst *int, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func setBool(dst *bool, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

// Normalize fills empty fields with defaults and cleans addresses.
func (c *Config) Normalize() {
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if c.APIAddr == "" {
		c.APIAddr = DefaultAPIAddr
	}
	if c.ControlAddr == "" {
		c.ControlAddr = DefaultControlAddr
	}
	if c.Ports.Min == 0 {
		c.Ports.Min = DefaultPortRangeLo
	}
	if c.Ports.Max == 0 {
		c.Ports.Max = DefaultPortRangeHi
	}
	if c.Auth.TokenTTLHours == 0 {
		c.Auth.TokenTTLHours = DefaultTokenTTLHour
	}
	if c.Auth.MaxFailedLogins == 0 {
		c.Auth.MaxFailedLogins = 10
	}
	if c.Auth.LockoutMinutes == 0 {
		c.Auth.LockoutMinutes = 15
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL = "file:" + filepath.Join(c.DataDir, "yukitunnel.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	c.Domain.Suffix = strings.Trim(strings.ToLower(strings.TrimSpace(c.Domain.Suffix)), ".")
	c.PublicHost = strings.TrimSpace(c.PublicHost)
}

// Validate checks invariants that must hold before the server starts.
func (c *Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.APIAddr); err != nil {
		return fmt.Errorf("api_addr %q is not host:port: %w", c.APIAddr, err)
	}
	if _, _, err := net.SplitHostPort(c.ControlAddr); err != nil {
		return fmt.Errorf("control_addr %q is not host:port: %w", c.ControlAddr, err)
	}
	if c.Ports.Min < 1 || c.Ports.Max > 65535 || c.Ports.Min > c.Ports.Max {
		return fmt.Errorf("invalid port range %d-%d", c.Ports.Min, c.Ports.Max)
	}
	if c.Domain.Binding && c.Domain.Suffix == "" {
		return errors.New("domain.binding is enabled but domain.suffix is empty")
	}
	if c.Auth.RequireTLS && c.TLS.CertFile == "" && !c.TLS.AutoSelfSigned {
		return errors.New("tls.require_tls is set but no certificate is configured")
	}
	return nil
}

// TokenTTL returns the session token lifetime.
func (c *Config) TokenTTL() time.Duration {
	return time.Duration(c.Auth.TokenTTLHours) * time.Hour
}

// PortInRange reports whether p is allocatable.
func (c *Config) PortInRange(p int) bool {
	if len(c.Ports.Allowlist) > 0 {
		for _, a := range c.Ports.Allowlist {
			if a == p {
				return true
			}
		}
		return false
	}
	return p >= c.Ports.Min && p <= c.Ports.Max
}
