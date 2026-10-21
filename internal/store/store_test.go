package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open("file:" + filepath.Join(t.TempDir(), "yuki.db") + "?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNodeLifecycle(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	n := &Node{
		ID: "n1", Name: "hk-1", Region: "hk", PublicHost: "1.2.3.4",
		ControlHost: "1.2.3.4", ControlPort: 9443, Enabled: true, MaxTunnels: 50,
	}
	if err := st.CreateNode(ctx, n); err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.Status != "offline" {
		t.Fatalf("default status should be offline, got %q", n.Status)
	}

	got, err := st.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "hk-1" || !got.Enabled || got.ControlPort != 9443 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	byName, err := st.GetNodeByName(ctx, "hk-1")
	if err != nil || byName.ID != "n1" {
		t.Fatalf("get by name: %v %+v", err, byName)
	}

	got.Enabled = false
	got.Region = "sg"
	if err := st.UpdateNode(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := st.GetNode(ctx, "n1")
	if got2.Enabled || got2.Region != "sg" {
		t.Fatalf("update not persisted: %+v", got2)
	}

	if err := st.SetNodeHealth(ctx, "n1", "online", 42); err != nil {
		t.Fatalf("health: %v", err)
	}
	got3, _ := st.GetNode(ctx, "n1")
	if got3.Status != "online" || got3.LatencyMS != 42 || got3.LastSeenAt.IsZero() {
		t.Fatalf("health not persisted: %+v", got3)
	}

	list, err := st.ListNodes(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	if err := st.DeleteNode(ctx, "n1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetNode(ctx, "n1"); err == nil {
		t.Fatal("expected not-found after delete")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	// Unknown keys return the caller's default instead of erroring.
	if v, err := st.GetSetting(ctx, "missing", "fallback"); err != nil || v != "fallback" {
		t.Fatalf("default: %v %q", err, v)
	}
	if err := st.SetSetting(ctx, "domain_binding", "true"); err != nil {
		t.Fatal(err)
	}
	if v, err := st.GetSetting(ctx, "domain_binding", ""); err != nil || v != "true" {
		t.Fatalf("get: %v %q", err, v)
	}
	if b, err := st.GetSettingBool(ctx, "domain_binding", false); err != nil || !b {
		t.Fatalf("bool: %v %v", err, b)
	}
	// Overwriting must not create a duplicate row.
	if err := st.SetSetting(ctx, "domain_binding", "false"); err != nil {
		t.Fatal(err)
	}
	if b, _ := st.GetSettingBool(ctx, "domain_binding", false); b {
		t.Fatal("setting not overwritten")
	}

	if err := st.SetSetting(ctx, "port_min", "20000"); err != nil {
		t.Fatal(err)
	}
	if n, err := st.GetSettingInt(ctx, "port_min", 0); err != nil || n != 20000 {
		t.Fatalf("int: %v %d", err, n)
	}
	// A malformed value surfaces as an error; callers such as
	// Service.PortRange are responsible for falling back to config.
	_ = st.SetSetting(ctx, "broken", "not-a-number")
	if _, err := st.GetSettingInt(ctx, "broken", 7); err == nil {
		t.Fatal("expected a parse error for a malformed setting")
	}
}

func TestAuditAndLoginAttempts(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := st.AddAudit(ctx, &AuditEntry{
			ID:      "a" + strconv.Itoa(i),
			ActorID: "u1", ActorName: "admin", Action: "tunnel.start",
			TargetType: "tunnel", TargetID: "tun_1",
			Detail: "port=20000", IP: "10.0.0.1",
		}); err != nil {
			t.Fatalf("audit: %v", err)
		}
	}
	entries, err := st.ListAudit(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].CreatedAt.IsZero() {
		t.Fatal("created_at not set")
	}
	// Newest first, so paging in the UI shows recent events.
	if entries[0].Action != "tunnel.start" {
		t.Fatalf("unexpected ordering: %+v", entries[0])
	}
	// Offset paging must not repeat rows.
	page2, err := st.ListAudit(ctx, 10, 2)
	if err != nil {
		t.Fatalf("paged audit: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("offset paging: want 1, got %d", len(page2))
	}

	for i := 0; i < 3; i++ {
		if err := st.AddLoginAttempt(ctx, "alice", "10.0.0.9", false); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.AddLoginAttempt(ctx, "alice", "10.0.0.9", true)
	fails, err := st.RecentFailures(ctx, "alice", "10.0.0.9", time.Hour)
	if err != nil {
		t.Fatalf("recent failures: %v", err)
	}
	if fails != 3 {
		t.Fatalf("want 3 failures, got %d", fails)
	}
	// A negative window means "everything before now", which is how the test
	// can prove rows are actually removed.
	if err := st.PruneLoginAttempts(ctx, -time.Minute); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n, _ := st.RecentFailures(ctx, "alice", "10.0.0.9", time.Hour); n != 0 {
		t.Fatalf("prune left %d rows", n)
	}
}

func TestIsAdmin(t *testing.T) {
	if !(&User{Role: "admin"}).IsAdmin() {
		t.Fatal("admin should be admin")
	}
	if (&User{Role: "user"}).IsAdmin() {
		t.Fatal("user must not be admin")
	}
}
