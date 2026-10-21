// Package server wires the store, auth, tunnel hub and HTTP API together.
package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/apierr"
	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/config"
	"github.com/yuuuuukiu/yukitunnel/internal/firewall"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
	"github.com/yuuuuukiu/yukitunnel/internal/tlsutil"
	"github.com/yuuuuukiu/yukitunnel/internal/tunnel"
	"github.com/yuuuuukiu/yukitunnel/internal/version"
)

// Service is the embeddable server core. It is used by cmd/yuki-server and by
// the desktop app's embedded server mode.
type Service struct {
	Cfg      *config.Config
	Store    *store.Store
	Auth     *auth.Service
	Hub      *tunnel.Hub
	Firewall *firewall.Service
	Log      *slog.Logger

	httpSrv   *httpServer
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	chMu       sync.Mutex
	challenges map[string]challenge

	statsMu sync.RWMutex
	stats   map[string]tunnel.TunnelStats
}

// New opens the database and prepares (but does not start) the service.
func New(cfg *config.Config, log *slog.Logger) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	// A release build must never ship the development signing key: every
	// profile download would fail signature verification at runtime.
	if err := CheckReleaseReadiness(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	st, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		Cfg:    cfg,
		Store:  st,
		Auth:   auth.New(st, cfg.TokenTTL(), cfg.Auth.MaxFailedLogins, time.Duration(cfg.Auth.LockoutMinutes)*time.Minute),
		Log:    log,
		ctx:    ctx,
		cancel: cancel,
	}
	s.Hub = tunnel.NewHub(ctx, s.hooks(), log)
	s.Firewall = firewall.NewService(log, os.Getenv("YUKI_FIREWALL") == "off")
	s.Firewall.OnEvent = func(e firewall.Event) {
		if e.Skipped && e.Reason != "" {
			// Skips are expected on most hosts; only record failures and
			// applied changes to keep the audit log readable.
			return
		}
		detail := fmt.Sprintf("backend=%s port=%d proto=%s applied=%v", e.Backend, e.Port, e.Proto, e.Applied)
		if e.Error != "" {
			detail += " error=" + e.Error
		}
		_ = s.audit(context.Background(), nil, "firewall."+e.Action, "port",
			strconv.Itoa(e.Port), detail, "system")
	}
	s.httpSrv = newHTTPServer(s)
	return s, nil
}

// Start begins listening on the API/control and WebUI ports.
func (s *Service) Start() error {
	if err := s.EnsureBootstrap(); err != nil {
		return err
	}
	if err := s.httpSrv.start(); err != nil {
		return err
	}
	go s.reconcileFirewall()
	return nil
}

// reconcileFirewall opens ports for tunnels that are marked as running and
// removes rules for tunnels that no longer exist. It runs once at startup and
// is safe to call again.
func (s *Service) reconcileFirewall() {
	if s.Firewall == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tunnels, err := s.Store.ListTunnels(ctx, "")
	if err != nil {
		s.Log.Warn("firewall reconcile: list tunnels failed", "err", err)
		return
	}
	var desired []firewall.Rule
	for i := range tunnels {
		t := tunnels[i]
		if !t.Enabled || !t.DesiredRunning || t.PublicPort == 0 {
			continue
		}
		desired = append(desired, firewall.Rule{Port: t.PublicPort, Protocol: t.Proto})
	}
	res, err := s.Firewall.Reconcile(ctx, desired)
	if err != nil {
		s.Log.Warn("firewall reconcile failed", "err", err)
		return
	}
	if res.Skipped {
		s.Log.Debug("firewall reconcile skipped", "reason", res.Reason)
		return
	}
	s.Log.Info("firewall reconciled", "kept", len(res.Kept), "removed", len(res.Removed), "backend", res.Backend)
	if len(res.Removed) > 0 {
		_ = s.audit(ctx, nil, "firewall.reconciled", "firewall",
			string(res.Backend), fmt.Sprintf("removed=%v", res.Removed), "system")
	}
}

// AllowTunnelPort opens the public port of a tunnel in the host firewall.
// Failures are logged but never block the tunnel: a firewall the operator
// manages manually is a valid setup.
func (s *Service) AllowTunnelPort(ctx context.Context, port int, proto string) firewall.Result {
	if s.Firewall == nil || port <= 0 {
		return firewall.Result{Skipped: true, Reason: "防火墙适配未启用"}
	}
	res, err := s.Firewall.AllowPort(ctx, port, proto)
	if err != nil {
		s.Log.Warn("firewall allow failed", "port", port, "proto", proto, "err", err)
	}
	return res
}

// RevokeTunnelPort closes the public port of a tunnel.
func (s *Service) RevokeTunnelPort(ctx context.Context, port int, proto string) firewall.Result {
	if s.Firewall == nil || port <= 0 {
		return firewall.Result{Skipped: true, Reason: "防火墙适配未启用"}
	}
	res, err := s.Firewall.RevokePort(ctx, port, proto)
	if err != nil {
		s.Log.Warn("firewall revoke failed", "port", port, "proto", proto, "err", err)
	}
	return res
}

// Shutdown stops listeners, sessions and the database.
func (s *Service) Shutdown(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.cancel()
		if s.httpSrv != nil {
			err = s.httpSrv.shutdown(ctx)
		}
		s.Hub.Close()
		if s.Store != nil {
			_ = s.Store.Close()
		}
	})
	return err
}

// EnsureBootstrap creates the initial admin, node and settings when the
// database is empty. It is idempotent.
func (s *Service) EnsureBootstrap() error {
	ctx := context.Background()
	count, err := s.Store.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		password := os.Getenv("YUKI_ADMIN_PASSWORD")
		if password == "" {
			password = "admin12345"
			s.Log.Warn("YUKI_ADMIN_PASSWORD not set; using the default password, change it immediately")
		}
		username := envOr("YUKI_ADMIN_USERNAME", "admin")
		if _, err := s.Auth.CreateUser(ctx, username, "Administrator", password, "admin"); err != nil {
			return fmt.Errorf("bootstrap admin: %w", err)
		}
		s.Log.Info("bootstrap admin created", "username", username)
		_ = s.audit(ctx, nil, "admin.bootstrap", "user", username, "initial administrator", "")
	}
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		host := s.Cfg.PublicHost
		if host == "" {
			host = DetectPublicHost()
		}
		_, portStr, _ := net.SplitHostPort(s.Cfg.ControlAddr)
		port, _ := strconv.Atoi(portStr)
		n := &store.Node{
			ID:          ids.Hex(8),
			Name:        "default",
			Region:      "local",
			PublicHost:  host,
			ControlHost: "127.0.0.1",
			ControlPort: port,
			Enabled:     true,
			Status:      "online",
		}
		if err := s.Store.CreateNode(ctx, n); err != nil && !errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("bootstrap node: %w", err)
		}
		s.Log.Info("bootstrap node created", "public_host", host)
	}
	if err := s.seedSettings(ctx); err != nil {
		return err
	}
	return nil
}

// recordStats keeps the latest traffic counters in memory. Writing them to
// SQLite every few seconds per tunnel would cause needless write amplification.
func (s *Service) recordStats(id string, st tunnel.TunnelStats) {
	s.statsMu.Lock()
	if s.stats == nil {
		s.stats = map[string]tunnel.TunnelStats{}
	}
	s.stats[id] = st
	s.statsMu.Unlock()
}

// TunnelStats returns the last counters for a tunnel.
func (s *Service) TunnelStats(id string) tunnel.TunnelStats {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	return s.stats[id]
}

func (s *Service) seedSettings(ctx context.Context) error {
	defaults := map[string]string{
		"domain_binding": strconv.FormatBool(s.Cfg.Domain.Binding),
		"domain_suffix":  s.Cfg.Domain.Suffix,
		"port_min":       strconv.Itoa(s.Cfg.Ports.Min),
		"port_max":       strconv.Itoa(s.Cfg.Ports.Max),
		"public_host":    s.Cfg.PublicHost,
	}
	for k, v := range defaults {
		cur, err := s.Store.GetSetting(ctx, k, "")
		if err != nil {
			return err
		}
		if cur == "" {
			if err := s.Store.SetSetting(ctx, k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// DetectPublicHost guesses the machine's primary outbound IP.
func DetectPublicHost() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
		return addr.IP.String()
	}
	return "127.0.0.1"
}

// DomainBinding reports the runtime domain switch.
func (s *Service) DomainBinding(ctx context.Context) bool {
	v, err := s.Store.GetSettingBool(ctx, "domain_binding", s.Cfg.Domain.Binding)
	if err != nil {
		return s.Cfg.Domain.Binding
	}
	return v
}

// DomainSuffix returns the runtime domain suffix.
func (s *Service) DomainSuffix(ctx context.Context) string {
	v, err := s.Store.GetSetting(ctx, "domain_suffix", s.Cfg.Domain.Suffix)
	if err != nil {
		return s.Cfg.Domain.Suffix
	}
	return strings.Trim(strings.ToLower(strings.TrimSpace(v)), ".")
}

// PublicHost returns the configured public host override (may be empty).
func (s *Service) PublicHost(ctx context.Context) string {
	v, err := s.Store.GetSetting(ctx, "public_host", s.Cfg.PublicHost)
	if err != nil {
		return s.Cfg.PublicHost
	}
	return strings.TrimSpace(v)
}

// PortRange returns the runtime allocatable port range.
func (s *Service) PortRange(ctx context.Context) (int, int) {
	lo, err := s.Store.GetSettingInt(ctx, "port_min", s.Cfg.Ports.Min)
	if err != nil {
		lo = s.Cfg.Ports.Min
	}
	hi, err := s.Store.GetSettingInt(ctx, "port_max", s.Cfg.Ports.Max)
	if err != nil {
		hi = s.Cfg.Ports.Max
	}
	return lo, hi
}

// PublicAddress computes the address shown to users for a tunnel.
func (s *Service) PublicAddress(ctx context.Context, t *store.Tunnel, node *store.Node) string {
	host := ""
	if t.Domain != "" {
		host = t.Domain
	} else if node != nil {
		host = node.PublicHost
	}
	if host == "" {
		host = s.PublicHost(ctx)
	}
	if host == "" {
		host = DetectPublicHost()
	}
	return net.JoinHostPort(host, strconv.Itoa(t.PublicPort))
}

// SpecFor builds the data-plane spec for a tunnel.
func (s *Service) SpecFor(ctx context.Context, t *store.Tunnel) (*tunnel.TunnelSpec, error) {
	node, err := s.Store.GetNode(ctx, t.NodeID)
	if err != nil {
		return nil, apierr.NotFound("NODE_NOT_FOUND", "隧道所属节点不存在")
	}
	host := node.PublicHost
	if t.Domain != "" {
		host = t.Domain
	} else if override := s.PublicHost(ctx); override != "" {
		host = override
	}
	return &tunnel.TunnelSpec{
		ID: t.ID, UserID: t.UserID, NodeID: t.NodeID, Name: t.Name, Proto: t.Proto,
		LocalIP: t.LocalIP, LocalPort: t.LocalPort, PublicPort: t.PublicPort, Domain: t.Domain,
		BandwidthKbps: t.BandwidthKbps, MaxConnections: t.MaxConnections,
		PublicHost: host, BindAddr: "0.0.0.0",
	}, nil
}

// RestoreUserTunnels re-opens listeners for tunnels the user asked to keep
// running, called whenever an agent (re)connects.
func (s *Service) RestoreUserTunnels(ctx context.Context, userID string) {
	list, err := s.Store.ListDesiredRunning(ctx, userID)
	if err != nil {
		return
	}
	for i := range list {
		spec, err := s.SpecFor(ctx, &list[i])
		if err != nil {
			continue
		}
		if _, err := s.Hub.StartTunnelFor(userID, *spec); err != nil {
			continue
		}
	}
}

// hooks connects the hub to service state.
func (s *Service) hooks() tunnel.Hooks {
	return tunnel.Hooks{
		Authenticate: func(ctx context.Context, token string) (*tunnel.Identity, error) {
			u, _, err := s.Auth.AuthenticateToken(ctx, token)
			if err != nil {
				return nil, err
			}
			return &tunnel.Identity{UserID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin()}, nil
		},
		LookupTunnel: func(ctx context.Context, userID, tunnelID string) (*tunnel.TunnelSpec, error) {
			t, err := s.Store.GetTunnel(ctx, tunnelID)
			if err != nil {
				return nil, tunnel.ErrUnknownTunnel
			}
			if t.UserID != userID {
				return nil, tunnel.ErrUnknownTunnel
			}
			if !t.Enabled {
				return nil, errors.New("隧道已被管理员禁用")
			}
			return s.SpecFor(ctx, t)
		},
		ListRunnable: func(ctx context.Context, userID string) ([]tunnel.TunnelSpec, error) {
			list, err := s.Store.ListTunnels(ctx, userID)
			if err != nil {
				return nil, err
			}
			out := make([]tunnel.TunnelSpec, 0, len(list))
			for i := range list {
				if !list[i].Enabled {
					continue
				}
				sp, err := s.SpecFor(ctx, &list[i])
				if err != nil {
					continue
				}
				out = append(out, *sp)
			}
			return out, nil
		},
		AllocatePort: func(ctx context.Context, spec *tunnel.TunnelSpec) (int, error) {
			return s.allocatePort(ctx, spec.NodeID, spec.Proto)
		},
		SavePort: func(ctx context.Context, tunnelID string, port int) error {
			t, err := s.Store.GetTunnel(ctx, tunnelID)
			if err != nil {
				return err
			}
			t.PublicPort = port
			return s.Store.UpdateTunnel(ctx, t)
		},
		Stats: func(tunnelID string, rx, tx, conns int64, active int) {
			s.recordStats(tunnelID, tunnel.TunnelStats{
				RxBytes: rx, TxBytes: tx, Conns: conns, Active: active,
			})
		},
		Log: func(level, msg string, kv map[string]any) {
			s.Log.Info(msg, "level", level, "detail", kv)
		},
	}
}

// allocatePort finds a free port inside the configured range that is not used
// by any tunnel on the node and is currently bindable.
func (s *Service) allocatePort(ctx context.Context, nodeID, proto string) (int, error) {
	lo, hi := s.PortRange(ctx)
	used, err := s.Store.UsedPorts(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	for p := lo; p <= hi; p++ {
		if used[p] {
			continue
		}
		if !s.Cfg.PortInRange(p) {
			continue
		}
		if portFree(proto, p) {
			return p, nil
		}
	}
	return 0, apierr.Conflict("NO_PORT_AVAILABLE",
		fmt.Sprintf("端口范围 %d-%d 内已没有可用端口，请联系管理员扩容", lo, hi))
}

func portFree(proto string, port int) bool {
	if proto == "udp" {
		addr, err := net.ResolveUDPAddr("udp", ":"+strconv.Itoa(port))
		if err != nil {
			return false
		}
		c, err := net.ListenUDP("udp", addr)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// randomSuffix returns n lowercase base36-ish characters.
func randomSuffix(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[randInt(len(alphabet))]
	}
	return string(b)
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

// audit writes an audit record; failures are logged but never returned.
func (s *Service) audit(ctx context.Context, actor *store.User, action, targetType, targetID, detail, ip string) error {
	a := &store.AuditEntry{
		ID: ids.Hex(12), Action: action, TargetType: targetType, TargetID: targetID,
		Detail: detail, IP: ip,
	}
	if actor != nil {
		a.ActorID = actor.ID
		a.ActorName = actor.Username
	}
	if err := s.Store.AddAudit(ctx, a); err != nil {
		s.Log.Warn("audit write failed", "err", err, "action", action)
		return err
	}
	return nil
}

// EnsureTLS returns TLS file paths, generating a self-signed pair when needed.
func (s *Service) EnsureTLS() (certFile, keyFile string, err error) {
	if s.Cfg.TLS.CertFile != "" && s.Cfg.TLS.KeyFile != "" {
		return s.Cfg.TLS.CertFile, s.Cfg.TLS.KeyFile, nil
	}
	if !s.Cfg.TLS.AutoSelfSigned {
		return "", "", errors.New("未配置 TLS 证书，且自动生成已关闭")
	}
	hosts := []string{"localhost", s.Cfg.PublicHost, DetectPublicHost()}
	if s.Cfg.Domain.Suffix != "" {
		hosts = append(hosts, "*."+s.Cfg.Domain.Suffix)
	}
	f, err := tlsutil.EnsureSelfSigned(s.Cfg.DataDir, hosts)
	if err != nil {
		return "", "", err
	}
	return f.CertFile, f.KeyFile, nil
}

// CertificateFingerprint returns the SHA-256 fingerprint of the API cert.
func (s *Service) CertificateFingerprint() (string, error) {
	cert, _, err := s.EnsureTLS()
	if err != nil {
		return "", err
	}
	return tlsutil.FingerprintFile(cert)
}

// Version returns the running build version.
func (s *Service) Version() string { return version.Version }

// LogsDir returns the directory where the server writes its log file.
func (s *Service) LogsDir() string { return filepath.Join(s.Cfg.DataDir, "logs") }
