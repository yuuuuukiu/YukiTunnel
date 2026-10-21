package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/apierr"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
)

// Service ties the store to credential verification and token issuance.
type Service struct {
	Store    *store.Store
	TTL      time.Duration
	MaxFails int
	Lockout  time.Duration
}

// New builds an auth service.
func New(st *store.Store, ttl time.Duration, maxFails int, lockout time.Duration) *Service {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	if maxFails <= 0 {
		maxFails = 10
	}
	if lockout <= 0 {
		lockout = 15 * time.Minute
	}
	return &Service{Store: st, TTL: ttl, MaxFails: maxFails, Lockout: lockout}
}

// LoginResult is returned by password and key login.
type LoginResult struct {
	Token     string      `json:"token"`
	ExpiresAt time.Time   `json:"expires_at"`
	User      *store.User `json:"user"`
}

// AuthenticatePassword verifies username/password, applies lockout policy and
// issues a session token.
func (s *Service) AuthenticatePassword(ctx context.Context, username, password, ip string) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, apierr.BadRequest("INVALID_CREDENTIALS", "请输入用户名和密码")
	}
	fails, err := s.Store.RecentFailures(ctx, username, ip, s.Lockout)
	if err != nil {
		return nil, apierr.Wrapf(err, "读取登录记录失败")
	}
	if fails >= s.MaxFails {
		_ = s.Store.AddAudit(ctx, &store.AuditEntry{
			ID: ids.Hex(12), Action: "auth.locked", ActorName: username, IP: ip,
			Detail: fmt.Sprintf("%d failed attempts", fails),
		})
		return nil, apierr.New(429, "ACCOUNT_LOCKED",
			fmt.Sprintf("登录失败次数过多，请 %d 分钟后再试", int(s.Lockout.Minutes())))
	}
	u, err := s.Store.GetUserByUsername(ctx, username)
	if err != nil || u == nil || u.PasswordHash == "" || !VerifyPassword(u.PasswordHash, password) {
		_ = s.Store.AddLoginAttempt(ctx, username, ip, false)
		return nil, apierr.Unauthorized("AUTH_FAILED", "用户名或密码错误")
	}
	if u.Disabled {
		_ = s.Store.AddLoginAttempt(ctx, username, ip, false)
		return nil, apierr.Forbidden("USER_DISABLED", "该账号已被管理员禁用")
	}
	_ = s.Store.AddLoginAttempt(ctx, username, ip, true)
	return s.issue(ctx, u)
}

// issue creates and persists a session token.
func (s *Service) issue(ctx context.Context, u *store.User) (*LoginResult, error) {
	plaintext, hash := NewToken()
	now := time.Now().UTC()
	tok := &store.APIToken{
		ID:        ids.Hex(12),
		UserID:    u.ID,
		Name:      "session",
		TokenHash: hash,
		Scope:     "session",
		CreatedAt: now,
		ExpiresAt: now.Add(s.TTL),
	}
	if err := s.Store.CreateAPIToken(ctx, tok); err != nil {
		return nil, apierr.Wrapf(err, "创建会话失败")
	}
	_ = s.Store.TouchLogin(ctx, u.ID)
	_ = s.Store.PruneLoginAttempts(ctx, 24*time.Hour)
	return &LoginResult{Token: plaintext, ExpiresAt: tok.ExpiresAt, User: u}, nil
}

// IssueForUser issues a session token after an out-of-band authentication
// (for example RSA challenge-response) has already succeeded.
func (s *Service) IssueForUser(ctx context.Context, u *store.User) (*LoginResult, error) {
	return s.issue(ctx, u)
}

// AuthenticateToken resolves a bearer token to a user.
func (s *Service) AuthenticateToken(ctx context.Context, plaintext string) (*store.User, *store.APIToken, error) {
	if plaintext == "" {
		return nil, nil, apierr.Unauthorized("UNAUTHENTICATED", "请先登录")
	}
	tok, u, err := s.Store.FindAPITokenByHash(ctx, HashToken(plaintext))
	if err != nil {
		return nil, nil, apierr.Unauthorized("TOKEN_INVALID", "登录已失效，请重新登录")
	}
	if tok.RevokedAt != (time.Time{}) {
		return nil, nil, apierr.Unauthorized("TOKEN_REVOKED", "该登录凭证已被撤销")
	}
	if time.Now().After(tok.ExpiresAt) {
		return nil, nil, apierr.Unauthorized("TOKEN_EXPIRED", "登录已过期，请重新登录")
	}
	if u.Disabled {
		return nil, nil, apierr.Forbidden("USER_DISABLED", "该账号已被管理员禁用")
	}
	_ = s.Store.TouchAPIToken(ctx, tok.ID)
	return u, tok, nil
}

// Logout revokes the presented token.
func (s *Service) Logout(ctx context.Context, tokenID string) error {
	err := s.Store.RevokeAPIToken(ctx, tokenID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// CreateUser provisions an account (admin-only operation, enforced by caller).
func (s *Service) CreateUser(ctx context.Context, username, displayName, password, role string) (*store.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, apierr.BadRequest("INVALID_USERNAME", "用户名不能为空")
	}
	if len(username) > 64 || strings.ContainsAny(username, " \t/\\") {
		return nil, apierr.BadRequest("INVALID_USERNAME", "用户名不能包含空格或斜杠，且不超过 64 个字符")
	}
	if role == "" {
		role = "user"
	}
	if role != "user" && role != "admin" {
		return nil, apierr.BadRequest("INVALID_ROLE", "角色必须是 user 或 admin")
	}
	var hash string
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return nil, apierr.BadRequest("WEAK_PASSWORD", err.Error())
		}
		hash = h
	}
	u := &store.User{
		ID:           ids.Hex(12),
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		Role:         role,
	}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, apierr.Conflict("USER_EXISTS", "用户名 "+username+" 已存在")
		}
		return nil, apierr.Wrapf(err, "创建用户失败")
	}
	return u, nil
}
