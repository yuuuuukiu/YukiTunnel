package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const tunnelCols = `id, user_id, node_id, name, proto, local_ip, local_port, public_port, domain, enabled,
	bandwidth_kbps, max_connections, desired_running, created_at, updated_at, last_started_at`

func scanTunnel(sc interface{ Scan(...any) error }) (*Tunnel, error) {
	var t Tunnel
	var enabled, desired int
	var created, updated, lastStarted int64
	err := sc.Scan(&t.ID, &t.UserID, &t.NodeID, &t.Name, &t.Proto, &t.LocalIP, &t.LocalPort,
		&t.PublicPort, &t.Domain, &enabled, &t.BandwidthKbps, &t.MaxConnections, &desired,
		&created, &updated, &lastStarted)
	if err != nil {
		return nil, err
	}
	t.Enabled = enabled != 0
	t.DesiredRunning = desired != 0
	t.CreatedAt, t.UpdatedAt, t.LastStartedAt = fromTS(created), fromTS(updated), fromTS(lastStarted)
	return &t, nil
}

// CreateTunnel inserts a tunnel.
func (s *Store) CreateTunnel(ctx context.Context, t *Tunnel) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO tunnels(`+tunnelCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.UserID, t.NodeID, t.Name, t.Proto, t.LocalIP, t.LocalPort, t.PublicPort, t.Domain,
		boolInt(t.Enabled), t.BandwidthKbps, t.MaxConnections, boolInt(t.DesiredRunning),
		ts(t.CreatedAt), ts(t.UpdatedAt), ts(t.LastStartedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrConflict
	}
	return err
}

// UpdateTunnel persists editable tunnel fields.
func (s *Store) UpdateTunnel(ctx context.Context, t *Tunnel) error {
	res, err := s.db.ExecContext(ctx, `UPDATE tunnels SET node_id=?, name=?, proto=?, local_ip=?, local_port=?,
		public_port=?, domain=?, enabled=?, bandwidth_kbps=?, max_connections=?, desired_running=?, updated_at=? WHERE id=?`,
		t.NodeID, t.Name, t.Proto, t.LocalIP, t.LocalPort, t.PublicPort, t.Domain, boolInt(t.Enabled),
		t.BandwidthKbps, t.MaxConnections, boolInt(t.DesiredRunning), ts(time.Now()), t.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrConflict
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDesiredRunning flips the user's intent flag and records start time.
func (s *Store) SetDesiredRunning(ctx context.Context, id string, running bool) error {
	if running {
		_, err := s.db.ExecContext(ctx, `UPDATE tunnels SET desired_running=1, last_started_at=? WHERE id=?`, ts(time.Now()), id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE tunnels SET desired_running=0 WHERE id=?`, id)
	return err
}

// ListDesiredRunning returns the user's tunnels marked as "should be running".
func (s *Store) ListDesiredRunning(ctx context.Context, userID string) ([]Tunnel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tunnelCols+` FROM tunnels
		WHERE user_id=? AND desired_running=1 AND enabled=1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// TouchTunnelStart records the last start time.
func (s *Store) TouchTunnelStart(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tunnels SET last_started_at=? WHERE id=?`, ts(time.Now()), id)
	return err
}

// GetTunnel looks a tunnel up by id.
func (s *Store) GetTunnel(ctx context.Context, id string) (*Tunnel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+tunnelCols+` FROM tunnels WHERE id=?`, id)
	t, err := scanTunnel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListTunnels returns tunnels filtered by user ("" = all).
func (s *Store) ListTunnels(ctx context.Context, userID string) ([]Tunnel, error) {
	q := `SELECT ` + tunnelCols + ` FROM tunnels`
	var args []any
	if userID != "" {
		q += ` WHERE user_id=?`
		args = append(args, userID)
	}
	q += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ListTunnelsByNode returns all tunnels bound to a node.
func (s *Store) ListTunnelsByNode(ctx context.Context, nodeID string) ([]Tunnel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tunnelCols+` FROM tunnels WHERE node_id=?`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// DeleteTunnel removes a tunnel.
func (s *Store) DeleteTunnel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tunnels WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PortInUse reports whether a node already uses a public port (excluding id).
func (s *Store) PortInUse(ctx context.Context, nodeID string, port int, excludeID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tunnels WHERE node_id=? AND public_port=? AND id<>?`,
		nodeID, port, excludeID).Scan(&n)
	return n > 0, err
}

// UsedPorts returns the public ports already allocated on a node.
func (s *Store) UsedPorts(ctx context.Context, nodeID string) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT public_port FROM tunnels WHERE node_id=? AND public_port>0`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}
