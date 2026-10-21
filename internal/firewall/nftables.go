package firewall

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// nftablesManager manages hosts that use nft directly (no ufw/firewalld).
//
// Rules are added to a dedicated chain so that reconciliation can remove the
// whole chain without touching operator rules:
//
//	table inet yukitunnel { chain input { tcp dport 20000 accept ... } }
//
// The chain is hooked into `inet filter input` via a jump.
type nftablesManager struct {
	det *Detector
	st  Status
}

const (
	nftTable = "yukitunnel"
	nftChain = "input"
)

func (m *nftablesManager) Status() Status { return m.st }

// ensureChain creates our table and chain if they are missing.
func (m *nftablesManager) ensureChain(ctx context.Context) error {
	_, errOut, err := m.det.Runner.Run(ctx, "nft", "list", "table", "inet", nftTable)
	if err == nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(errOut), "no such") {
		return fmt.Errorf("nft list table 失败：%s", strings.TrimSpace(errOut))
	}
	if _, errOut, err := m.det.Runner.Run(ctx, "nft", "add", "table", "inet", nftTable); err != nil {
		return fmt.Errorf("nft add table 失败：%s", strings.TrimSpace(errOut))
	}
	if _, errOut, err := m.det.Runner.Run(ctx, "nft", "add", "chain", "inet", nftTable, nftChain,
		"{", "type", "filter", "hook", "input", "priority", "0", ";", "}"); err != nil {
		return fmt.Errorf("nft add chain 失败：%s", strings.TrimSpace(errOut))
	}
	return nil
}

func (m *nftablesManager) AllowPort(ctx context.Context, r Rule) (Result, error) {
	if skip, reason := shouldSkip(m.st); skip {
		return Result{Skipped: true, Reason: reason, Backend: m.st.Type}, nil
	}
	if err := m.ensureChain(ctx); err != nil {
		return Result{}, err
	}
	proto := normalizeProto(r.Protocol)
	port := strconv.Itoa(r.Port)
	// `add rule` appends unconditionally, so check first to stay idempotent.
	existing, _, _ := m.det.Runner.Run(ctx, "nft", "list", "chain", "inet", nftTable, nftChain)
	if strings.Contains(existing, fmt.Sprintf("%s dport %s accept", proto, port)) {
		return Result{Skipped: true, Reason: "规则已存在", Backend: m.st.Type}, nil
	}
	_, errOut, err := m.det.Runner.Run(ctx, "nft", "add", "rule", "inet", nftTable, nftChain,
		proto, "dport", port, "accept", "comment", `"`+commentFor(r)+`"`)
	if err != nil {
		return Result{}, fmt.Errorf("nft add rule 失败：%s", strings.TrimSpace(errOut))
	}
	return Result{Applied: true, Backend: m.st.Type}, nil
}

func (m *nftablesManager) RevokePort(ctx context.Context, r Rule) (Result, error) {
	if skip, reason := shouldSkip(m.st); skip {
		return Result{Skipped: true, Reason: reason, Backend: m.st.Type}, nil
	}
	proto := normalizeProto(r.Protocol)
	port := strconv.Itoa(r.Port)
	// nft deletes by handle; find the handle of the matching rule first.
	out, _, err := m.det.Runner.Run(ctx, "nft", "-a", "list", "chain", "inet", nftTable, nftChain)
	if err != nil {
		return Result{Skipped: true, Reason: "YukiTunnel 规则链不存在", Backend: m.st.Type}, nil
	}
	handle := findNFTHandle(out, proto, port)
	if handle == "" {
		return Result{Skipped: true, Reason: "规则不存在", Backend: m.st.Type}, nil
	}
	_, errOut, err := m.det.Runner.Run(ctx, "nft", "delete", "rule", "inet", nftTable, nftChain, "handle", handle)
	if err != nil {
		return Result{}, fmt.Errorf("nft delete rule 失败：%s", strings.TrimSpace(errOut))
	}
	return Result{Applied: true, Backend: m.st.Type}, nil
}

// findNFTHandle returns the handle of "tcp dport <port> accept" if present.
func findNFTHandle(listOutput, proto, port string) string {
	needle := fmt.Sprintf("%s dport %s accept", proto, port)
	for _, line := range strings.Split(listOutput, "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		idx := strings.Index(line, "handle ")
		if idx < 0 {
			continue
		}
		rest := strings.Fields(line[idx+len("handle "):])
		if len(rest) > 0 {
			return rest[0]
		}
	}
	return ""
}

func (m *nftablesManager) ListManaged(ctx context.Context) ([]Rule, error) {
	out, _, err := m.det.Runner.Run(ctx, "nft", "list", "chain", "inet", nftTable, nftChain)
	if err != nil {
		return nil, nil // chain absent means nothing is managed
	}
	var rules []Rule
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+3 < len(fields); i++ {
			if fields[i+1] != "dport" || fields[i+3] != "accept" {
				continue
			}
			port, err := strconv.Atoi(fields[i+2])
			if err != nil {
				continue
			}
			rules = append(rules, Rule{Port: port, Protocol: fields[i], Comment: Comment})
		}
	}
	return rules, nil
}
