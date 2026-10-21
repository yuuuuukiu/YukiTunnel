// Package cliutil provides the shared CLI plumbing: persistent client config,
// an HTTPS API client, human/JSON output and error rendering.
package cliutil

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ClientConfig is the persistent CLI/GUI connection profile.
type ClientConfig struct {
	ServerURL   string    `yaml:"server_url" json:"server_url"`
	Username    string    `yaml:"username" json:"username"`
	Token       string    `yaml:"token,omitempty" json:"-"`
	TokenExpiry time.Time `yaml:"token_expiry,omitempty" json:"-"`
	// KeyPath is the RSA private key used for key authentication.
	KeyPath string `yaml:"key_path,omitempty" json:"key_path,omitempty"`
	// InsecureSkipVerify trusts self-signed server certificates.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
	// Fingerprint pins the server certificate.
	Fingerprint string `yaml:"fingerprint,omitempty" json:"fingerprint,omitempty"`
	// AutoReconnect keeps `yuki run` alive across network changes.
	AutoReconnect bool `yaml:"auto_reconnect" json:"auto_reconnect"`
}

// ConfigDir returns the per-user config directory.
func ConfigDir() string {
	if v := os.Getenv("YUKI_CLIENT_HOME"); v != "" {
		return v
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, "yukitunnel")
}

// ClientConfigPath returns the CLI/GUI profile path.
func ClientConfigPath() string { return filepath.Join(ConfigDir(), "client.yaml") }

// LoadClientConfig reads the profile; missing files return an empty config.
func LoadClientConfig() (*ClientConfig, error) {
	path := ClientConfigPath()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &ClientConfig{AutoReconnect: true}, nil
	}
	if err != nil {
		return nil, err
	}
	var c ClientConfig
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("配置文件损坏：%w", err)
	}
	return &c, nil
}

// Save writes the profile with 0600 permissions.
func (c *ClientConfig) Save() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(ClientConfigPath(), b, 0o600)
}

// NormalizeServerURL adds https:// and strips trailing slashes.
func NormalizeServerURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, "/api/v1")
	return s
}

// APIError mirrors the server envelope.
type APIError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Code)
}

// Client is a thin HTTPS API client.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient builds a client from a profile.
func NewClient(cfg *ClientConfig) *Client {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.InsecureSkipVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // explicit user opt-in for self-signed
	}
	tr := &http.Transport{
		TLSClientConfig:     tlsCfg,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
	}
	return &Client{
		BaseURL: NormalizeServerURL(cfg.ServerURL),
		Token:   cfg.Token,
		HTTP:    &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}
}

// Do performs a JSON request. out may be nil.
func (c *Client) Do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	url := c.BaseURL + "/api/v1" + path
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s", HumanError(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var env struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error != nil {
			env.Error.Status = resp.StatusCode
			return env.Error
		}
		return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("服务端返回了无法解析的数据：%w", err)
		}
	}
	return nil
}

// HumanError maps transport errors to readable Chinese text.
func HumanError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	// Multi-line errors already carry their own structured context (mirror
	// lists, per-source failures). Collapsing them into a generic message would
	// hide which mirror failed and why.
	if strings.Contains(s, "\n") {
		return s
	}
	switch {
	case strings.Contains(s, "connection refused"):
		return "服务端地址不可达（连接被拒绝）"
	case strings.Contains(s, "no such host"):
		return "无法解析服务端域名"
	case strings.Contains(s, "certificate"):
		return "TLS 证书校验失败：如使用自签名证书，请加 --insecure 或信任该证书"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"):
		return "连接服务端超时"
	}
	return s
}

// RenderError prints an API or transport error for humans.
func RenderError(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		if api.Message != "" {
			return api.Message
		}
		return api.Code
	}
	return HumanError(err)
}
