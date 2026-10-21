package servercli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/config"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
)

// LocalBackend reads and writes the SQLite database directly. It is what
// `yuki-server init` uses before any HTTP listener exists, and what
// `yuki-server user create` uses on an air-gapped host.
type LocalBackend struct {
	cfg *config.Config
	st  *store.Store
}

// NewLocalBackend opens the database described by cfg.
func NewLocalBackend(cfg *config.Config) (*LocalBackend, error) {
	if _, err := os.Stat(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("数据目录不存在：%s（请先运行 yuki-server init --data-dir %s）", cfg.DataDir, cfg.DataDir)
	}
	st, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败（%s）：%w", cfg.DatabaseURL, err)
	}
	return &LocalBackend{cfg: cfg, st: st}, nil
}

// Store exposes the underlying store (used by init).
func (l *LocalBackend) Store() *store.Store { return l.st }

func (l *LocalBackend) Mode() string { return "local" }

func (l *LocalBackend) Close() error { return l.st.Close() }

func (l *LocalBackend) Health(ctx context.Context) (map[string]any, error) {
	if err := l.st.DB().PingContext(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"status": "ok", "mode": "local", "database": "ok"}, nil
}

func (l *LocalBackend) Settings(ctx context.Context) (*Settings, error) {
	lo, _ := l.st.GetSettingInt(ctx, "port_min", l.cfg.Ports.Min)
	hi, _ := l.st.GetSettingInt(ctx, "port_max", l.cfg.Ports.Max)
	binding, _ := l.st.GetSettingBool(ctx, "domain_binding", l.cfg.Domain.Binding)
	suffix, _ := l.st.GetSetting(ctx, "domain_suffix", l.cfg.Domain.Suffix)
	host, _ := l.st.GetSetting(ctx, "public_host", l.cfg.PublicHost)
	return &Settings{
		DomainBinding: binding, DomainSuffix: suffix, PublicHost: host,
		PortMin: lo, PortMax: hi, APIAddr: l.cfg.APIAddr, ControlAddr: l.cfg.ControlAddr,
	}, nil
}

func (l *LocalBackend) UpdateSettings(ctx context.Context, p SettingsPatch) (*Settings, error) {
	if p.PortMin != nil || p.PortMax != nil {
		cur, _ := l.Settings(ctx)
		lo, hi := cur.PortMin, cur.PortMax
		if p.PortMin != nil {
			lo = *p.PortMin
		}
		if p.PortMax != nil {
			hi = *p.PortMax
		}
		if lo < 1 || hi > 65535 || lo > hi {
			return nil, fmt.Errorf("端口范围无效：%d-%d", lo, hi)
		}
		if err := l.st.SetSetting(ctx, "port_min", fmt.Sprint(lo)); err != nil {
			return nil, err
		}
		if err := l.st.SetSetting(ctx, "port_max", fmt.Sprint(hi)); err != nil {
			return nil, err
		}
	}
	if p.DomainBinding != nil {
		if err := l.st.SetSetting(ctx, "domain_binding", fmt.Sprint(*p.DomainBinding)); err != nil {
			return nil, err
		}
	}
	if p.DomainSuffix != nil {
		if err := l.st.SetSetting(ctx, "domain_suffix", *p.DomainSuffix); err != nil {
			return nil, err
		}
	}
	if p.PublicHost != nil {
		if err := l.st.SetSetting(ctx, "public_host", *p.PublicHost); err != nil {
			return nil, err
		}
	}
	return l.Settings(ctx)
}

func (l *LocalBackend) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := l.st.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(rows))
	for _, u := range rows {
		v := User{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role,
			Disabled: u.Disabled, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
		}
		if keys, err := l.st.ListUserKeys(ctx, u.ID); err == nil {
			v.KeyCount = len(keys)
		}
		if tuns, err := l.st.ListTunnels(ctx, u.ID); err == nil {
			v.TunnelCount = len(tuns)
		}
		out = append(out, v)
	}
	return out, nil
}

func (l *LocalBackend) CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) {
	if req.Password == "" && req.PublicKey == "" {
		return nil, errors.New("请设置初始密码或提供 RSA 公钥")
	}
	var hash string
	var err error
	if req.Password != "" {
		hash, err = auth.HashPassword(req.Password)
		if err != nil {
			return nil, err
		}
	}
	role := req.Role
	if role == "" {
		role = "user"
	}
	u := &store.User{
		ID: ids.Hex(12), Username: req.Username, DisplayName: req.DisplayName,
		PasswordHash: hash, Role: role,
	}
	if err := l.st.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("用户 %s 已存在", req.Username)
		}
		return nil, err
	}
	if req.PublicKey != "" {
		if _, err := l.addKey(ctx, u.ID, req.KeyName, req.PublicKey); err != nil {
			return nil, err
		}
	}
	return &User{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role}, nil
}

func (l *LocalBackend) UpdateUser(ctx context.Context, id string, req UpdateUserRequest) (*User, error) {
	u, err := l.st.GetUser(ctx, id)
	if err != nil {
		return nil, errors.New("用户不存在")
	}
	if req.DisplayName != nil {
		u.DisplayName = *req.DisplayName
	}
	if req.Role != nil {
		if *req.Role != "user" && *req.Role != "admin" {
			return nil, errors.New("角色必须是 user 或 admin")
		}
		u.Role = *req.Role
	}
	if req.Disabled != nil {
		u.Disabled = *req.Disabled
	}
	if req.Password != nil && *req.Password != "" {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = hash
	}
	if err := l.st.UpdateUser(ctx, u); err != nil {
		return nil, err
	}
	return &User{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role, Disabled: u.Disabled}, nil
}

func (l *LocalBackend) DeleteUser(ctx context.Context, id string) error {
	return l.st.DeleteUser(ctx, id)
}

func (l *LocalBackend) ListKeys(ctx context.Context, userID string) ([]UserKey, error) {
	keys, err := l.st.ListUserKeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]UserKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, UserKey{ID: k.ID, UserID: k.UserID, Name: k.Name, Fingerprint: k.Fingerprint, PublicKey: k.PublicKey, CreatedAt: k.CreatedAt})
	}
	return out, nil
}

func (l *LocalBackend) addKey(ctx context.Context, userID, name, publicKey string) (*UserKey, error) {
	canonical, fp, err := auth.NormalizePublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("公钥格式不正确：%w", err)
	}
	k := &store.UserKey{ID: ids.Hex(12), UserID: userID, Name: name, Fingerprint: fp, PublicKey: canonical}
	if err := l.st.CreateUserKey(ctx, k); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, errors.New("该公钥已登记")
		}
		return nil, err
	}
	return &UserKey{ID: k.ID, UserID: k.UserID, Name: k.Name, Fingerprint: k.Fingerprint, PublicKey: k.PublicKey, CreatedAt: k.CreatedAt}, nil
}

func (l *LocalBackend) AddKey(ctx context.Context, userID, name, publicKey string) (*UserKey, error) {
	return l.addKey(ctx, userID, name, publicKey)
}

func (l *LocalBackend) DeleteKey(ctx context.Context, userID, keyID string) error {
	return l.st.DeleteUserKey(ctx, userID, keyID)
}

func (l *LocalBackend) ListNodes(ctx context.Context) ([]Node, error) {
	nodes, err := l.st.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toNode(n))
	}
	return out, nil
}

func toNode(n store.Node) Node {
	return Node{
		ID: n.ID, Name: n.Name, Region: n.Region, PublicHost: n.PublicHost,
		ControlHost: n.ControlHost, ControlPort: n.ControlPort, Enabled: n.Enabled,
		Status: n.Status, LatencyMS: n.LatencyMS, MaxTunnels: n.MaxTunnels, LastSeenAt: n.LastSeenAt,
	}
}

func (l *LocalBackend) CreateNode(ctx context.Context, req NodeRequest) (*Node, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	port := req.ControlPort
	if port == 0 {
		port = 9443
	}
	host := req.ControlHost
	if host == "" {
		host = req.PublicHost
	}
	n := &store.Node{
		ID: ids.Hex(8), Name: req.Name, Region: req.Region, PublicHost: req.PublicHost,
		ControlHost: host, ControlPort: port, Enabled: enabled, MaxTunnels: req.MaxTunnels,
		Status: "unknown",
	}
	if err := l.st.CreateNode(ctx, n); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("节点 %s 已存在", req.Name)
		}
		return nil, err
	}
	v := toNode(*n)
	return &v, nil
}

func (l *LocalBackend) UpdateNode(ctx context.Context, id string, req NodeRequest) (*Node, error) {
	n, err := l.st.GetNode(ctx, id)
	if err != nil {
		return nil, errors.New("节点不存在")
	}
	if req.Name != "" {
		n.Name = req.Name
	}
	if req.Region != "" {
		n.Region = req.Region
	}
	if req.PublicHost != "" {
		n.PublicHost = req.PublicHost
	}
	if req.ControlHost != "" {
		n.ControlHost = req.ControlHost
	}
	if req.ControlPort > 0 {
		n.ControlPort = req.ControlPort
	}
	n.MaxTunnels = req.MaxTunnels
	if req.Enabled != nil {
		n.Enabled = *req.Enabled
	}
	if err := l.st.UpdateNode(ctx, n); err != nil {
		return nil, err
	}
	v := toNode(*n)
	return &v, nil
}

func (l *LocalBackend) DeleteNode(ctx context.Context, id string) error {
	err := l.st.DeleteNode(ctx, id)
	if errors.Is(err, store.ErrConflict) {
		return errors.New("该节点仍有隧道，请先删除隧道")
	}
	return err
}

func (l *LocalBackend) CheckNode(ctx context.Context, id string) (*Node, error) {
	n, err := l.st.GetNode(ctx, id)
	if err != nil {
		return nil, errors.New("节点不存在")
	}
	// A local backend cannot reach into the hub; report the stored state and
	// let `yuki-server doctor` do live probing.
	v := toNode(*n)
	return &v, nil
}

func (l *LocalBackend) ListTunnels(ctx context.Context, userID string) ([]Tunnel, error) {
	list, err := l.st.ListTunnels(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Tunnel, 0, len(list))
	for _, t := range list {
		v := Tunnel{
			ID: t.ID, UserID: t.UserID, NodeID: t.NodeID, Name: t.Name, Proto: t.Proto,
			LocalIP: t.LocalIP, LocalPort: t.LocalPort, PublicPort: t.PublicPort,
			Domain: t.Domain, Enabled: t.Enabled,
		}
		if n, err := l.st.GetNode(ctx, t.NodeID); err == nil {
			v.NodeName = n.Name
			host := n.PublicHost
			if t.Domain != "" {
				host = t.Domain
			}
			v.Address = fmt.Sprintf("%s:%d", host, t.PublicPort)
		}
		if u, err := l.st.GetUser(ctx, t.UserID); err == nil {
			v.UserName = u.Username
		}
		out = append(out, v)
	}
	return out, nil
}

func (l *LocalBackend) EnableTunnel(ctx context.Context, id string, enabled bool) (*Tunnel, error) {
	t, err := l.st.GetTunnel(ctx, id)
	if err != nil {
		return nil, errors.New("隧道不存在")
	}
	t.Enabled = enabled
	if err := l.st.UpdateTunnel(ctx, t); err != nil {
		return nil, err
	}
	return &Tunnel{ID: t.ID, Name: t.Name, Enabled: t.Enabled}, nil
}

func (l *LocalBackend) DeleteTunnel(ctx context.Context, id string) error {
	return l.st.DeleteTunnel(ctx, id)
}

func (l *LocalBackend) ListAudit(ctx context.Context, limit, offset int) ([]Audit, error) {
	list, err := l.st.ListAudit(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]Audit, 0, len(list))
	for _, a := range list {
		out = append(out, Audit{
			ID: a.ID, ActorName: a.ActorName, Action: a.Action, TargetType: a.TargetType,
			TargetID: a.TargetID, Detail: a.Detail, IP: a.IP, CreatedAt: a.CreatedAt,
		})
	}
	return out, nil
}

func (l *LocalBackend) PortUsage(ctx context.Context) (*PortUsage, error) {
	s, err := l.Settings(ctx)
	if err != nil {
		return nil, err
	}
	nodes, err := l.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := &PortUsage{}
	out.Range.Min, out.Range.Max = s.PortMin, s.PortMax
	for _, n := range nodes {
		ports, _ := l.st.UsedPorts(ctx, n.ID)
		list := make([]int, 0, len(ports))
		for p := range ports {
			list = append(list, p)
		}
		out.Nodes = append(out.Nodes, struct {
			NodeID string `json:"node_id"`
			Name   string `json:"name"`
			Used   []int  `json:"used"`
		}{NodeID: n.ID, Name: n.Name, Used: list})
	}
	return out, nil
}

// Audit logs a local CLI action.
func (l *LocalBackend) Audit(ctx context.Context, actor, action, targetType, targetID, detail string) {
	_ = l.st.AddAudit(ctx, &store.AuditEntry{
		ID: ids.Hex(12), ActorName: actor, Action: action, TargetType: targetType,
		TargetID: targetID, Detail: detail, IP: "cli", CreatedAt: time.Now().UTC(),
	})
}
