package firewall

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// firewalldManager manages RHEL/CentOS/Fedora hosts.
//
// Runtime changes are applied immediately; --permanent is added so the rule
// survives a reboot. firewalld treats re-adding the same port as a no-op with a
// warning, which we report as "already present".
type firewalldManager struct {
	det *Detector
	st  Status
}

func (m *firewalldManager) Status() Status { return m.st }

func (m *firewalldManager) AllowPort(ctx context.Context, r Rule) (Result, error) {
	if skip, reason := shouldSkip(m.st); skip {
		return Result{Skipped: true, Reason: reason, Backend: m.st.Type}, nil
	}
	proto := normalizeProto(r.Protocol)
	port := strconv.Itoa(r.Port)
	// Permanent first so a runtime failure does not leave a half-applied rule.
	if _, errOut, err := m.det.Runner.Run(ctx, "firewall-cmd", "--permanent", "--add-port", port+"/"+proto); err != nil {
		combined := strings.ToLower(errOut)
		if strings.Contains(combined, "already") {
			return Result{Skipped: true, Reason: "规则已存在", Backend: m.st.Type}, nil
		}
		return Result{}, fmt.Errorf("firewall-cmd --permanent --add-port %s/%s 失败：%s", port, proto, strings.TrimSpace(errOut))
	}
	if _, errOut, err := m.det.Runner.Run(ctx, "firewall-cmd", "--add-port", port+"/"+proto); err != nil {
		combined := strings.ToLower(errOut)
		if !strings.Contains(combined, "already") {
			return Result{}, fmt.Errorf("firewall-cmd --add-port %s/%s 失败：%s", port, proto, strings.TrimSpace(errOut))
		}
	}
	// Reload is only needed for other zones/consumers; runtime is already live.
	return Result{Applied: true, Backend: m.st.Type}, nil
}

func (m *firewalldManager) RevokePort(ctx context.Context, r Rule) (Result, error) {
	if skip, reason := shouldSkip(m.st); skip {
		return Result{Skipped: true, Reason: reason, Backend: m.st.Type}, nil
	}
	proto := normalizeProto(r.Protocol)
	port := strconv.Itoa(r.Port)
	_, permErr, permErrVal := m.det.Runner.Run(ctx, "firewall-cmd", "--permanent", "--remove-port", port+"/"+proto)
	_, runErr, runErrVal := m.det.Runner.Run(ctx, "firewall-cmd", "--remove-port", port+"/"+proto)
	if permErrVal != nil && runErrVal != nil {
		combined := strings.ToLower(permErr + runErr)
		if strings.Contains(combined, "not enabled") || strings.Contains(combined, "not found") {
			return Result{Skipped: true, Reason: "规则不存在", Backend: m.st.Type}, nil
		}
		return Result{}, fmt.Errorf("firewall-cmd --remove-port %s/%s 失败：%s", port, proto, strings.TrimSpace(permErr))
	}
	return Result{Applied: true, Backend: m.st.Type}, nil
}

// ListManaged reads permanent ports. firewalld has no comment field for ports,
// so every port in the managed range is considered ours; callers intersect with
// the tunnel table before deleting.
func (m *firewalldManager) ListManaged(ctx context.Context) ([]Rule, error) {
	out, _, err := m.det.Runner.Run(ctx, "firewall-cmd", "--permanent", "--list-ports")
	if err != nil {
		return nil, err
	}
	var rules []Rule
	for _, token := range strings.Fields(out) {
		parts := strings.SplitN(token, "/", 2)
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		proto := "tcp"
		if len(parts) == 2 {
			proto = parts[1]
		}
		rules = append(rules, Rule{Port: port, Protocol: proto, Comment: Comment})
	}
	return rules, nil
}
