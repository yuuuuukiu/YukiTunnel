package firewall

import (
	"context"
	"fmt"
	"sort"
)

// ReconcileResult summarises one reconciliation pass.
type ReconcileResult struct {
	Backend Type     `json:"backend"`
	Skipped bool     `json:"skipped"`
	Reason  string   `json:"reason,omitempty"`
	Kept    []string `json:"kept,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Errors  []string `json:"errors,omitempty"`
}

// Reconciler removes firewall rules that YukiTunnel opened for tunnels that no
// longer exist (for example after a crash, or after a tunnel was deleted while
// the server was down).
type Reconciler struct {
	Manager Manager
}

// NewReconciler builds a reconciler for the given manager.
func NewReconciler(m Manager) *Reconciler { return &Reconciler{Manager: m} }

// Reconcile keeps exactly the rules whose Key() is in desired and removes the
// rest. Rules the operator added by hand are never touched because ListManaged
// only returns rules carrying our marker (or, for firewalld, ports we recorded).
func (r *Reconciler) Reconcile(ctx context.Context, desired []Rule) (ReconcileResult, error) {
	res := ReconcileResult{Backend: r.Manager.Status().Type}
	st := r.Manager.Status()
	if skip, reason := shouldSkip(st); skip {
		res.Skipped = true
		res.Reason = reason
		return res, nil
	}
	managed, err := r.Manager.ListManaged(ctx)
	if err != nil {
		return res, fmt.Errorf("列出已管理规则失败：%w", err)
	}
	want := map[string]bool{}
	for _, d := range desired {
		want[d.Key()] = true
	}
	seen := map[string]bool{}
	for _, m := range managed {
		key := m.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		if want[key] {
			res.Kept = append(res.Kept, key)
			continue
		}
		out, err := r.Manager.RevokePort(ctx, m)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		if out.Applied || out.Skipped {
			res.Removed = append(res.Removed, key)
		}
	}
	sort.Strings(res.Kept)
	sort.Strings(res.Removed)
	return res, nil
}
