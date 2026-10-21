package firewall

import (
	"context"
	"fmt"
	"strings"
)

// Comment is embedded in every rule YukiTunnel creates. Reconciliation uses it
// to tell our rules apart from rules an operator added by hand.
const Comment = "yukitunnel"

// Rule is one port to open or close.
type Rule struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Comment  string `json:"comment,omitempty"`
}

// Key returns a stable identifier for de-duplication.
func (r Rule) Key() string { return fmt.Sprintf("%d/%s", r.Port, normalizeProto(r.Protocol)) }

func normalizeProto(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "udp":
		return "udp"
	default:
		return "tcp"
	}
}

// Result reports what happened.
type Result struct {
	Applied bool   `json:"applied"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason,omitempty"`
	Backend Type   `json:"backend,omitempty"`
}

// Manager is implemented by each backend.
type Manager interface {
	// Status returns the current detection result.
	Status() Status
	// AllowPort opens a port.
	AllowPort(ctx context.Context, r Rule) (Result, error)
	// RevokePort closes a port.
	RevokePort(ctx context.Context, r Rule) (Result, error)
	// ListManaged returns rules that carry our comment marker.
	ListManaged(ctx context.Context) ([]Rule, error)
}

// NewManager picks the backend for a detection result.
//
// An unsupported or disabled firewall yields a manager whose operations are all
// Skipped, which keeps callers free of nil checks.
func NewManager(det *Detector, st Status) Manager {
	switch st.Type {
	case TypeUFW:
		return &ufwManager{det: det, st: st}
	case TypeFirewalld:
		return &firewalldManager{det: det, st: st}
	case TypeNftables:
		return &nftablesManager{det: det, st: st}
	default:
		return &noopManager{st: st}
	}
}

// noopManager implements Manager for unsupported/none/allow-default cases.
type noopManager struct{ st Status }

func (m *noopManager) Status() Status { return m.st }

func (m *noopManager) AllowPort(ctx context.Context, r Rule) (Result, error) {
	return Result{Skipped: true, Reason: m.st.Reason(), Backend: m.st.Type}, nil
}

func (m *noopManager) RevokePort(ctx context.Context, r Rule) (Result, error) {
	return Result{Skipped: true, Reason: m.st.Reason(), Backend: m.st.Type}, nil
}

func (m *noopManager) ListManaged(ctx context.Context) ([]Rule, error) { return nil, nil }

// shouldSkip reports whether a mutation must be skipped.
func shouldSkip(st Status) (bool, string) {
	if st.Type == TypeUnsupported || st.Type == TypeNone {
		return true, st.Reason()
	}
	if !st.Enabled {
		return true, st.Reason()
	}
	if st.DefaultInboundPolicy != PolicyDeny {
		return true, st.Reason()
	}
	return false, ""
}
