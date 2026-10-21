package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	c := Default(t.TempDir())
	c.Normalize()
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	if c.APIAddr != DefaultAPIAddr || c.ControlAddr != DefaultControlAddr {
		t.Fatalf("unexpected defaults: %s %s", c.APIAddr, c.ControlAddr)
	}
	if c.Ports.Min == 0 || c.Ports.Max < c.Ports.Min {
		t.Fatalf("bad default port range: %d-%d", c.Ports.Min, c.Ports.Max)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"api_addr without port", func(c *Config) { c.APIAddr = "127.0.0.1" }},
		{"control_addr without port", func(c *Config) { c.ControlAddr = "nope" }},
		{"port min zero", func(c *Config) { c.Ports.Min = 0 }},
		{"port max too high", func(c *Config) { c.Ports.Max = 70000 }},
		{"port range inverted", func(c *Config) { c.Ports.Min, c.Ports.Max = 30000, 20000 }},
		{"binding without suffix", func(c *Config) { c.Domain.Binding = true; c.Domain.Suffix = "" }},
		{"require_tls without cert", func(c *Config) {
			c.Auth.RequireTLS = true
			c.TLS.CertFile = ""
			c.TLS.AutoSelfSigned = false
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default(t.TempDir())
			c.Normalize()
			tc.mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestPortInRange(t *testing.T) {
	c := Default(t.TempDir())
	c.Normalize()
	c.Ports.Min, c.Ports.Max = 20000, 20010

	if !c.PortInRange(20000) || !c.PortInRange(20010) || !c.PortInRange(20005) {
		t.Fatal("in-range ports rejected")
	}
	if c.PortInRange(19999) || c.PortInRange(20011) {
		t.Fatal("out-of-range ports accepted")
	}

	// An explicit allowlist overrides the range entirely.
	c.Ports.Allowlist = []int{30001, 30005}
	if !c.PortInRange(30001) || !c.PortInRange(30005) {
		t.Fatal("allowlisted ports rejected")
	}
	if c.PortInRange(20005) {
		t.Fatal("range should be ignored when an allowlist exists")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := Default(dir)
	c.Normalize()
	c.PublicHost = "203.0.113.7"
	c.Ports.Min, c.Ports.Max = 21000, 21100
	c.Domain.Suffix = "tunnel.example.com"
	c.Domain.Binding = true
	c.Auth.MaxFailedLogins = 5

	path := Path(dir)
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("config must be 0600 (it holds no secrets today but will), got %o", mode)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.PublicHost != "203.0.113.7" || got.Ports.Min != 21000 ||
		got.Domain.Suffix != "tunnel.example.com" || !got.Domain.Binding ||
		got.Auth.MaxFailedLogins != 5 {
		t.Fatalf("round-trip lost data: %+v", got)
	}
}

func TestLoadOrEmpty(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadOrEmpty(Path(dir))
	if err != nil {
		t.Fatalf("missing file should not be an error: %v", err)
	}
	if c == nil {
		t.Fatal("expected a default config")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("returned config must be usable: %v", err)
	}
}

func TestApplyEnv(t *testing.T) {
	t.Setenv("YUKI_API_ADDR", "0.0.0.0:18443")
	t.Setenv("YUKI_CONTROL_ADDR", "0.0.0.0:19443")
	t.Setenv("YUKI_PUBLIC_HOST", "tunnel.example.com")
	t.Setenv("YUKI_PORT_MIN", "22000")
	t.Setenv("YUKI_PORT_MAX", "22100")
	t.Setenv("YUKI_DOMAIN_BINDING", "true")
	t.Setenv("YUKI_DOMAIN_SUFFIX", "tunnel.example.com")
	t.Setenv("YUKI_TOKEN_TTL_HOURS", "24")

	c := Default(t.TempDir())
	c.ApplyEnv()
	c.Normalize()

	if c.APIAddr != "0.0.0.0:18443" || c.ControlAddr != "0.0.0.0:19443" {
		t.Fatalf("addresses not applied: %s %s", c.APIAddr, c.ControlAddr)
	}
	if c.PublicHost != "tunnel.example.com" {
		t.Fatalf("public host not applied: %q", c.PublicHost)
	}
	if c.Ports.Min != 22000 || c.Ports.Max != 22100 {
		t.Fatalf("ports not applied: %d-%d", c.Ports.Min, c.Ports.Max)
	}
	if !c.Domain.Binding || c.Domain.Suffix != "tunnel.example.com" {
		t.Fatalf("domain settings not applied: %+v", c.Domain)
	}
	if c.Auth.TokenTTLHours != 24 {
		t.Fatalf("ttl not applied: %d", c.Auth.TokenTTLHours)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("env-applied config must validate: %v", err)
	}
}

func TestApplyEnvIgnoresGarbage(t *testing.T) {
	t.Setenv("YUKI_PORT_MIN", "not-a-number")
	t.Setenv("YUKI_DOMAIN_BINDING", "perhaps")

	c := Default(t.TempDir())
	before := c.Ports.Min
	c.ApplyEnv()
	if c.Ports.Min != before {
		t.Fatalf("garbage int should be ignored, got %d", c.Ports.Min)
	}
	if c.Domain.Binding {
		t.Fatal("garbage bool should be ignored")
	}
}

func TestNormalizeFillsZeroValues(t *testing.T) {
	c := &Config{}
	c.Normalize()
	if c.APIAddr == "" || c.ControlAddr == "" {
		t.Fatal("addresses not defaulted")
	}
	if c.Auth.LockoutMinutes == 0 || c.Auth.MaxFailedLogins == 0 {
		t.Fatalf("auth defaults missing: %+v", c.Auth)
	}
	if c.Ports.Min == 0 || c.Ports.Max == 0 {
		t.Fatalf("port defaults missing: %+v", c.Ports)
	}
}

func TestTokenTTL(t *testing.T) {
	c := Default(t.TempDir())
	c.Normalize()
	c.Auth.TokenTTLHours = 12
	if got := c.TokenTTL().Hours(); got != 12 {
		t.Fatalf("TokenTTL = %v, want 12h", got)
	}
}

func TestPathUnderDataDir(t *testing.T) {
	dir := filepath.Join("tmp", "yt")
	if got := Path(dir); got != filepath.Join(dir, "config.yaml") {
		t.Fatalf("Path = %q", got)
	}
}
