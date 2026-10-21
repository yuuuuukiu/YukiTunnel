package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/config"
)

// EmbeddedOptions configures the in-process server used by the desktop
// installer ("one-click deploy" without Docker).
type EmbeddedOptions struct {
	DataDir string
	// APIPort and ControlPort are user chosen in the installer wizard.
	APIPort       int
	ControlPort   int
	PublicHost    string
	AdminUser     string
	AdminPass     string
	DomainBinding bool
	DomainSuffix  string
	TLSCert       string
	TLSKey        string
}

// RunEmbedded starts a server in the current process until ctx is cancelled.
// The returned channel receives readiness (nil error) or the startup error.
func RunEmbedded(ctx context.Context, opts EmbeddedOptions) (<-chan error, *Service, error) {
	if opts.APIPort == 0 {
		opts.APIPort = 8443
	}
	if opts.ControlPort == 0 {
		opts.ControlPort = 9443
	}
	if opts.DataDir == "" {
		opts.DataDir = config.DefaultDataDir()
	}
	cfg := config.Default(opts.DataDir)
	cfg.APIAddr = net.JoinHostPort("0.0.0.0", fmt.Sprint(opts.APIPort))
	cfg.ControlAddr = net.JoinHostPort("0.0.0.0", fmt.Sprint(opts.ControlPort))
	cfg.PublicHost = opts.PublicHost
	cfg.Domain.Binding = opts.DomainBinding
	cfg.Domain.Suffix = opts.DomainSuffix
	cfg.TLS.CertFile = opts.TLSCert
	cfg.TLS.KeyFile = opts.TLSKey
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	svc, err := New(cfg, nil)
	if err != nil {
		return nil, nil, err
	}
	if opts.AdminUser != "" && opts.AdminPass != "" {
		ctxb := context.Background()
		if n, _ := svc.Store.CountAdmins(ctxb); n == 0 {
			if _, err := svc.Auth.CreateUser(ctxb, opts.AdminUser, "Administrator", opts.AdminPass, "admin"); err != nil {
				_ = svc.Shutdown(context.Background())
				return nil, nil, err
			}
		}
	}
	if err := svc.Start(); err != nil {
		_ = svc.Shutdown(context.Background())
		return nil, nil, err
	}
	ready := make(chan error, 1)
	go func() {
		// Probe our own health endpoint until it answers.
		url := fmt.Sprintf("https://127.0.0.1:%d/api/v1/health", opts.APIPort)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if healthy(url) {
				ready <- nil
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		select {
		case ready <- fmt.Errorf("健康检查超时：%s 无响应", url):
		default:
		}
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = svc.Shutdown(shutCtx)
	}()
	return ready, svc, nil
}

func healthy(url string) bool {
	client := insecureHTTPClient(3 * time.Second)
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}
