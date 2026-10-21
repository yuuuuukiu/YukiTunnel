package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Service is the integration point used by the tunnel server. It caches the
// detection result, serialises mutations (firewalls are not concurrency-safe),
// and records every decision for the audit log.
type Service struct {
	mu       sync.Mutex
	detector *Detector
	manager  Manager
	status   Status
	log      *slog.Logger
	// OnEvent receives every allow/revoke/skip for audit logging.
	OnEvent func(Event)
	// refreshAfter bounds how often detection is re-run.
	refreshAfter time.Duration
	lastDetect   time.Time
	// disabled turns the whole adapter off (YUKI_FIREWALL=off).
	disabled bool
}

// Event describes one firewall interaction.
type Event struct {
	At      time.Time `json:"at"`
	Action  string    `json:"action"` // allow | revoke | reconcile | detect
	Port    int       `json:"port,omitempty"`
	Proto   string    `json:"proto,omitempty"`
	Backend Type      `json:"backend"`
	Applied bool      `json:"applied"`
	Skipped bool      `json:"skipped"`
	Reason  string    `json:"reason,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// NewService builds a firewall service.
func NewService(log *slog.Logger, disabled bool) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		detector:     NewDetector(),
		log:          log,
		disabled:     disabled,
		refreshAfter: 30 * time.Second,
	}
}

// NewServiceWithRunner is the test seam: it injects a runner and detection.
func NewServiceWithRunner(r Runner, goos string, euid int, log *slog.Logger) *Service {
	s := NewService(log, false)
	s.detector = &Detector{Runner: r, GOOS: goos, EUID: euid}
	s.status = s.detector.Detect(context.Background())
	s.manager = NewManager(s.detector, s.status)
	s.lastDetect = time.Now()
	return s
}

// Status returns the cached detection result, refreshing it periodically.
func (s *Service) Status(ctx context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked(ctx)
}

func (s *Service) statusLocked(ctx context.Context) Status {
	if s.disabled {
		return Status{
			Type: TypeNone, Available: false, Enabled: false,
			DefaultInboundPolicy: PolicyUnknown,
			Detail:               "系统防火墙自动放行已通过 YUKI_FIREWALL=off 关闭",
		}
	}
	if s.manager != nil && time.Since(s.lastDetect) < s.refreshAfter {
		return s.status
	}
	s.status = s.detector.Detect(ctx)
	s.manager = NewManager(s.detector, s.status)
	s.lastDetect = time.Now()
	return s.status
}

// AllowPort opens a port and reports what happened.
func (s *Service) AllowPort(ctx context.Context, port int, proto string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statusLocked(ctx)
	if s.manager == nil {
		return Result{Skipped: true, Reason: st.Reason(), Backend: st.Type}, nil
	}
	res, err := s.manager.AllowPort(ctx, Rule{Port: port, Protocol: proto})
	s.emit(Event{Action: "allow", Port: port, Proto: normalizeProto(proto), Backend: st.Type,
		Applied: res.Applied, Skipped: res.Skipped, Reason: res.Reason, Error: errString(err)})
	return res, err
}

// RevokePort closes a port.
func (s *Service) RevokePort(ctx context.Context, port int, proto string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statusLocked(ctx)
	if s.manager == nil {
		return Result{Skipped: true, Reason: st.Reason(), Backend: st.Type}, nil
	}
	res, err := s.manager.RevokePort(ctx, Rule{Port: port, Protocol: proto})
	s.emit(Event{Action: "revoke", Port: port, Proto: normalizeProto(proto), Backend: st.Type,
		Applied: res.Applied, Skipped: res.Skipped, Reason: res.Reason, Error: errString(err)})
	return res, err
}

// Reconcile removes rules for ports that are no longer used by any tunnel.
func (s *Service) Reconcile(ctx context.Context, desired []Rule) (ReconcileResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statusLocked(ctx)
	if s.manager == nil {
		return ReconcileResult{Skipped: true, Reason: st.Reason(), Backend: st.Type}, nil
	}
	res, err := NewReconciler(s.manager).Reconcile(ctx, desired)
	s.emit(Event{Action: "reconcile", Backend: st.Type,
		Applied: len(res.Removed) > 0, Skipped: res.Skipped, Reason: res.Reason, Error: errString(err)})
	return res, err
}

// Summary renders a one-line description for `yuki-server doctor`.
func (s *Service) Summary(ctx context.Context) string {
	st := s.Status(ctx)
	if st.Type == TypeUnsupported || st.Type == TypeNone {
		return fmt.Sprintf("%s（%s）", st.Type, st.Detail)
	}
	state := "未启用"
	if st.Enabled {
		state = "已启用"
	}
	managed := "不自动放行"
	if st.Managed {
		managed = "自动放行"
	}
	return fmt.Sprintf("%s %s，入站默认 %s，%s；%s", st.Type, state, st.DefaultInboundPolicy, managed, st.Detail)
}

func (s *Service) emit(e Event) {
	e.At = time.Now().UTC()
	if e.Error != "" {
		s.log.Warn("firewall", "action", e.Action, "port", e.Port, "proto", e.Proto,
			"backend", e.Backend, "error", e.Error)
	} else {
		s.log.Debug("firewall", "action", e.Action, "port", e.Port, "proto", e.Proto,
			"backend", e.Backend, "applied", e.Applied, "skipped", e.Skipped, "reason", e.Reason)
	}
	if s.OnEvent != nil {
		s.OnEvent(e)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
