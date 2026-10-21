// Package firewall adapts YukiTunnel to the host's packet filter so that the
// public ports it opens for tunnels are actually reachable.
//
// Scope: this package only manages the *local* host firewall (ufw, firewalld,
// nftables). Cloud security groups are handled by the profile registry.
package firewall

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Type identifies the detected firewall backend.
type Type string

const (
	// TypeUFW is Ubuntu/Debian's Uncomplicated Firewall (iptables frontend).
	TypeUFW Type = "ufw"
	// TypeFirewalld is the RHEL/CentOS/Fedora default (nftables backend).
	TypeFirewalld Type = "firewalld"
	// TypeNftables is a bare nftables ruleset with no frontend.
	TypeNftables Type = "nftables"
	// TypeNone means no supported firewall was found.
	TypeNone Type = "none"
	// TypeUnsupported is returned on Windows and macOS.
	TypeUnsupported Type = "unsupported"
)

// Inbound policies reported by the detector.
const (
	PolicyDeny    = "deny"
	PolicyAllow   = "allow"
	PolicyUnknown = "unknown"
)

// Status is the detection result.
type Status struct {
	Type                 Type   `json:"type"`
	Available            bool   `json:"available"`
	Enabled              bool   `json:"enabled"`
	DefaultInboundPolicy string `json:"default_inbound_policy"`
	Detail               string `json:"detail"`
	// Managed reports whether YukiTunnel may modify this firewall.
	Managed bool `json:"managed"`
}

// Reason explains why a change was or was not applied.
func (s Status) Reason() string {
	switch {
	case s.Type == TypeUnsupported:
		return fmt.Sprintf("当前平台（%s/%s）不支持系统防火墙自动放行", runtime.GOOS, runtime.GOARCH)
	case s.Type == TypeNone:
		return "未检测到受支持的防火墙（ufw / firewalld / nftables）"
	case !s.Enabled:
		return "防火墙已安装但未启用，无需放行"
	case s.DefaultInboundPolicy == PolicyAllow:
		return "防火墙入站默认策略为 allow，无需逐端口放行"
	case s.DefaultInboundPolicy == PolicyUnknown:
		return "无法确定入站默认策略，为避免误改已跳过"
	default:
		return ""
	}
}

// Runner abstracts command execution so tests can use a fake.
type Runner interface {
	// Run executes name with args and returns stdout, stderr and the error.
	Run(ctx context.Context, name string, args ...string) (string, string, error)
	// LookPath reports whether an executable exists.
	LookPath(name string) (string, error)
}

// ExecRunner runs real commands.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// LookPath implements Runner.
func (ExecRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Detector inspects the host.
type Detector struct {
	Runner Runner
	// GOOS overrides runtime.GOOS in tests.
	GOOS string
	// EUID overrides os.Geteuid in tests.
	EUID int
}

// NewDetector returns a detector backed by real command execution.
func NewDetector() *Detector {
	return &Detector{Runner: ExecRunner{}, GOOS: runtime.GOOS, EUID: currentEUID()}
}

// Detect inspects the host and returns its firewall status.
func (d *Detector) Detect(ctx context.Context) Status {
	if d.Runner == nil {
		d.Runner = ExecRunner{}
	}
	goos := d.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	switch goos {
	case "windows", "darwin":
		return Status{
			Type: TypeUnsupported, Available: false, Enabled: false,
			DefaultInboundPolicy: PolicyUnknown,
			Detail:               "该平台请使用系统自带防火墙界面或企业策略管理端口",
		}
	}

	if _, err := d.Runner.LookPath("ufw"); err == nil {
		if st, ok := d.detectUFW(ctx); ok {
			return st
		}
	}
	if _, err := d.Runner.LookPath("firewall-cmd"); err == nil {
		if st, ok := d.detectFirewalld(ctx); ok {
			return st
		}
	}
	if _, err := d.Runner.LookPath("nft"); err == nil {
		if st, ok := d.detectNftables(ctx); ok {
			return st
		}
	}
	return Status{
		Type: TypeNone, Available: false, Enabled: false,
		DefaultInboundPolicy: PolicyUnknown,
		Detail:               "未找到 ufw、firewall-cmd 或 nft",
	}
}

func (d *Detector) privileged() bool {
	return d.EUID == 0
}

// detectUFW parses `ufw status verbose`.
func (d *Detector) detectUFW(ctx context.Context) (Status, bool) {
	out, errOut, err := d.Runner.Run(ctx, "ufw", "status", "verbose")
	if err != nil {
		// ufw exists but needs root; still report it so doctor can explain.
		if strings.Contains(strings.ToLower(errOut), "permission") || strings.Contains(strings.ToLower(errOut), "root") {
			return Status{
				Type: TypeUFW, Available: true, Enabled: true,
				DefaultInboundPolicy: PolicyUnknown,
				Detail:               "需要 root 权限读取 ufw 状态（请用 sudo 运行或配置 capabilities）",
			}, true
		}
		return Status{}, false
	}
	st := Status{Type: TypeUFW, Available: true, DefaultInboundPolicy: PolicyUnknown}
	lower := strings.ToLower(out)
	st.Enabled = strings.Contains(lower, "status: active")
	if !st.Enabled {
		st.Detail = "ufw 未启用"
		return st, true
	}
	// "Default: deny (incoming), allow (outgoing), disabled (routed)"
	for _, line := range strings.Split(out, "\n") {
		l := strings.ToLower(line)
		if strings.HasPrefix(strings.TrimSpace(l), "default:") {
			switch {
			case strings.Contains(l, "deny (incoming)"):
				st.DefaultInboundPolicy = PolicyDeny
			case strings.Contains(l, "allow (incoming)"):
				st.DefaultInboundPolicy = PolicyAllow
			case strings.Contains(l, "reject (incoming)"):
				st.DefaultInboundPolicy = PolicyDeny
			}
		}
	}
	st.Managed = st.DefaultInboundPolicy == PolicyDeny && d.privileged()
	if st.DefaultInboundPolicy == PolicyDeny && !d.privileged() {
		st.Detail = "入站默认 deny，但当前进程非 root，无法自动放行"
	} else {
		st.Detail = fmt.Sprintf("入站默认策略 %s", st.DefaultInboundPolicy)
	}
	return st, true
}

// detectFirewalld parses `firewall-cmd --state` and the default zone target.
func (d *Detector) detectFirewalld(ctx context.Context) (Status, bool) {
	out, _, err := d.Runner.Run(ctx, "firewall-cmd", "--state")
	// `--state` prints exactly "running" or "not running"; a substring check
	// would treat "not running" as running.
	state := strings.ToLower(strings.TrimSpace(out))
	if err != nil || state != "running" {
		// Installed but stopped: report it so doctor can suggest enabling.
		if _, lerr := d.Runner.LookPath("firewall-cmd"); lerr == nil {
			return Status{
				Type: TypeFirewalld, Available: true, Enabled: false,
				DefaultInboundPolicy: PolicyUnknown, Detail: "firewalld 未运行",
			}, true
		}
		return Status{}, false
	}
	st := Status{Type: TypeFirewalld, Available: true, Enabled: true, DefaultInboundPolicy: PolicyUnknown}
	// The default zone's target decides whether inbound is filtered.
	target, _, terr := d.Runner.Run(ctx, "firewall-cmd", "--permanent", "--get-target")
	zone, _, _ := d.Runner.Run(ctx, "firewall-cmd", "--get-default-zone")
	if terr == nil {
		switch strings.ToLower(strings.TrimSpace(target)) {
		case "drop", "reject", "%%reject%%":
			st.DefaultInboundPolicy = PolicyDeny
		case "accept":
			st.DefaultInboundPolicy = PolicyAllow
		case "default":
			// "default" means the zone's services decide; treat as deny for
			// ports not explicitly opened.
			st.DefaultInboundPolicy = PolicyDeny
		}
	}
	st.Managed = st.DefaultInboundPolicy == PolicyDeny && d.privileged()
	st.Detail = fmt.Sprintf("默认区域 %s，target=%s，入站策略 %s",
		strings.TrimSpace(zone), strings.TrimSpace(target), st.DefaultInboundPolicy)
	return st, true
}

// detectNftables looks for an inet filter input chain.
func (d *Detector) detectNftables(ctx context.Context) (Status, bool) {
	out, _, err := d.Runner.Run(ctx, "nft", "list", "chains")
	if err != nil {
		return Status{}, false
	}
	if !strings.Contains(out, "inet filter") {
		return Status{
			Type: TypeNftables, Available: true, Enabled: false,
			DefaultInboundPolicy: PolicyUnknown,
			Detail:               "nftables 存在但没有 inet filter 表",
		}, true
	}
	st := Status{Type: TypeNftables, Available: true, Enabled: true, DefaultInboundPolicy: PolicyUnknown}
	// A `policy drop` on the input chain is the common "deny inbound" setup.
	chain, _, cerr := d.Runner.Run(ctx, "nft", "list", "chain", "inet", "filter", "input")
	if cerr == nil {
		lower := strings.ToLower(chain)
		switch {
		case strings.Contains(lower, "policy drop"):
			st.DefaultInboundPolicy = PolicyDeny
		case strings.Contains(lower, "policy accept"):
			st.DefaultInboundPolicy = PolicyAllow
		}
	}
	st.Managed = st.DefaultInboundPolicy == PolicyDeny && d.privileged()
	st.Detail = fmt.Sprintf("inet filter input，入站策略 %s", st.DefaultInboundPolicy)
	if st.DefaultInboundPolicy == PolicyDeny && !d.privileged() {
		st.Detail += "（需要 root 才能修改）"
	}
	return st, true
}
