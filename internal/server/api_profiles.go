package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/apierr"
	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile"
	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile/registry"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
	"gopkg.in/yaml.v3"
)

// profileRegistry lazily builds the registry from config so that operators can
// change mirrors/proxy without a restart.
func (s *Service) profileRegistry(ctx context.Context) (*registry.Registry, error) {
	mirrors := s.ProfileMirrors(ctx)
	proxy := s.ProfileProxy(ctx)
	return registry.New(registry.Options{
		Mirrors:     mirrors,
		Proxy:       proxy,
		TrustedKeys: s.trustedProfileKeys(),
		CacheDir:    filepath.Join(s.Cfg.DataDir, "profiles-cache"),
		TTL:         12 * time.Hour,
	})
}

// ProfileMirrors returns the configured mirror chain.
//
// Precedence: YUKI_PROFILE_MIRRORS env (replaces the built-in chain entirely,
// for air-gapped installs and tests) > the `profile_mirrors` setting (tried
// before the built-ins) > the built-in chain.
func (s *Service) ProfileMirrors(ctx context.Context) []string {
	if raw := strings.TrimSpace(os.Getenv("YUKI_PROFILE_MIRRORS")); raw != "" {
		out := []string{}
		for _, m := range strings.Split(raw, ",") {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
		return out
	}
	raw, _ := s.Store.GetSetting(ctx, "profile_mirrors", "")
	mirrors := registry.DefaultMirrors
	if strings.TrimSpace(raw) != "" {
		custom := []string{}
		for _, m := range strings.Split(raw, ",") {
			if m = strings.TrimSpace(m); m != "" {
				custom = append(custom, m)
			}
		}
		// Custom mirrors are tried first, then the built-in chain.
		mirrors = append(custom, registry.DefaultMirrors...)
	}
	return mirrors
}

// ProfileProxy returns the configured proxy URL for profile downloads and
// provider calls.
func (s *Service) ProfileProxy(ctx context.Context) string {
	v, _ := s.Store.GetSetting(ctx, "profile_proxy", "")
	return strings.TrimSpace(v)
}

// trustedProfileKeys loads the pinned signing keys. The built-in key is
// compiled in; operators may add more via `profile_keys` in settings.
func (s *Service) trustedProfileKeys() map[string]ed25519PublicKey {
	return builtinProfileKeys()
}

// profileEngine builds an execution engine using the configured proxy.
func (s *Service) profileEngine(ctx context.Context) (*cloudprofile.Engine, error) {
	return cloudprofile.NewEngine(s.ProfileProxy(ctx))
}

// sealCredentials encrypts cloud credentials with the shared store helper so
// the CLI, the API and any future writer all use the same key and envelope.
func (s *Service) sealCredentials(v map[string]string) (string, error) {
	return store.SealCredentials(s.Cfg.DataDir, v)
}

// openCredentials decrypts a stored credential blob.
func (s *Service) openCredentials(sealed string) (map[string]string, error) {
	return store.OpenCredentials(s.Cfg.DataDir, sealed)
}

// ---- HTTP handlers ----

// handleProfileIndex returns the signed catalogue from the mirror chain.
func (h *httpServer) handleProfileIndex(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	reg, err := h.svc.profileRegistry(ctx)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "初始化 profile registry 失败"))
		return
	}
	idx, err := reg.Refresh(ctx)
	if err != nil && errors.Is(err, registry.ErrVerification) {
		// Integrity failure: never fall back. If an attacker can block the
		// mirrors, a fallback would turn that into "use unverified profiles".
		writeError(w, apierr.New(http.StatusBadGateway, "PROFILE_INTEGRITY_FAILURE",
			"profile 索引验签失败，已拒绝使用任何未验证的内容："+err.Error()))
		return
	}
	if err != nil {
		// Offline fallback: the binary ships the official profiles, so a fresh
		// install behind a firewall can still use them. They are labelled
		// "builtin" rather than "verified" so the UI can explain the difference.
		bundled, berr := cloudprofile.BundledIndex()
		if berr != nil || len(bundled) == 0 {
			writeError(w, apierr.New(http.StatusBadGateway, "REGISTRY_UNAVAILABLE",
				"无法获取 profile 索引（所有镜像均失败）："+err.Error()))
			return
		}
		entries := make([]cloudprofile.IndexEntry, 0, len(bundled))
		for _, b := range bundled {
			entries = append(entries, cloudprofile.IndexEntry{
				ID: b.ID, Vendor: b.Vendor, Name: b.Name, Version: b.Version,
				Path: "profiles/" + b.ID + ".yaml", SHA256: b.SHA256,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"source":   "builtin",
			"key_id":   "builtin",
			"profiles": entries,
			"verified": false,
			"offline":  true,
			"warning":  "无法连接 profile 镜像，正在使用二进制内置的 profile（未联网验签）：" + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":       reg.Source(),
		"generated_at": idx.GeneratedAt,
		"key_id":       idx.KeyID,
		"profiles":     idx.Profiles,
		"verified":     true,
	})
}

// handleProfileList lists installed profiles.
func (h *httpServer) handleProfileList(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := h.svc.Store.ListCloudProfiles(r.Context())
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取 profile 失败"))
		return
	}
	if list == nil {
		list = []store.CloudProfile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
}

type installProfileRequest struct {
	ID string `json:"id"`
	// Document installs a local (already verified) YAML instead of fetching.
	Document string `json:"document"`
	SHA256   string `json:"sha256"`
}

// handleProfileInstall fetches, verifies and stores a profile.
func (h *httpServer) handleProfileInstall(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req installProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	var p *cloudprofile.Profile

	if req.Document != "" {
		// Local import: still require an explicit hash so an operator cannot
		// silently install a modified document.
		sum := cloudprofile.Digest([]byte(req.Document))
		if req.SHA256 == "" || !strings.EqualFold(sum, req.SHA256) {
			writeError(w, apierr.BadRequest("PROFILE_HASH_MISMATCH",
				fmt.Sprintf("本地 profile 的 sha256 为 %s，请显式确认后安装", sum)))
			return
		}
		var parsed cloudprofile.Profile
		if err := yaml.Unmarshal([]byte(req.Document), &parsed); err != nil {
			writeError(w, apierr.BadRequest("PROFILE_PARSE_ERROR", "YAML 解析失败："+err.Error()))
			return
		}
		if err := parsed.Validate(); err != nil {
			writeError(w, apierr.BadRequest("PROFILE_INVALID", err.Error()))
			return
		}
		parsed.SHA256 = sum
		parsed.Source = "local-import"
		p = &parsed
	} else {
		if req.ID == "" {
			writeError(w, apierr.BadRequest("PROFILE_ID_REQUIRED", "请提供 profile id"))
			return
		}
		reg, err := h.svc.profileRegistry(ctx)
		if err != nil {
			writeError(w, apierr.Wrapf(err, "初始化 registry 失败"))
			return
		}
		p, err = reg.Load(ctx, req.ID)
		if err != nil && errors.Is(err, registry.ErrVerification) {
			writeError(w, apierr.New(http.StatusBadGateway, "PROFILE_INTEGRITY_FAILURE",
				"profile 验签失败，已拒绝安装："+err.Error()))
			return
		}
		if err != nil {
			// Offline (transport failure only): install the embedded copy.
			bundled, berr := cloudprofile.BundledProfile(req.ID)
			if berr != nil {
				writeError(w, apierr.New(http.StatusBadGateway, "PROFILE_LOAD_FAILED", err.Error()))
				return
			}
			h.svc.Log.Warn("profile registry unreachable, installing bundled copy",
				"profile", req.ID, "err", err)
			p = bundled
		}
	}

	raw, err := yaml.Marshal(p)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "序列化 profile 失败"))
		return
	}
	rec := &store.CloudProfile{
		ID: p.Metadata.ID, Vendor: p.Metadata.Vendor, Name: p.Metadata.Name,
		Version: p.Metadata.Version, Source: p.Source, SHA256: p.SHA256,
		Document: string(raw), Enabled: true,
	}
	if err := h.svc.Store.UpsertCloudProfile(ctx, rec); err != nil {
		writeError(w, apierr.Wrapf(err, "保存 profile 失败"))
		return
	}
	_ = h.svc.audit(ctx, actor, "profile.installed", "profile", rec.ID,
		fmt.Sprintf("%s@%s sha256=%s source=%s", rec.ID, rec.Version, short(rec.SHA256), rec.Source), clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"profile": rec})
}

// handleProfileGet returns one installed profile including its YAML.
func (h *httpServer) handleProfileGet(w http.ResponseWriter, r *http.Request, u *store.User) {
	p, err := h.svc.Store.GetCloudProfile(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("PROFILE_NOT_FOUND", "profile 未安装"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": p, "document": p.Document})
}

// handleProfileToggle enables/disables a profile.
func (h *httpServer) handleProfileToggle(w http.ResponseWriter, r *http.Request, actor *store.User) {
	enabled := r.PathValue("state") == "enable"
	if err := h.svc.Store.SetCloudProfileEnabled(r.Context(), r.PathValue("id"), enabled); err != nil {
		writeError(w, apierr.NotFound("PROFILE_NOT_FOUND", "profile 未安装"))
		return
	}
	_ = h.svc.audit(r.Context(), actor, "profile."+r.PathValue("state"), "profile", r.PathValue("id"), "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": enabled})
}

// handleProfileDelete removes an installed profile.
func (h *httpServer) handleProfileDelete(w http.ResponseWriter, r *http.Request, actor *store.User) {
	if err := h.svc.Store.DeleteCloudProfile(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, apierr.NotFound("PROFILE_NOT_FOUND", "profile 未安装"))
		return
	}
	_ = h.svc.audit(r.Context(), actor, "profile.deleted", "profile", r.PathValue("id"), "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- credentials ----

type credentialRequest struct {
	Label  string            `json:"label"`
	Fields map[string]string `json:"fields"`
}

// handleProfileCredentialPut stores encrypted cloud credentials.
func (h *httpServer) handleProfileCredentialPut(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req credentialRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	profileID := r.PathValue("id")
	p, err := h.svc.Store.GetCloudProfile(ctx, profileID)
	if err != nil {
		writeError(w, apierr.NotFound("PROFILE_NOT_FOUND", "profile 未安装"))
		return
	}
	if req.Label == "" {
		req.Label = "default"
	}
	// Validate the field set against the profile declaration so typos surface
	// before an execution fails.
	var doc cloudprofile.Profile
	if err := yaml.Unmarshal([]byte(p.Document), &doc); err != nil {
		writeError(w, apierr.Internal("已安装的 profile 文档损坏"))
		return
	}
	allowed := map[string]bool{}
	for _, f := range doc.Spec.Auth.CredentialFields {
		allowed[f] = true
	}
	for k := range req.Fields {
		if !allowed[k] {
			writeError(w, apierr.BadRequest("UNKNOWN_CREDENTIAL_FIELD",
				fmt.Sprintf("字段 %q 不在 profile 声明的 credential_fields 中", k)))
			return
		}
	}
	sealed, err := h.svc.sealCredentials(req.Fields)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "加密凭证失败"))
		return
	}
	rec := &store.ProfileCredential{
		ID: ids.Hex(12), ProfileID: profileID, Label: req.Label, FieldsJSON: sealed,
	}
	if err := h.svc.Store.UpsertProfileCredential(ctx, rec); err != nil {
		writeError(w, apierr.Wrapf(err, "保存凭证失败"))
		return
	}
	// Audit only the key prefix, never the secret.
	preview := ""
	for _, k := range []string{"access_key_id", "secret_id"} {
		if v, ok := req.Fields[k]; ok && len(v) > 4 {
			preview = k + "=" + v[:4] + "…"
			break
		}
	}
	_ = h.svc.audit(ctx, actor, "profile.credential_set", "profile", profileID,
		fmt.Sprintf("label=%s %s", req.Label, preview), clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "label": req.Label})
}

// handleProfileCredentialList lists credential labels and the *masked* field
// names. Secret values are never returned by any endpoint.
func (h *httpServer) handleProfileCredentialList(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := h.svc.Store.ListProfileCredentials(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取凭证失败"))
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		fields, ferr := h.svc.openCredentials(c.FieldsJSON)
		masked := map[string]string{}
		if ferr == nil {
			masked = store.MaskCredentials(fields)
			// Access key ids are identifiers, not secrets: keep a short prefix
			// so operators can tell two accounts apart.
			for _, k := range []string{"access_key_id", "secret_id"} {
				if v, ok := fields[k]; ok {
					masked[k] = store.MaskSecret(v)
				}
			}
		}
		out = append(out, map[string]any{
			"id": c.ID, "profile_id": c.ProfileID, "label": c.Label,
			"fields": masked, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": out})
}

// handleProfileCredentialDelete removes a credential set.
func (h *httpServer) handleProfileCredentialDelete(w http.ResponseWriter, r *http.Request, actor *store.User) {
	if err := h.svc.Store.DeleteProfileCredential(r.Context(), r.PathValue("credID")); err != nil {
		writeError(w, apierr.NotFound("CREDENTIAL_NOT_FOUND", "凭证不存在"))
		return
	}
	_ = h.svc.audit(r.Context(), actor, "profile.credential_deleted", "profile", r.PathValue("id"), "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- execution ----

type runProfileRequest struct {
	Action      string         `json:"action"`
	NodeID      string         `json:"node_id"`
	PortMin     int            `json:"port_min"`
	PortMax     int            `json:"port_max"`
	Protocol    string         `json:"protocol"`
	Credential  string         `json:"credential_label"`
	Params      map[string]any `json:"params"`
	DryRun      bool           `json:"dry_run"`
	Acknowledge bool           `json:"acknowledge_permissions"`
}

// handleProfileRun executes ensure/revoke for a node's port range.
func (h *httpServer) handleProfileRun(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req runProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.Action != cloudprofile.ActionEnsurePortAllowed && req.Action != cloudprofile.ActionRevokePortAllowed {
		writeError(w, apierr.BadRequest("INVALID_ACTION", "action 必须是 ensure_port_allowed 或 revoke_port_allowed"))
		return
	}
	if !req.DryRun && !req.Acknowledge {
		writeError(w, apierr.BadRequest("PERMISSIONS_NOT_ACKNOWLEDGED",
			"请先确认 profile 声明的云权限（acknowledge_permissions=true）"))
		return
	}
	rec, err := h.svc.Store.GetCloudProfile(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, apierr.NotFound("PROFILE_NOT_FOUND", "profile 未安装"))
		return
	}
	if !rec.Enabled {
		writeError(w, apierr.Forbidden("PROFILE_DISABLED", "该 profile 已被禁用"))
		return
	}
	var doc cloudprofile.Profile
	if err := yaml.Unmarshal([]byte(rec.Document), &doc); err != nil {
		writeError(w, apierr.Internal("profile 文档损坏"))
		return
	}
	if err := doc.Validate(); err != nil {
		writeError(w, apierr.Internal("profile 校验失败："+err.Error()))
		return
	}
	doc.SHA256 = rec.SHA256

	// Merge node-derived defaults into params.
	params := map[string]any{}
	for k, v := range req.Params {
		params[k] = v
	}
	if req.PortMin > 0 {
		params["port_min"] = req.PortMin
	}
	if req.PortMax == 0 {
		params["port_max"] = params["port_min"]
	} else if req.PortMax > 0 {
		params["port_max"] = req.PortMax
	}
	if req.Protocol != "" {
		params["protocol"] = req.Protocol
	}
	if req.NodeID != "" {
		if node, err := h.svc.Store.GetNode(ctx, req.NodeID); err == nil {
			if _, ok := params["region"]; !ok && node.Region != "" {
				params["region"] = node.Region
			}
			if _, ok := params["source_cidr"]; !ok {
				if cidr, _ := h.svc.Store.GetSetting(ctx, "profile_source_cidr", ""); cidr != "" {
					params["source_cidr"] = cidr
				}
			}
		}
	}

	label := req.Credential
	if label == "" {
		label = "default"
	}
	cred, err := h.svc.Store.GetProfileCredential(ctx, rec.ID, label)
	if err != nil {
		writeError(w, apierr.BadRequest("CREDENTIAL_MISSING",
			fmt.Sprintf("未找到凭证 %q，请先在「云 Profile」中配置", label)))
		return
	}
	creds, err := h.svc.openCredentials(cred.FieldsJSON)
	if err != nil {
		writeError(w, apierr.Internal("解密凭证失败："+err.Error()))
		return
	}

	eng, err := h.svc.profileEngine(ctx)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "初始化执行引擎失败"))
		return
	}
	res, runErr := eng.Run(ctx, &doc, cloudprofile.RunOptions{
		Action: req.Action, Params: params, Creds: creds, DryRun: req.DryRun,
	})

	run := &store.ProfileRun{
		ID: ids.Hex(12), ProfileID: rec.ID, ProfileSHA: rec.SHA256, Action: req.Action,
		NodeID: req.NodeID, PortMin: intOr(params["port_min"]), PortMax: intOr(params["port_max"]),
		Protocol: fmt.Sprint(params["protocol"]), ActorID: actor.ID, ActorName: actor.Username,
	}
	if res != nil {
		run.DurationMS = res.Duration.Milliseconds()
		run.Message = res.Message
		if res.Status == 0 || res.Skipped {
			run.Status = "skipped"
		} else {
			run.Status = "success"
		}
	}
	if runErr != nil {
		run.Status = "failed"
		run.Message = runErr.Error()
	}
	_ = h.svc.Store.AddProfileRun(ctx, run)
	_ = h.svc.audit(ctx, actor, "profile."+req.Action, "profile", rec.ID, run.Message, clientIP(r))

	if runErr != nil {
		writeError(w, apierr.New(http.StatusBadGateway, "PROFILE_EXECUTION_FAILED", runErr.Error()).
			WithDetails(map[string]any{"run": run}))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "run": run})
}

// handleProfileRuns lists execution history.
func (h *httpServer) handleProfileRuns(w http.ResponseWriter, r *http.Request, u *store.User) {
	limit := queryInt(r, "limit", 100)
	list, err := h.svc.Store.ListProfileRuns(r.Context(), r.URL.Query().Get("profile_id"), limit)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取执行记录失败"))
		return
	}
	if list == nil {
		list = []store.ProfileRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": list})
}

// handleProfileSettings reads mirror/proxy configuration.
func (h *httpServer) handleProfileSettings(w http.ResponseWriter, r *http.Request, u *store.User) {
	ctx := r.Context()
	mirrors := h.svc.ProfileMirrors(ctx)
	proxy := h.svc.ProfileProxy(ctx)
	sourceCIDR, _ := h.svc.Store.GetSetting(ctx, "profile_source_cidr", "0.0.0.0/0")
	writeJSON(w, http.StatusOK, map[string]any{
		"mirrors":     mirrors,
		"proxy":       proxy,
		"source_cidr": sourceCIDR,
		"trusted_keys": func() []string {
			keys := make([]string, 0, len(builtinProfileKeys()))
			for k := range builtinProfileKeys() {
				keys = append(keys, k)
			}
			return keys
		}(),
	})
}

type profileSettingsRequest struct {
	Mirrors    *string `json:"mirrors"`
	Proxy      *string `json:"proxy"`
	SourceCIDR *string `json:"source_cidr"`
}

// handleProfileSettingsUpdate persists mirror/proxy configuration.
func (h *httpServer) handleProfileSettingsUpdate(w http.ResponseWriter, r *http.Request, actor *store.User) {
	var req profileSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.Proxy != nil {
		p := strings.TrimSpace(*req.Proxy)
		if p != "" && !strings.HasPrefix(p, "http://") && !strings.HasPrefix(p, "https://") &&
			!strings.HasPrefix(p, "socks5://") && !strings.HasPrefix(p, "socks5h://") {
			writeError(w, apierr.BadRequest("INVALID_PROXY", "代理地址必须以 http://、https:// 或 socks5:// 开头"))
			return
		}
		if err := h.svc.Store.SetSetting(ctx, "profile_proxy", p); err != nil {
			writeError(w, apierr.Wrapf(err, "保存代理失败"))
			return
		}
	}
	if req.Mirrors != nil {
		if err := h.svc.Store.SetSetting(ctx, "profile_mirrors", strings.TrimSpace(*req.Mirrors)); err != nil {
			writeError(w, apierr.Wrapf(err, "保存镜像失败"))
			return
		}
	}
	if req.SourceCIDR != nil {
		if err := h.svc.Store.SetSetting(ctx, "profile_source_cidr", strings.TrimSpace(*req.SourceCIDR)); err != nil {
			writeError(w, apierr.Wrapf(err, "保存来源网段失败"))
			return
		}
	}
	_ = h.svc.audit(ctx, actor, "profile.settings_updated", "settings", "", "", clientIP(r))
	h.handleProfileSettings(w, r, actor)
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func intOr(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

var _ = io.Discard
