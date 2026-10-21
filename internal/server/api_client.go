package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/apierr"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
	"github.com/yuuuuukiu/yukitunnel/internal/tunnel"
)

// ServerView is the node listing returned to clients, including live latency.
type ServerView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Region      string `json:"region"`
	PublicHost  string `json:"public_host"`
	Status      string `json:"status"`
	Online      bool   `json:"online"`
	LatencyMS   int    `json:"latency_ms"`
	Domain      string `json:"domain,omitempty"`
	PortRange   [2]int `json:"port_range"`
	MaxTunnels  int    `json:"max_tunnels"`
	TunnelCount int    `json:"tunnel_count"`
}

func (h *httpServer) handleListServers(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	nodes, err := h.svc.Store.ListNodes(ctx)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取节点失败"))
		return
	}
	binding := h.svc.DomainBinding(ctx)
	lo, hi := h.svc.PortRange(ctx)
	out := make([]ServerView, 0, len(nodes))
	for _, n := range nodes {
		if !n.Enabled {
			continue
		}
		latency := n.LatencyMS
		if latency == 0 {
			latency = h.svc.probeNode(ctx, &n)
		}
		count := 0
		if list, err := h.svc.Store.ListTunnelsByNode(ctx, n.ID); err == nil {
			count = len(list)
		}
		v := ServerView{
			ID: n.ID, Name: n.Name, Region: n.Region, PublicHost: n.PublicHost,
			Status: n.Status, Online: n.Status == "online", LatencyMS: latency,
			PortRange: [2]int{lo, hi}, MaxTunnels: n.MaxTunnels, TunnelCount: count,
		}
		if binding {
			v.Domain = h.svc.DomainSuffix(ctx)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"servers":        out,
		"domain_binding": binding,
		"domain_suffix":  h.svc.DomainSuffix(ctx),
	})
}

// probeNode measures TCP connect latency to the node control endpoint.
func (s *Service) probeNode(ctx context.Context, n *store.Node) int {
	host := n.ControlHost
	if host == "" {
		host = n.PublicHost
	}
	port := n.ControlPort
	if port == 0 {
		_, ps, _ := net.SplitHostPort(s.Cfg.ControlAddr)
		port, _ = strconv.Atoi(ps)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		_ = s.Store.SetNodeHealth(context.Background(), n.ID, "offline", 0)
		return 0
	}
	_ = c.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms < 1 {
		ms = 1
	}
	_ = s.Store.SetNodeHealth(context.Background(), n.ID, "online", ms)
	return ms
}

// TunnelView is the API representation of a tunnel with its public address.
type TunnelView struct {
	store.Tunnel
	NodeName      string `json:"node_name"`
	Address       string `json:"address"`
	Active        bool   `json:"active"`
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
	Connections   int64  `json:"connections"`
	ActiveConns   int    `json:"active_connections"`
	PublicHost    string `json:"public_host"`
	LastStartedAt string `json:"last_started_at"`
}

func (h *httpServer) viewTunnel(ctx context.Context, t *store.Tunnel) TunnelView {
	node, _ := h.svc.Store.GetNode(ctx, t.NodeID)
	v := TunnelView{
		Tunnel: *t, Address: h.svc.PublicAddress(ctx, t, node), Active: h.svc.Hub.IsActive(t.ID),
		LastStartedAt: t.LastStartedAt.Format(time.RFC3339),
	}
	if node != nil {
		v.NodeName = node.Name
		v.PublicHost = node.PublicHost
	}
	if t.Domain != "" {
		v.PublicHost = t.Domain
	}
	if st := h.svc.TunnelStats(t.ID); st.Conns > 0 || st.RxBytes > 0 || st.TxBytes > 0 {
		v.RxBytes, v.TxBytes = st.RxBytes, st.TxBytes
		v.Connections, v.ActiveConns = st.Conns, st.Active
	}
	return v
}

func (h *httpServer) handleListMyTunnels(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := h.svc.Store.ListTunnels(r.Context(), u.ID)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取隧道失败"))
		return
	}
	out := make([]TunnelView, 0, len(list))
	for i := range list {
		out = append(out, h.viewTunnel(r.Context(), &list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnels": out})
}

func (h *httpServer) handleGetTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	t, err := h.svc.Store.GetTunnel(r.Context(), r.PathValue("id"))
	if err != nil || (t.UserID != u.ID && !u.IsAdmin()) {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnel": h.viewTunnel(r.Context(), t)})
}

type createTunnelRequest struct {
	Name           string `json:"name"`
	NodeID         string `json:"node_id"`
	Proto          string `json:"proto"`
	LocalIP        string `json:"local_ip"`
	LocalPort      int    `json:"local_port"`
	PublicPort     int    `json:"public_port"`
	UseDomain      *bool  `json:"use_domain"`
	BandwidthKbps  int    `json:"bandwidth_kbps"`
	MaxConnections int    `json:"max_connections"`
}

func normalizeProto(p string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "tcp":
		return "tcp", nil
	case "udp":
		return "udp", nil
	case "http":
		return "http", nil
	case "https", "tls":
		return "https", nil
	}
	return "", apierr.BadRequest("INVALID_PROTOCOL", "协议必须是 TCP、UDP、HTTP 或 HTTPS")
}

func (h *httpServer) handleCreateTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req createTunnelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	proto, err := normalizeProto(req.Proto)
	if err != nil {
		writeError(w, apierr.As(err))
		return
	}
	if req.NodeID == "" {
		writeError(w, apierr.BadRequest("NODE_REQUIRED", "请选择穿透节点"))
		return
	}
	node, err := h.svc.Store.GetNode(ctx, req.NodeID)
	if err != nil {
		writeError(w, apierr.NotFound("NODE_NOT_FOUND", "所选节点不存在"))
		return
	}
	if !node.Enabled {
		writeError(w, apierr.Conflict("NODE_OFFLINE", "节点已停用，请选择其他节点"))
		return
	}
	if node.MaxTunnels > 0 {
		if list, err := h.svc.Store.ListTunnelsByNode(ctx, node.ID); err == nil && len(list) >= node.MaxTunnels {
			writeError(w, apierr.Conflict("NODE_FULL", "该节点隧道数量已达上限，请选择其他节点"))
			return
		}
	}
	ip := strings.TrimSpace(req.LocalIP)
	if ip == "" {
		ip = "127.0.0.1"
	}
	if net.ParseIP(ip) == nil && !isHostname(ip) {
		writeError(w, apierr.BadRequest("INVALID_LOCAL_IP", "内网地址格式不正确"))
		return
	}
	if req.LocalPort < 1 || req.LocalPort > 65535 {
		writeError(w, apierr.BadRequest("INVALID_LOCAL_PORT", "内网端口必须在 1-65535 之间"))
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fmt.Sprintf("%s-%d", proto, req.LocalPort)
	}

	// Decide public port / domain according to the admin switch.
	binding := h.svc.DomainBinding(ctx)
	suffix := h.svc.DomainSuffix(ctx)
	useDomain := binding
	if req.UseDomain != nil {
		if *req.UseDomain && !binding {
			writeError(w, apierr.Conflict("DOMAIN_DISABLED", "管理员未开启域名绑定，请联系管理员"))
			return
		}
		useDomain = *req.UseDomain
	}
	if useDomain && suffix == "" {
		writeError(w, apierr.Conflict("DOMAIN_NOT_CONFIGURED", "管理员尚未配置域名后缀"))
		return
	}
	publicPort := req.PublicPort
	if publicPort == 0 {
		p, err := h.svc.allocatePort(ctx, node.ID, proto)
		if err != nil {
			writeError(w, apierr.As(err))
			return
		}
		publicPort = p
	} else {
		lo, hi := h.svc.PortRange(ctx)
		if publicPort < lo || publicPort > hi {
			writeError(w, apierr.BadRequest("PORT_OUT_OF_RANGE",
				fmt.Sprintf("公网端口必须在 %d-%d 范围内", lo, hi)))
			return
		}
		if busy, _ := h.svc.Store.PortInUse(ctx, node.ID, publicPort, ""); busy {
			writeError(w, apierr.Conflict("PORT_IN_USE", fmt.Sprintf("公网端口 %d 已被占用，请更换", publicPort)))
			return
		}
		if !h.svc.Cfg.PortInRange(publicPort) {
			writeError(w, apierr.BadRequest("PORT_NOT_ALLOWED", "该端口不在管理员配置的允许列表中"))
			return
		}
	}
	domain := ""
	if useDomain {
		domain = h.svc.tunnelDomain(ctx, name, suffix)
	}

	t := &store.Tunnel{
		ID: h.svc.newTunnelID(), UserID: u.ID, NodeID: node.ID, Name: name, Proto: proto,
		LocalIP: ip, LocalPort: req.LocalPort, PublicPort: publicPort, Domain: domain,
		Enabled: true, BandwidthKbps: req.BandwidthKbps, MaxConnections: req.MaxConnections,
	}
	if t.BandwidthKbps == 0 {
		t.BandwidthKbps = h.svc.Cfg.Limits.DefaultBandwidthKbps
	}
	if t.MaxConnections == 0 {
		t.MaxConnections = h.svc.Cfg.Limits.DefaultMaxConnections
	}
	if err := h.svc.Store.CreateTunnel(ctx, t); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, apierr.Conflict("PORT_IN_USE", "公网端口已被占用，请更换"))
			return
		}
		writeError(w, apierr.Wrapf(err, "创建隧道失败"))
		return
	}
	_ = h.svc.audit(ctx, u, "tunnel.created", "tunnel", t.ID, fmt.Sprintf("%s %s:%d", proto, ip, req.LocalPort), clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"tunnel": h.viewTunnel(ctx, t)})
}

func isHostname(s string) bool {
	if len(s) > 253 || s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" || len(part) > 63 {
			return false
		}
		for i, c := range part {
			ok := c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
			if !ok || (i == 0 && c == '-') {
				return false
			}
		}
	}
	return true
}

type updateTunnelRequest struct {
	Name           *string `json:"name"`
	NodeID         *string `json:"node_id"`
	Proto          *string `json:"proto"`
	LocalIP        *string `json:"local_ip"`
	LocalPort      *int    `json:"local_port"`
	PublicPort     *int    `json:"public_port"`
	BandwidthKbps  *int    `json:"bandwidth_kbps"`
	MaxConnections *int    `json:"max_connections"`
	Enabled        *bool   `json:"enabled"`
	DesiredRunning *bool   `json:"desired_running"`
}

func (h *httpServer) handleUpdateTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	t, err := h.svc.Store.GetTunnel(ctx, r.PathValue("id"))
	if err != nil || (t.UserID != u.ID && !u.IsAdmin()) {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	var req updateTunnelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		t.Name = strings.TrimSpace(*req.Name)
	}
	if req.Proto != nil {
		p, err := normalizeProto(*req.Proto)
		if err != nil {
			writeError(w, apierr.As(err))
			return
		}
		t.Proto = p
	}
	if req.LocalIP != nil {
		if net.ParseIP(*req.LocalIP) == nil && !isHostname(*req.LocalIP) {
			writeError(w, apierr.BadRequest("INVALID_LOCAL_IP", "内网地址格式不正确"))
			return
		}
		t.LocalIP = *req.LocalIP
	}
	if req.LocalPort != nil {
		if *req.LocalPort < 1 || *req.LocalPort > 65535 {
			writeError(w, apierr.BadRequest("INVALID_LOCAL_PORT", "内网端口必须在 1-65535 之间"))
			return
		}
		t.LocalPort = *req.LocalPort
	}
	if req.NodeID != nil && *req.NodeID != t.NodeID {
		node, err := h.svc.Store.GetNode(ctx, *req.NodeID)
		if err != nil || !node.Enabled {
			writeError(w, apierr.BadRequest("NODE_NOT_FOUND", "所选节点不存在或已停用"))
			return
		}
		t.NodeID = node.ID
	}
	if req.PublicPort != nil && *req.PublicPort != t.PublicPort {
		lo, hi := h.svc.PortRange(ctx)
		if *req.PublicPort < lo || *req.PublicPort > hi {
			writeError(w, apierr.BadRequest("PORT_OUT_OF_RANGE", fmt.Sprintf("公网端口必须在 %d-%d 范围内", lo, hi)))
			return
		}
		if busy, _ := h.svc.Store.PortInUse(ctx, t.NodeID, *req.PublicPort, t.ID); busy {
			writeError(w, apierr.Conflict("PORT_IN_USE", "公网端口已被占用"))
			return
		}
		t.PublicPort = *req.PublicPort
	}
	if req.BandwidthKbps != nil {
		t.BandwidthKbps = *req.BandwidthKbps
	}
	if req.MaxConnections != nil {
		t.MaxConnections = *req.MaxConnections
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.DesiredRunning != nil {
		t.DesiredRunning = *req.DesiredRunning
	}
	if err := h.svc.Store.UpdateTunnel(ctx, t); err != nil {
		writeError(w, apierr.Wrapf(err, "更新隧道失败"))
		return
	}
	// Restart the listener if it is live so changes apply immediately.
	if t.DesiredRunning && t.Enabled {
		if err := h.svc.restartTunnel(ctx, t); err != nil && !errors.Is(err, tunnel.ErrClientOffline) {
			writeError(w, apierr.As(err))
			return
		}
	} else if h.svc.Hub.IsActive(t.ID) {
		h.svc.Hub.StopTunnel(t.ID)
	}
	_ = h.svc.audit(ctx, u, "tunnel.updated", "tunnel", t.ID, "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"tunnel": h.viewTunnel(ctx, t)})
}

func (h *httpServer) handleDeleteTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	t, err := h.svc.Store.GetTunnel(ctx, r.PathValue("id"))
	if err != nil || (t.UserID != u.ID && !u.IsAdmin()) {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	h.svc.Hub.StopTunnel(t.ID)
	if err := h.svc.Store.DeleteTunnel(ctx, t.ID); err != nil {
		writeError(w, apierr.Wrapf(err, "删除隧道失败"))
		return
	}
	_ = h.svc.audit(ctx, u, "tunnel.deleted", "tunnel", t.ID, t.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *httpServer) handleStartTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	t, err := h.svc.Store.GetTunnel(ctx, r.PathValue("id"))
	if err != nil || (t.UserID != u.ID && !u.IsAdmin()) {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	if !t.Enabled {
		writeError(w, apierr.Forbidden("TUNNEL_DISABLED", "该隧道已被管理员禁用"))
		return
	}
	node, err := h.svc.Store.GetNode(ctx, t.NodeID)
	if err != nil || !node.Enabled {
		writeError(w, apierr.Conflict("NODE_OFFLINE", "节点离线，无法启动隧道"))
		return
	}
	spec, err := h.svc.SpecFor(ctx, t)
	if err != nil {
		writeError(w, apierr.As(err))
		return
	}
	// Open the host firewall before the listener so the port is reachable the
	// moment the tunnel comes up. A failure here is logged, not fatal: many
	// operators manage the firewall themselves.
	fwRes := h.svc.AllowTunnelPort(ctx, spec.PublicPort, spec.Proto)
	if fwRes.Applied {
		_ = h.svc.audit(ctx, u, "firewall.allow", "tunnel", t.ID,
			fmt.Sprintf("%d/%s via %s", spec.PublicPort, spec.Proto, fwRes.Backend), clientIP(r))
	}

	// The listener lives on the server; the agent only forwards streams. When
	// no agent is connected the tunnel stays pending and starts on reconnect.
	if _, err := h.svc.Hub.StartTunnelFor(t.UserID, *spec); err != nil && !errors.Is(err, tunnel.ErrClientOffline) {
		writeError(w, apierr.Conflict("START_FAILED", "启动失败："+err.Error()))
		return
	}
	_ = h.svc.Store.SetDesiredRunning(ctx, t.ID, true)
	_ = h.svc.audit(ctx, u, "tunnel.started", "tunnel", t.ID, spec.Address(), clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"tunnel": h.viewTunnel(ctx, t), "address": spec.Address()})
}

func (h *httpServer) handleStopTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	t, err := h.svc.Store.GetTunnel(ctx, r.PathValue("id"))
	if err != nil || (t.UserID != u.ID && !u.IsAdmin()) {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	h.svc.Hub.StopTunnel(t.ID)
	_ = h.svc.Store.SetDesiredRunning(ctx, t.ID, false)
	if fwRes := h.svc.RevokeTunnelPort(ctx, t.PublicPort, t.Proto); fwRes.Applied {
		_ = h.svc.audit(ctx, u, "firewall.revoke", "tunnel", t.ID,
			fmt.Sprintf("%d/%s via %s", t.PublicPort, t.Proto, fwRes.Backend), clientIP(r))
	}
	_ = h.svc.audit(ctx, u, "tunnel.stopped", "tunnel", t.ID, "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"tunnel": h.viewTunnel(ctx, t)})
}

func (h *httpServer) handleTunnelStats(w http.ResponseWriter, r *http.Request, u *store.User) {
	t, err := h.svc.Store.GetTunnel(r.Context(), r.PathValue("id"))
	if err != nil || (t.UserID != u.ID && !u.IsAdmin()) {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	v := h.viewTunnel(r.Context(), t)
	writeJSON(w, http.StatusOK, map[string]any{
		"active": v.Active, "rx_bytes": v.RxBytes, "tx_bytes": v.TxBytes,
		"connections": v.Connections, "active_connections": v.ActiveConns,
	})
}

// restartTunnel re-registers a live tunnel after an edit.
func (s *Service) restartTunnel(ctx context.Context, t *store.Tunnel) error {
	spec, err := s.SpecFor(ctx, t)
	if err != nil {
		return err
	}
	_ = s.Hub.PushStop(t.UserID, []string{t.ID})
	return s.Hub.PushRegister(t.UserID, []tunnel.TunnelSpec{*spec})
}

func (s *Service) newTunnelID() string {
	for i := 0; i < 5; i++ {
		id := "tun_" + randomSuffix(8)
		if _, err := s.Store.GetTunnel(context.Background(), id); err != nil {
			return id
		}
	}
	return "tun_" + randomSuffix(12)
}

// tunnelDomain builds "<slug>-<rand>.<suffix>".
func (s *Service) tunnelDomain(ctx context.Context, name, suffix string) string {
	slug := sanitizeLabel(name)
	if slug == "" {
		slug = "tunnel"
	}
	if len(slug) > 24 {
		slug = slug[:24]
	}
	return slug + "-" + randomSuffix(6) + "." + suffix
}

func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '-' || c == '_' || c == '.' || c == ' ':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
