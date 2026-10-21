package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/yuuuuukiu/yukitunnel/internal/apierr"
	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
)

func (h *httpServer) handleOverview(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	users, _ := h.svc.Store.ListUsers(ctx)
	nodes, _ := h.svc.Store.ListNodes(ctx)
	tunnels, _ := h.svc.Store.ListTunnels(ctx, "")
	audit, _ := h.svc.Store.ListAudit(ctx, 10, 0)
	online, active := 0, 0
	for _, n := range nodes {
		if n.Status == "online" && n.Enabled {
			online++
		}
	}
	for _, t := range tunnels {
		if h.svc.Hub.IsActive(t.ID) {
			active++
		}
	}
	binding := h.svc.DomainBinding(ctx)
	lo, hi := h.svc.PortRange(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"counts": map[string]int{
			"users": len(users), "nodes": len(nodes), "nodes_online": online,
			"tunnels": len(tunnels), "tunnels_active": active,
		},
		"settings": map[string]any{
			"domain_binding": binding,
			"domain_suffix":  h.svc.DomainSuffix(ctx),
			"public_host":    h.svc.PublicHost(ctx),
			"port_min":       lo,
			"port_max":       hi,
		},
		"recent_audit": audit,
		"version":      h.svc.Version(),
	})
}

// ---- users ----

type adminUserView struct {
	store.User
	KeyCount    int `json:"key_count"`
	TunnelCount int `json:"tunnel_count"`
}

func (h *httpServer) handleAdminListUsers(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	list, err := h.svc.Store.ListUsers(ctx)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取用户失败"))
		return
	}
	out := make([]adminUserView, 0, len(list))
	for _, usr := range list {
		v := adminUserView{User: usr}
		if keys, err := h.svc.Store.ListUserKeys(ctx, usr.ID); err == nil {
			v.KeyCount = len(keys)
		}
		if tuns, err := h.svc.Store.ListTunnels(ctx, usr.ID); err == nil {
			v.TunnelCount = len(tuns)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

type adminCreateUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	PublicKey   string `json:"public_key"`
	KeyName     string `json:"key_name"`
}

func (h *httpServer) handleAdminCreateUser(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req adminCreateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Password == "" && req.PublicKey == "" {
		writeError(w, apierr.BadRequest("CREDENTIAL_REQUIRED", "请设置初始密码或登记 RSA 公钥"))
		return
	}
	u, err := h.svc.Auth.CreateUser(r.Context(), req.Username, req.DisplayName, req.Password, req.Role)
	if err != nil {
		writeError(w, apierr.As(err))
		return
	}
	if req.PublicKey != "" {
		if _, err := h.svc.addKey(r.Context(), u.ID, req.KeyName, req.PublicKey); err != nil {
			writeError(w, apierr.As(err))
			return
		}
	}
	_ = h.svc.audit(r.Context(), actor, "user.created", "user", u.ID, u.Username, clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

type adminUpdateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
	Role        *string `json:"role"`
	Disabled    *bool   `json:"disabled"`
}

func (h *httpServer) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request, actor *store.User) {
	ctx := r.Context()
	u, err := h.svc.Store.GetUser(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("USER_NOT_FOUND", "用户不存在"))
		return
	}
	var req adminUpdateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DisplayName != nil {
		u.DisplayName = *req.DisplayName
	}
	if req.Role != nil {
		if *req.Role != "user" && *req.Role != "admin" {
			writeError(w, apierr.BadRequest("INVALID_ROLE", "角色必须是 user 或 admin"))
			return
		}
		if u.ID == actor.ID && *req.Role != "admin" {
			writeError(w, apierr.BadRequest("SELF_DEMOTE", "不能取消自己的管理员权限"))
			return
		}
		u.Role = *req.Role
	}
	if req.Disabled != nil {
		if u.ID == actor.ID && *req.Disabled {
			writeError(w, apierr.BadRequest("SELF_DISABLE", "不能禁用当前登录的账号"))
			return
		}
		u.Disabled = *req.Disabled
	}
	if req.Password != nil && *req.Password != "" {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			writeError(w, apierr.BadRequest("WEAK_PASSWORD", err.Error()))
			return
		}
		u.PasswordHash = hash
	}
	// Guard the last remaining admin only when this update actually removes an
	// admin (demotion or disable). The previous condition also fired for plain
	// users, so disabling any regular account failed with LAST_ADMIN.
	wasAdmin := u.Role == "admin"
	stillActiveAdmin := u.Role == "admin" && !u.Disabled
	if wasAdmin && !stillActiveAdmin {
		if admins, err := h.svc.Store.CountAdmins(ctx); err == nil && admins <= 1 {
			writeError(w, apierr.BadRequest("LAST_ADMIN", "至少需要保留一个管理员账号"))
			return
		}
	}
	if err := h.svc.Store.UpdateUser(ctx, u); err != nil {
		writeError(w, apierr.Wrapf(err, "更新用户失败"))
		return
	}
	_ = h.svc.audit(ctx, actor, "user.updated", "user", u.ID, u.Username, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (h *httpServer) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request, actor *store.User) {
	ctx := r.Context()
	id := r.PathValue("id")
	if id == actor.ID {
		writeError(w, apierr.BadRequest("SELF_DELETE", "不能删除当前登录的账号"))
		return
	}
	u, err := h.svc.Store.GetUser(ctx, id)
	if err != nil {
		writeError(w, apierr.NotFound("USER_NOT_FOUND", "用户不存在"))
		return
	}
	// Stop any live tunnels for this user before deleting rows.
	if tuns, err := h.svc.Store.ListTunnels(ctx, id); err == nil {
		ids := make([]string, 0, len(tuns))
		for _, t := range tuns {
			ids = append(ids, t.ID)
		}
		if len(ids) > 0 {
			_ = h.svc.Hub.PushStop(id, ids)
		}
	}
	if err := h.svc.Store.DeleteUser(ctx, id); err != nil {
		writeError(w, apierr.Wrapf(err, "删除用户失败"))
		return
	}
	_ = h.svc.audit(ctx, actor, "user.deleted", "user", id, u.Username, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *httpServer) handleAdminListKeys(w http.ResponseWriter, r *http.Request, actor *store.User) {
	keys, err := h.svc.Store.ListUserKeys(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取密钥失败"))
		return
	}
	if keys == nil {
		keys = []store.UserKey{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *httpServer) handleAdminAddKey(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req addKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key, err := h.svc.addKey(r.Context(), r.PathValue("id"), req.Name, req.PublicKey)
	if err != nil {
		writeError(w, apierr.As(err))
		return
	}
	_ = h.svc.audit(r.Context(), actor, "key.added", "user_key", key.ID, key.Fingerprint, clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"key": key})
}

func (h *httpServer) handleAdminDeleteKey(w http.ResponseWriter, r *http.Request, actor *store.User) {
	if err := h.svc.Store.DeleteUserKey(r.Context(), r.PathValue("id"), r.PathValue("keyID")); err != nil {
		writeError(w, apierr.NotFound("KEY_NOT_FOUND", "密钥不存在"))
		return
	}
	_ = h.svc.audit(r.Context(), actor, "key.deleted", "user_key", r.PathValue("keyID"), "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- nodes ----

func (h *httpServer) handleAdminListNodes(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := h.svc.Store.ListNodes(r.Context())
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取节点失败"))
		return
	}
	if list == nil {
		list = []store.Node{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": list})
}

type nodeRequest struct {
	Name        string `json:"name"`
	Region      string `json:"region"`
	PublicHost  string `json:"public_host"`
	ControlHost string `json:"control_host"`
	ControlPort int    `json:"control_port"`
	MaxTunnels  int    `json:"max_tunnels"`
	Enabled     *bool  `json:"enabled"`
}

func validateNode(req *nodeRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return apierr.BadRequest("INVALID_NAME", "节点名称不能为空")
	}
	if strings.TrimSpace(req.PublicHost) == "" {
		return apierr.BadRequest("INVALID_PUBLIC_HOST", "公网入口地址不能为空")
	}
	if req.ControlPort < 0 || req.ControlPort > 65535 {
		return apierr.BadRequest("INVALID_PORT", "控制端口必须在 0-65535 之间")
	}
	return nil
}

func (h *httpServer) handleAdminCreateNode(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req nodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := validateNode(&req); err != nil {
		writeError(w, apierr.As(err))
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	port := req.ControlPort
	if port == 0 {
		_, ps, _ := splitHostPortSafe(h.svc.Cfg.ControlAddr)
		port, _ = strconv.Atoi(ps)
	}
	host := req.ControlHost
	if host == "" {
		host = req.PublicHost
	}
	n := &store.Node{
		ID: ids.Hex(8), Name: strings.TrimSpace(req.Name), Region: req.Region,
		PublicHost: strings.TrimSpace(req.PublicHost), ControlHost: host, ControlPort: port,
		Enabled: enabled, MaxTunnels: req.MaxTunnels, Status: "unknown",
	}
	if err := h.svc.Store.CreateNode(r.Context(), n); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, apierr.Conflict("NODE_EXISTS", "节点名称已存在"))
			return
		}
		writeError(w, apierr.Wrapf(err, "创建节点失败"))
		return
	}
	_ = h.svc.audit(r.Context(), actor, "node.created", "node", n.ID, n.Name, clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"node": n})
}

func splitHostPortSafe(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", errors.New("no port")
	}
	return addr[:i], addr[i+1:], nil
}

func (h *httpServer) handleAdminUpdateNode(w http.ResponseWriter, r *http.Request, actor *store.User) {
	ctx := r.Context()
	n, err := h.svc.Store.GetNode(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("NODE_NOT_FOUND", "节点不存在"))
		return
	}
	var req nodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != "" {
		n.Name = strings.TrimSpace(req.Name)
	}
	if req.PublicHost != "" {
		n.PublicHost = strings.TrimSpace(req.PublicHost)
	}
	if req.ControlHost != "" {
		n.ControlHost = req.ControlHost
	}
	if req.ControlPort > 0 {
		n.ControlPort = req.ControlPort
	}
	if req.Region != "" {
		n.Region = req.Region
	}
	n.MaxTunnels = req.MaxTunnels
	if req.Enabled != nil {
		n.Enabled = *req.Enabled
		if !n.Enabled {
			// Disabling a node stops its tunnels.
			if list, err := h.svc.Store.ListTunnelsByNode(ctx, n.ID); err == nil {
				for _, t := range list {
					_ = h.svc.Hub.PushStop(t.UserID, []string{t.ID})
				}
			}
		}
	}
	if err := h.svc.Store.UpdateNode(ctx, n); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, apierr.Conflict("NODE_EXISTS", "节点名称已存在"))
			return
		}
		writeError(w, apierr.Wrapf(err, "更新节点失败"))
		return
	}
	_ = h.svc.audit(ctx, actor, "node.updated", "node", n.ID, n.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"node": n})
}

func (h *httpServer) handleAdminDeleteNode(w http.ResponseWriter, r *http.Request, actor *store.User) {
	ctx := r.Context()
	n, err := h.svc.Store.GetNode(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("NODE_NOT_FOUND", "节点不存在"))
		return
	}
	if err := h.svc.Store.DeleteNode(ctx, n.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, apierr.Conflict("NODE_IN_USE", "该节点仍有隧道，请先删除或迁移隧道"))
			return
		}
		writeError(w, apierr.Wrapf(err, "删除节点失败"))
		return
	}
	_ = h.svc.audit(ctx, actor, "node.deleted", "node", n.ID, n.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *httpServer) handleAdminCheckNode(w http.ResponseWriter, r *http.Request, actor *store.User) {
	n, err := h.svc.Store.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("NODE_NOT_FOUND", "节点不存在"))
		return
	}
	ms := h.svc.probeNode(r.Context(), n)
	status := "online"
	if ms == 0 {
		status = "offline"
	}
	writeJSON(w, http.StatusOK, map[string]any{"node_id": n.ID, "status": status, "latency_ms": ms})
}

// ---- tunnels (admin) ----

func (h *httpServer) handleAdminListTunnels(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	userFilter := r.URL.Query().Get("user_id")
	list, err := h.svc.Store.ListTunnels(ctx, userFilter)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取隧道失败"))
		return
	}
	out := make([]TunnelView, 0, len(list))
	for i := range list {
		out = append(out, h.viewTunnel(ctx, &list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnels": out})
}

func (h *httpServer) handleAdminDeleteTunnel(w http.ResponseWriter, r *http.Request, actor *store.User) {
	ctx := r.Context()
	t, err := h.svc.Store.GetTunnel(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	h.svc.Hub.StopTunnel(t.ID)
	if err := h.svc.Store.DeleteTunnel(ctx, t.ID); err != nil {
		writeError(w, apierr.Wrapf(err, "删除隧道失败"))
		return
	}
	_ = h.svc.audit(ctx, actor, "tunnel.deleted", "tunnel", t.ID, t.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *httpServer) setTunnelEnabled(w http.ResponseWriter, r *http.Request, actor *store.User, enabled bool) {
	ctx := r.Context()
	t, err := h.svc.Store.GetTunnel(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("TUNNEL_NOT_FOUND", "隧道不存在"))
		return
	}
	t.Enabled = enabled
	if !enabled {
		t.DesiredRunning = false
	}
	if err := h.svc.Store.UpdateTunnel(ctx, t); err != nil {
		writeError(w, apierr.Wrapf(err, "更新隧道失败"))
		return
	}
	if !enabled {
		h.svc.Hub.StopTunnel(t.ID)
	}
	action := "tunnel.disabled"
	if enabled {
		action = "tunnel.enabled"
	}
	_ = h.svc.audit(ctx, actor, action, "tunnel", t.ID, t.Name, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"tunnel": h.viewTunnel(ctx, t)})
}

func (h *httpServer) handleAdminDisableTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	h.setTunnelEnabled(w, r, u, false)
}

func (h *httpServer) handleAdminEnableTunnel(w http.ResponseWriter, r *http.Request, u *store.User) {
	h.setTunnelEnabled(w, r, u, true)
}

// ---- settings ----

type settingsView struct {
	DomainBinding bool   `json:"domain_binding"`
	DomainSuffix  string `json:"domain_suffix"`
	PublicHost    string `json:"public_host"`
	PortMin       int    `json:"port_min"`
	PortMax       int    `json:"port_max"`
	PortAllowlist []int  `json:"port_allowlist"`
	APIAddr       string `json:"api_addr"`
	ControlAddr   string `json:"control_addr"`
	Version       string `json:"version"`
}

func (h *httpServer) currentSettings(r *http.Request) settingsView {
	ctx := r.Context()
	lo, hi := h.svc.PortRange(ctx)
	return settingsView{
		DomainBinding: h.svc.DomainBinding(ctx),
		DomainSuffix:  h.svc.DomainSuffix(ctx),
		PublicHost:    h.svc.PublicHost(ctx),
		PortMin:       lo, PortMax: hi,
		PortAllowlist: h.svc.Cfg.Ports.Allowlist,
		APIAddr:       h.svc.Cfg.APIAddr,
		ControlAddr:   h.svc.Cfg.ControlAddr,
		Version:       h.svc.Version(),
	}
}

func (h *httpServer) handleGetSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, map[string]any{"settings": h.currentSettings(r)})
}

type updateSettingsRequest struct {
	DomainBinding *bool   `json:"domain_binding"`
	DomainSuffix  *string `json:"domain_suffix"`
	PublicHost    *string `json:"public_host"`
	PortMin       *int    `json:"port_min"`
	PortMax       *int    `json:"port_max"`
}

func (h *httpServer) handleUpdateSettings(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req updateSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	lo, hi := h.svc.PortRange(ctx)
	newLo, newHi := lo, hi
	if req.PortMin != nil {
		newLo = *req.PortMin
	}
	if req.PortMax != nil {
		newHi = *req.PortMax
	}
	if newLo < 1 || newHi > 65535 || newLo > newHi {
		writeError(w, apierr.BadRequest("INVALID_PORT_RANGE", "端口范围无效（1-65535，且起始端口不能大于结束端口）"))
		return
	}
	suffix := h.svc.DomainSuffix(ctx)
	if req.DomainSuffix != nil {
		suffix = strings.Trim(strings.ToLower(strings.TrimSpace(*req.DomainSuffix)), ".")
		if suffix != "" && !isHostname(suffix) {
			writeError(w, apierr.BadRequest("INVALID_DOMAIN", "域名后缀格式不正确"))
			return
		}
	}
	binding := h.svc.DomainBinding(ctx)
	if req.DomainBinding != nil {
		binding = *req.DomainBinding
	}
	if binding && suffix == "" {
		writeError(w, apierr.BadRequest("DOMAIN_REQUIRED", "开启域名绑定前必须填写域名后缀"))
		return
	}
	changes := []string{}
	if req.DomainBinding != nil {
		if err := h.svc.Store.SetSetting(ctx, "domain_binding", strconv.FormatBool(binding)); err != nil {
			writeError(w, apierr.Wrapf(err, "保存设置失败"))
			return
		}
		changes = append(changes, fmt.Sprintf("domain_binding=%v", binding))
	}
	if req.DomainSuffix != nil {
		if err := h.svc.Store.SetSetting(ctx, "domain_suffix", suffix); err != nil {
			writeError(w, apierr.Wrapf(err, "保存设置失败"))
			return
		}
		changes = append(changes, "domain_suffix="+suffix)
	}
	if req.PublicHost != nil {
		if err := h.svc.Store.SetSetting(ctx, "public_host", strings.TrimSpace(*req.PublicHost)); err != nil {
			writeError(w, apierr.Wrapf(err, "保存设置失败"))
			return
		}
		changes = append(changes, "public_host="+*req.PublicHost)
	}
	if req.PortMin != nil || req.PortMax != nil {
		if err := h.svc.Store.SetSetting(ctx, "port_min", strconv.Itoa(newLo)); err != nil {
			writeError(w, apierr.Wrapf(err, "保存设置失败"))
			return
		}
		if err := h.svc.Store.SetSetting(ctx, "port_max", strconv.Itoa(newHi)); err != nil {
			writeError(w, apierr.Wrapf(err, "保存设置失败"))
			return
		}
		changes = append(changes, fmt.Sprintf("ports=%d-%d", newLo, newHi))
	}
	_ = h.svc.audit(ctx, actor, "settings.updated", "settings", "", strings.Join(changes, ", "), clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"settings": h.currentSettings(r)})
}

func (h *httpServer) handleAudit(w http.ResponseWriter, r *http.Request, u *store.User) {
	limit := queryInt(r, "limit", 100)
	offset := queryInt(r, "offset", 0)
	list, err := h.svc.Store.ListAudit(r.Context(), limit, offset)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取审计日志失败"))
		return
	}
	if list == nil {
		list = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list, "limit": limit, "offset": offset})
}

func (h *httpServer) handlePortUsage(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	lo, hi := h.svc.PortRange(ctx)
	nodes, _ := h.svc.Store.ListNodes(ctx)
	type usage struct {
		NodeID string `json:"node_id"`
		Name   string `json:"name"`
		Used   []int  `json:"used"`
	}
	out := make([]usage, 0, len(nodes))
	for _, n := range nodes {
		ports, _ := h.svc.Store.UsedPorts(ctx, n.ID)
		list := make([]int, 0, len(ports))
		for p := range ports {
			list = append(list, p)
		}
		out = append(out, usage{NodeID: n.ID, Name: n.Name, Used: list})
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": map[string]int{"min": lo, "max": hi}, "nodes": out})
}
