package firewall

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner records commands and returns scripted output.
type fakeRunner struct {
	paths   map[string]bool
	outputs map[string]fakeOut
	calls   []string
}

type fakeOut struct {
	stdout string
	stderr string
	err    error
}

func newFake(paths ...string) *fakeRunner {
	f := &fakeRunner{paths: map[string]bool{}, outputs: map[string]fakeOut{}}
	for _, p := range paths {
		f.paths[p] = true
	}
	return f
}

func (f *fakeRunner) key(name string, args []string) string {
	return name + " " + strings.Join(args, " ")
}

func (f *fakeRunner) on(key, stdout string) *fakeRunner {
	f.outputs[key] = fakeOut{stdout: stdout}
	return f
}

func (f *fakeRunner) fail(key, stderr string) *fakeRunner {
	f.outputs[key] = fakeOut{stderr: stderr, err: errors.New("exit 1")}
	return f
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	k := f.key(name, args)
	f.calls = append(f.calls, k)
	if out, ok := f.outputs[k]; ok {
		return out.stdout, out.stderr, out.err
	}
	// Default: succeed silently (mutations).
	return "", "", nil
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.paths[name] {
		return "/usr/sbin/" + name, nil
	}
	return "", errors.New("not found")
}

func (f *fakeRunner) called(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func newTestDetector(r Runner) *Detector {
	return &Detector{Runner: r, GOOS: "linux", EUID: 0}
}

// ---- detection ----

func TestDetectUFWDeny(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip
`)
	d := newTestDetector(f)
	st := d.Detect(context.Background())
	if st.Type != TypeUFW || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	if st.DefaultInboundPolicy != PolicyDeny {
		t.Fatalf("policy = %s", st.DefaultInboundPolicy)
	}
	if !st.Managed {
		t.Fatal("ufw with deny + root should be managed")
	}
}

func TestDetectUFWAllowDefault(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: active\nDefault: allow (incoming), allow (outgoing)\n")
	st := newTestDetector(f).Detect(context.Background())
	if st.DefaultInboundPolicy != PolicyAllow {
		t.Fatalf("policy = %s", st.DefaultInboundPolicy)
	}
	if st.Managed {
		t.Fatal("allow-default must not be managed")
	}
}

func TestDetectUFWInactive(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: inactive\n")
	st := newTestDetector(f).Detect(context.Background())
	if st.Type != TypeUFW || st.Enabled {
		t.Fatalf("status = %+v", st)
	}
}

func TestDetectUFWWithoutRoot(t *testing.T) {
	f := newFake("ufw")
	f.fail("ufw status verbose", "ERROR: You need to be root to run this script")
	d := &Detector{Runner: f, GOOS: "linux", EUID: 1000}
	st := d.Detect(context.Background())
	if st.Type != TypeUFW || !st.Available {
		t.Fatalf("status = %+v", st)
	}
	if st.Managed {
		t.Fatal("non-root must not be managed")
	}
	if !strings.Contains(st.Detail, "root") {
		t.Fatalf("detail should mention root: %s", st.Detail)
	}
}

func TestDetectFirewalldRunning(t *testing.T) {
	f := newFake("firewall-cmd")
	f.on("firewall-cmd --state", "running\n")
	f.on("firewall-cmd --permanent --get-target", "DROP\n")
	f.on("firewall-cmd --get-default-zone", "public\n")
	st := newTestDetector(f).Detect(context.Background())
	if st.Type != TypeFirewalld || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	if st.DefaultInboundPolicy != PolicyDeny {
		t.Fatalf("policy = %s", st.DefaultInboundPolicy)
	}
}

func TestDetectFirewalldStopped(t *testing.T) {
	f := newFake("firewall-cmd")
	f.on("firewall-cmd --state", "not running\n")
	st := newTestDetector(f).Detect(context.Background())
	if st.Type != TypeFirewalld || st.Enabled {
		t.Fatalf("status = %+v", st)
	}
}

func TestDetectNftables(t *testing.T) {
	f := newFake("nft")
	f.on("nft list chains", "table inet filter {\n\tchain input {\n\t}\n}\n")
	f.on("nft list chain inet filter input", "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority 0; policy drop;\n\t}\n}\n")
	st := newTestDetector(f).Detect(context.Background())
	if st.Type != TypeNftables || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	if st.DefaultInboundPolicy != PolicyDeny {
		t.Fatalf("policy = %s", st.DefaultInboundPolicy)
	}
}

func TestDetectNone(t *testing.T) {
	st := newTestDetector(newFake()).Detect(context.Background())
	if st.Type != TypeNone {
		t.Fatalf("status = %+v", st)
	}
}

func TestDetectUnsupportedPlatforms(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		d := &Detector{Runner: newFake(), GOOS: goos, EUID: 0}
		st := d.Detect(context.Background())
		if st.Type != TypeUnsupported {
			t.Fatalf("%s: status = %+v", goos, st)
		}
		if st.Managed {
			t.Fatalf("%s: unsupported must not be managed", goos)
		}
		m := NewManager(d, st)
		res, err := m.AllowPort(context.Background(), Rule{Port: 20000, Protocol: "tcp"})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Skipped {
			t.Fatalf("%s: expected skipped, got %+v", goos, res)
		}
	}
}

// ---- skip conditions ----

func TestSkipWhenDisabled(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: inactive\n")
	d := newTestDetector(f)
	st := d.Detect(context.Background())
	m := NewManager(d, st)
	res, err := m.AllowPort(context.Background(), Rule{Port: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || !strings.Contains(res.Reason, "未启用") {
		t.Fatalf("result = %+v", res)
	}
	if f.called("ufw allow") {
		t.Fatal("must not run ufw allow when disabled")
	}
}

func TestSkipWhenDefaultAllow(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: active\nDefault: allow (incoming), allow (outgoing)\n")
	d := newTestDetector(f)
	st := d.Detect(context.Background())
	res, err := NewManager(d, st).AllowPort(context.Background(), Rule{Port: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("result = %+v", res)
	}
	if f.called("ufw allow") {
		t.Fatal("must not open a port when inbound is already allowed")
	}
}

func TestSkipWhenPolicyUnknown(t *testing.T) {
	st := Status{Type: TypeUFW, Available: true, Enabled: true, DefaultInboundPolicy: PolicyUnknown}
	res, err := NewManager(newTestDetector(newFake()), st).AllowPort(context.Background(), Rule{Port: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("unknown policy must be skipped: %+v", res)
	}
}

// ---- allow / revoke ----

func TestUFWAllowAndRevoke(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: active\nDefault: deny (incoming), allow (outgoing)\n")
	d := newTestDetector(f)
	m := NewManager(d, d.Detect(context.Background()))

	res, err := m.AllowPort(context.Background(), Rule{Port: 20001, Protocol: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied {
		t.Fatalf("result = %+v", res)
	}
	if !f.called("ufw allow 20001/tcp comment yukitunnel") {
		t.Fatalf("unexpected calls: %v", f.calls)
	}

	res, err = m.RevokePort(context.Background(), Rule{Port: 20001, Protocol: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied {
		t.Fatalf("result = %+v", res)
	}
	if !f.called("ufw --force delete allow 20001/tcp") {
		t.Fatalf("unexpected calls: %v", f.calls)
	}
}

func TestUFWAlreadyExistsIsSkipped(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: active\nDefault: deny (incoming)\n")
	f.fail("ufw allow 20001/tcp comment yukitunnel", "Skipping adding existing rule")
	d := newTestDetector(f)
	res, err := NewManager(d, d.Detect(context.Background())).AllowPort(context.Background(), Rule{Port: 20001})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("result = %+v", res)
	}
}

func TestUFWPermissionDeniedIsError(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: active\nDefault: deny (incoming)\n")
	f.fail("ufw allow 20001/tcp comment yukitunnel", "ERROR: You need to be root")
	d := newTestDetector(f)
	_, err := NewManager(d, d.Detect(context.Background())).AllowPort(context.Background(), Rule{Port: 20001})
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("expected root error, got %v", err)
	}
}

func TestFirewalldAllowAndRevoke(t *testing.T) {
	f := newFake("firewall-cmd")
	f.on("firewall-cmd --state", "running\n")
	f.on("firewall-cmd --permanent --get-target", "DROP\n")
	f.on("firewall-cmd --get-default-zone", "public\n")
	d := newTestDetector(f)
	m := NewManager(d, d.Detect(context.Background()))

	if res, err := m.AllowPort(context.Background(), Rule{Port: 20002, Protocol: "udp"}); err != nil || !res.Applied {
		t.Fatalf("allow = %+v err=%v", res, err)
	}
	if !f.called("firewall-cmd --permanent --add-port 20002/udp") {
		t.Fatalf("permanent add missing: %v", f.calls)
	}
	if !f.called("firewall-cmd --add-port 20002/udp") {
		t.Fatalf("runtime add missing: %v", f.calls)
	}
	if res, err := m.RevokePort(context.Background(), Rule{Port: 20002, Protocol: "udp"}); err != nil || !res.Applied {
		t.Fatalf("revoke = %+v err=%v", res, err)
	}
}

func TestNftablesAllowIsIdempotent(t *testing.T) {
	f := newFake("nft")
	f.on("nft list chains", "table inet filter { chain input { } }\n")
	f.on("nft list chain inet filter input", "policy drop;\n")
	f.on("nft list table inet yukitunnel", "table inet yukitunnel {\n}\n")
	f.on("nft list chain inet yukitunnel input", "tcp dport 20003 accept\n")
	d := newTestDetector(f)
	m := NewManager(d, d.Detect(context.Background()))
	res, err := m.AllowPort(context.Background(), Rule{Port: 20003, Protocol: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("existing nft rule must be skipped: %+v", res)
	}
	if f.called("nft add rule") {
		t.Fatal("must not duplicate an existing nft rule")
	}
}

func TestNftablesCreatesChainWhenMissing(t *testing.T) {
	f := newFake("nft")
	f.on("nft list chains", "table inet filter { chain input { } }\n")
	f.on("nft list chain inet filter input", "policy drop;\n")
	f.fail("nft list table inet yukitunnel", "No such file or directory")
	d := newTestDetector(f)
	m := NewManager(d, d.Detect(context.Background()))
	if _, err := m.AllowPort(context.Background(), Rule{Port: 20004}); err != nil {
		t.Fatal(err)
	}
	if !f.called("nft add table inet yukitunnel") {
		t.Fatalf("table creation missing: %v", f.calls)
	}
	if !f.called("nft add chain inet yukitunnel input") {
		t.Fatalf("chain creation missing: %v", f.calls)
	}
}

// ---- reconcile ----

func TestReconcileRemovesOrphans(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: active\nDefault: deny (incoming)\n")
	f.on("ufw status numbered", `Status: active
     To                         Action      From
     --                         ------      ----
[ 1] 20000/tcp                  ALLOW IN    Anywhere                   # yukitunnel
[ 2] 20001/tcp                  ALLOW IN    Anywhere                   # yukitunnel
[ 3] 22/tcp                     ALLOW IN    Anywhere
[ 4] 20002/udp                  ALLOW IN    Anywhere                   # yukitunnel
`)
	d := newTestDetector(f)
	m := NewManager(d, d.Detect(context.Background()))
	rec := NewReconciler(m)

	res, err := rec.Reconcile(context.Background(), []Rule{{Port: 20000, Protocol: "tcp"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Kept) != 1 || res.Kept[0] != "20000/tcp" {
		t.Fatalf("kept = %v", res.Kept)
	}
	if len(res.Removed) != 2 {
		t.Fatalf("removed = %v (want 20001/tcp and 20002/udp)", res.Removed)
	}
	for _, want := range []string{"20001/tcp", "20002/udp"} {
		found := false
		for _, r := range res.Removed {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s should be removed, got %v", want, res.Removed)
		}
	}
	// The operator's SSH rule must never be touched.
	if f.called("22/tcp") {
		t.Fatalf("must not touch operator rules: %v", f.calls)
	}
}

func TestReconcileSkippedWhenNotManaged(t *testing.T) {
	f := newFake("ufw")
	f.on("ufw status verbose", "Status: active\nDefault: allow (incoming)\n")
	d := newTestDetector(f)
	res, err := NewReconciler(NewManager(d, d.Detect(context.Background()))).
		Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("expected skipped, got %+v", res)
	}
	if f.called("ufw status numbered") {
		t.Fatal("must not enumerate rules when skipped")
	}
}

func TestParseUFWRule(t *testing.T) {
	r, ok := parseUFWRule("[ 3] 20000/tcp                  ALLOW IN    Anywhere                   # yukitunnel")
	if !ok || r.Port != 20000 || r.Protocol != "tcp" {
		t.Fatalf("parsed = %+v ok=%v", r, ok)
	}
	if _, ok := parseUFWRule("garbage"); ok {
		t.Fatal("garbage should not parse")
	}
}

func TestFindNFTHandle(t *testing.T) {
	out := `table inet yukitunnel {
	chain input {
		tcp dport 20000 accept comment "yukitunnel" # handle 7
		udp dport 20001 accept comment "yukitunnel" # handle 9
	}
}`
	if got := findNFTHandle(out, "tcp", "20000"); got != "7" {
		t.Fatalf("handle = %q", got)
	}
	if got := findNFTHandle(out, "udp", "20001"); got != "9" {
		t.Fatalf("handle = %q", got)
	}
	if got := findNFTHandle(out, "tcp", "9999"); got != "" {
		t.Fatalf("unexpected handle %q", got)
	}
}

func TestRuleKeyNormalisation(t *testing.T) {
	if (Rule{Port: 80, Protocol: "TCP"}).Key() != "80/tcp" {
		t.Fatal("protocol should be normalised to lowercase")
	}
	if (Rule{Port: 80}).Key() != "80/tcp" {
		t.Fatal("empty protocol should default to tcp")
	}
}
