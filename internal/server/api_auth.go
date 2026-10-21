package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/apierr"
	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
)

func (h *httpServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.svc.Store.DB().PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "degraded", "database": "unavailable", "error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"version":  h.svc.Version(),
		"time":     time.Now().UTC(),
		"database": "ok",
	})
}

func (h *httpServer) handleMeta(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lo, hi := h.svc.PortRange(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":           "YukiTunnel",
		"version":        h.svc.Version(),
		"domain_binding": h.svc.DomainBinding(ctx),
		"domain_suffix":  h.svc.DomainSuffix(ctx),
		"public_host":    h.svc.PublicHost(ctx),
		"port_range":     map[string]int{"min": lo, "max": hi},
		"registration":   false,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *httpServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.svc.Auth.AuthenticatePassword(r.Context(), req.Username, req.Password, clientIP(r))
	if err != nil {
		_ = h.svc.audit(r.Context(), nil, "auth.login_failed", "user", req.Username, "password login failed", clientIP(r))
		writeError(w, apierr.As(err))
		return
	}
	_ = h.svc.audit(r.Context(), res.User, "auth.login", "user", res.User.ID, "password login", clientIP(r))
	writeJSON(w, http.StatusOK, res)
}

type keyChallengeRequest struct {
	Username string `json:"username"`
}

func (h *httpServer) handleKeyChallenge(w http.ResponseWriter, r *http.Request) {
	var req keyChallengeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" {
		writeError(w, apierr.BadRequest("INVALID_USERNAME", "请提供用户名"))
		return
	}
	nonce := ids.Token()
	h.svc.putChallenge(nonce, req.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"nonce":      nonce,
		"expires_in": int(auth.ChallengeTTL.Seconds()),
		"algorithm":  "RSASSA-PKCS1-v1_5-SHA256",
	})
}

type keyLoginRequest struct {
	Username  string `json:"username"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

func (h *httpServer) handleKeyLogin(w http.ResponseWriter, r *http.Request) {
	var req keyLoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	username, ok := h.svc.takeChallenge(req.Nonce)
	if !ok || username != req.Username {
		writeError(w, apierr.Unauthorized("CHALLENGE_INVALID", "挑战码无效或已过期，请重试"))
		return
	}
	ctx := r.Context()
	u, err := h.svc.Store.GetUserByUsername(ctx, req.Username)
	if err != nil || u.Disabled {
		writeError(w, apierr.Unauthorized("AUTH_FAILED", "认证失败"))
		return
	}
	keys, err := h.svc.Store.ListUserKeys(ctx, u.ID)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取密钥失败"))
		return
	}
	var matched *store.UserKey
	for i := range keys {
		if auth.VerifyChallenge(keys[i].PublicKey, req.Nonce, req.Signature) == nil {
			matched = &keys[i]
			break
		}
	}
	if matched == nil {
		_ = h.svc.audit(ctx, nil, "auth.key_login_failed", "user", req.Username, "no key matched signature", clientIP(r))
		writeError(w, apierr.Unauthorized("KEY_MISMATCH", "RSA 私钥与服务端登记的公钥不匹配"))
		return
	}
	_ = h.svc.Store.TouchUserKey(ctx, matched.ID)
	res, err := h.svc.Auth.IssueForUser(ctx, u)
	if err != nil {
		writeError(w, apierr.As(err))
		return
	}
	_ = h.svc.audit(ctx, u, "auth.key_login", "user", u.ID, "key "+matched.Fingerprint, clientIP(r))
	writeJSON(w, http.StatusOK, res)
}

func (h *httpServer) handleLogout(w http.ResponseWriter, r *http.Request, u *store.User) {
	_, tok, err := h.svc.Auth.AuthenticateToken(r.Context(), bearerToken(r))
	if err == nil && tok != nil {
		_ = h.svc.Auth.Logout(r.Context(), tok.ID)
	}
	_ = h.svc.audit(r.Context(), u, "auth.logout", "user", u.ID, "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *httpServer) handleMe(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":           u,
		"domain_binding": h.svc.DomainBinding(r.Context()),
		"domain_suffix":  h.svc.DomainSuffix(r.Context()),
	})
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *httpServer) handleChangePassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req passwordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if u.PasswordHash != "" && !auth.VerifyPassword(u.PasswordHash, req.CurrentPassword) {
		writeError(w, apierr.Unauthorized("AUTH_FAILED", "当前密码不正确"))
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, apierr.BadRequest("WEAK_PASSWORD", err.Error()))
		return
	}
	u.PasswordHash = hash
	if err := h.svc.Store.UpdateUser(r.Context(), u); err != nil {
		writeError(w, apierr.Wrapf(err, "更新密码失败"))
		return
	}
	_ = h.svc.audit(r.Context(), u, "user.password_changed", "user", u.ID, "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *httpServer) handleListMyKeys(w http.ResponseWriter, r *http.Request, u *store.User) {
	keys, err := h.svc.Store.ListUserKeys(r.Context(), u.ID)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取密钥失败"))
		return
	}
	if keys == nil {
		keys = []store.UserKey{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

type addKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

func (h *httpServer) handleAddMyKey(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req addKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key, err := h.svc.addKey(r.Context(), u.ID, req.Name, req.PublicKey)
	if err != nil {
		writeError(w, apierr.As(err))
		return
	}
	_ = h.svc.audit(r.Context(), u, "key.added", "user_key", key.ID, key.Fingerprint, clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"key": key})
}

func (h *httpServer) handleDeleteMyKey(w http.ResponseWriter, r *http.Request, u *store.User) {
	if err := h.svc.Store.DeleteUserKey(r.Context(), u.ID, r.PathValue("id")); err != nil {
		writeError(w, apierr.NotFound("KEY_NOT_FOUND", "密钥不存在"))
		return
	}
	_ = h.svc.audit(r.Context(), u, "key.deleted", "user_key", r.PathValue("id"), "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *httpServer) handleListMyTokens(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := h.svc.Store.ListAPITokens(r.Context(), u.ID)
	if err != nil {
		writeError(w, apierr.Wrapf(err, "读取令牌失败"))
		return
	}
	if list == nil {
		list = []store.APIToken{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

type createTokenRequest struct {
	Name     string `json:"name"`
	TTLHours int    `json:"ttl_hours"`
}

func (h *httpServer) handleCreateMyToken(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req createTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TTLHours <= 0 {
		req.TTLHours = 24 * 30
	}
	plaintext, hash := auth.NewToken()
	tok := &store.APIToken{
		ID: ids.Hex(12), UserID: u.ID, Name: req.Name, TokenHash: hash, Scope: "api",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Duration(req.TTLHours) * time.Hour),
	}
	if err := h.svc.Store.CreateAPIToken(r.Context(), tok); err != nil {
		writeError(w, apierr.Wrapf(err, "创建令牌失败"))
		return
	}
	_ = h.svc.audit(r.Context(), u, "token.created", "api_token", tok.ID, tok.Name, clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"token": plaintext, "meta": tok})
}

func (h *httpServer) handleRevokeMyToken(w http.ResponseWriter, r *http.Request, u *store.User) {
	tok, err := h.svc.Store.GetAPIToken(r.Context(), r.PathValue("id"))
	if err != nil || tok.UserID != u.ID {
		writeError(w, apierr.NotFound("TOKEN_NOT_FOUND", "令牌不存在"))
		return
	}
	_ = h.svc.Store.RevokeAPIToken(r.Context(), tok.ID)
	_ = h.svc.audit(r.Context(), u, "token.revoked", "api_token", tok.ID, "", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// challenge store

func (s *Service) putChallenge(nonce, username string) {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	if s.challenges == nil {
		s.challenges = map[string]challenge{}
	}
	// opportunistic GC
	for k, v := range s.challenges {
		if time.Since(v.at) > auth.ChallengeTTL {
			delete(s.challenges, k)
		}
	}
	s.challenges[nonce] = challenge{username: username, at: time.Now()}
}

func (s *Service) takeChallenge(nonce string) (string, bool) {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	c, ok := s.challenges[nonce]
	if !ok {
		return "", false
	}
	delete(s.challenges, nonce)
	if time.Since(c.at) > auth.ChallengeTTL {
		return "", false
	}
	return c.username, true
}

type challenge struct {
	username string
	at       time.Time
}

func (s *Service) addKey(ctx context.Context, userID, name, publicKey string) (*store.UserKey, error) {
	if publicKey == "" {
		return nil, apierr.BadRequest("INVALID_KEY", "公钥不能为空")
	}
	canonical, fp, err := auth.NormalizePublicKey(publicKey)
	if err != nil {
		return nil, apierr.BadRequest("INVALID_KEY", "公钥格式不正确，请粘贴 ssh-rsa AAAA... 内容")
	}
	k := &store.UserKey{
		ID: ids.Hex(12), UserID: userID, Name: name, Fingerprint: fp, PublicKey: canonical,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateUserKey(ctx, k); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, apierr.Conflict("KEY_EXISTS", "该公钥已登记")
		}
		return nil, apierr.Wrapf(err, "登记公钥失败")
	}
	return k, nil
}
