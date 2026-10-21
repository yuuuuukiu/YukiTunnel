package firewall

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ufwManager manages Ubuntu/Debian hosts.
//
// ufw already deduplicates identical rules, so AllowPort is idempotent. We add a
// comment so reconciliation can find our rules with `ufw status numbered`.
type ufwManager struct {
	det *Detector
	st  Status
}

func (m *ufwManager) Status() Status { return m.st }

func (m *ufwManager) AllowPort(ctx context.Context, r Rule) (Result, error) {
	if skip, reason := shouldSkip(m.st); skip {
		return Result{Skipped: true, Reason: reason, Backend: m.st.Type}, nil
	}
	proto := normalizeProto(r.Protocol)
	spec := fmt.Sprintf("%d/%s", r.Port, proto)
	comment := commentFor(r)
	out, errOut, err := m.det.Runner.Run(ctx, "ufw", "allow", spec, "comment", comment)
	if err != nil {
		// "Skipping adding existing rule" exits 0 on modern ufw, but older
		// versions warn; treat a permission error as a real failure.
		combined := strings.ToLower(out + errOut)
		if strings.Contains(combined, "permission") || strings.Contains(combined, "root") {
			return Result{}, fmt.Errorf("ufw 需要 root 权限：%s", strings.TrimSpace(errOut))
		}
		if strings.Contains(combined, "already exists") || strings.Contains(combined, "skipping") {
			return Result{Applied: false, Skipped: true, Reason: "规则已存在", Backend: m.st.Type}, nil
		}
		return Result{}, fmt.Errorf("ufw allow %s 失败：%s", spec, strings.TrimSpace(errOut))
	}
	return Result{Applied: true, Backend: m.st.Type}, nil
}

func (m *ufwManager) RevokePort(ctx context.Context, r Rule) (Result, error) {
	if skip, reason := shouldSkip(m.st); skip {
		return Result{Skipped: true, Reason: reason, Backend: m.st.Type}, nil
	}
	proto := normalizeProto(r.Protocol)
	spec := fmt.Sprintf("%d/%s", r.Port, proto)
	out, errOut, err := m.det.Runner.Run(ctx, "ufw", "--force", "delete", "allow", spec)
	if err != nil {
		combined := strings.ToLower(out + errOut)
		if strings.Contains(combined, "could not find") || strings.Contains(combined, "not found") {
			return Result{Skipped: true, Reason: "规则不存在", Backend: m.st.Type}, nil
		}
		return Result{}, fmt.Errorf("ufw delete %s 失败：%s", spec, strings.TrimSpace(errOut))
	}
	return Result{Applied: true, Backend: m.st.Type}, nil
}

// ListManaged parses `ufw status numbered` looking for our comment.
func (m *ufwManager) ListManaged(ctx context.Context) ([]Rule, error) {
	out, _, err := m.det.Runner.Run(ctx, "ufw", "status", "numbered")
	if err != nil {
		return nil, err
	}
	var rules []Rule
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "# "+Comment) {
			continue
		}
		// "[ 3] 20000/tcp  ALLOW  Anywhere  # yukitunnel"
		if r, ok := parseUFWRule(line); ok {
			rules = append(rules, r)
		}
	}
	return rules, nil
}

// parseUFWRule extracts the port and protocol from a numbered ufw line.
func parseUFWRule(line string) (Rule, bool) {
	idx := strings.Index(line, "]")
	if idx < 0 {
		return Rule{}, false
	}
	fields := strings.Fields(line[idx+1:])
	if len(fields) == 0 {
		return Rule{}, false
	}
	spec := fields[0]
	parts := strings.SplitN(spec, "/", 2)
	port, err := strconv.Atoi(parts[0])
	if err != nil {
		return Rule{}, false
	}
	proto := "tcp"
	if len(parts) == 2 {
		proto = parts[1]
	}
	return Rule{Port: port, Protocol: proto, Comment: Comment}, true
}

func commentFor(r Rule) string {
	if r.Comment != "" {
		return Comment + ":" + r.Comment
	}
	return Comment
}
