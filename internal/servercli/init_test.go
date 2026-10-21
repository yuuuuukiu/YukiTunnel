package servercli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
)

func TestCreateAdminToken(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open("file:" + filepath.Join(dir, "yuki.db") + "?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()

	// When no admin exists, createAdminToken should fail cleanly.
	if _, err := createAdminToken(ctx, st, 720); err == nil {
		t.Fatal("expected error when no admin user exists")
	}

	// Create an admin user.
	hash, _ := auth.HashPassword("adminpass")
	u := &store.User{ID: ids.Hex(12), Username: "admin", Role: "admin", PasswordHash: hash}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	token, err := createAdminToken(ctx, st, 100)
	if err != nil {
		t.Fatalf("createAdminToken failed: %v", err)
	}
	if !strings.HasPrefix(token, "yk_") {
		t.Fatalf("expected yk_ prefix on token, got %s", token)
	}

	// Verify the token can be resolved by auth service.
	authSvc := auth.New(st, 100*time.Hour, 5, 15*time.Minute)
	resolvedUser, apiTok, err := authSvc.AuthenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("authenticate token failed: %v", err)
	}
	if resolvedUser.ID != u.ID || resolvedUser.Role != "admin" {
		t.Fatalf("unexpected user: %+v", resolvedUser)
	}
	if apiTok.Scope != "admin" {
		t.Fatalf("expected admin scope, got %s", apiTok.Scope)
	}
}

func TestDefaultTokenTTL(t *testing.T) {
	if got := defaultTokenTTL(); got != 8760 {
		t.Fatalf("expected default 8760, got %d", got)
	}

	t.Setenv("YUKI_ADMIN_TOKEN_TTL_HOURS", "4320")
	if got := defaultTokenTTL(); got != 4320 {
		t.Fatalf("expected overridden 4320, got %d", got)
	}

	t.Setenv("YUKI_ADMIN_TOKEN_TTL_HOURS", "invalid")
	if got := defaultTokenTTL(); got != 8760 {
		t.Fatalf("expected fallback 8760 on invalid env, got %d", got)
	}
}
