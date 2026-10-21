package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const nodeCols = `id, name, region, public_host, control_host, control_port, enabled, status, latency_ms,
	last_seen_at, max_tunnels, created_at, updated_at`

func scanNode(sc interface{ Scan(...any) error }) (*Node, error) {
	var n Node
	var enabled int
	var lastSeen, created, updated int64
	err := sc.Scan(&n.ID, &n.Name, &n.Region, &n.PublicHost, &n.ControlHost, &n.ControlPort,
		&enabled, &n.Status, &n.LatencyMS, &lastSeen, &n.MaxTunnels, &created, &updated)
	if err != nil {
		return nil, err
	}
	n.Enabled = enabled != 0
	n.LastSeenAt, n.CreatedAt, n.UpdatedAt = fromTS(lastSeen), fromTS(created), fromTS(updated)
	return &n, nil
}

// CreateNode inserts a node.
func (s *Store) CreateNode(ctx context.Context, n *Node) error {
	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	if n.Status == "" {
		n.Status = "offline"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO nodes(`+nodeCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Name, n.Region, n.PublicHost, n.ControlHost, n.ControlPort, boolInt(n.Enabled),
		n.Status, n.LatencyMS, ts(n.LastSeenAt), n.MaxTunnels, ts(n.CreatedAt), ts(n.UpdatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrConflict
	}
	return err
}

// UpdateNode persists the editable node fields.
func (s *Store) UpdateNode(ctx context.Context, n *Node) error {
	res, err := s.db.ExecContext(ctx, `UPDATE nodes SET name=?, region=?, public_host=?, control_host=?,
		control_port=?, enabled=?, max_tunnels=?, updated_at=? WHERE id=?`,
		n.Name, n.Region, n.PublicHost, n.ControlHost, n.ControlPort, boolInt(n.Enabled),
		n.MaxTunnels, ts(time.Now()), n.ID)
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

// SetNodeHealth records the heartbeat result for a node.
func (s *Store) SetNodeHealth(ctx context.Context, id, status string, latencyMS int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET status=?, latency_ms=?, last_seen_at=? WHERE id=?`,
		status, latencyMS, ts(time.Now()), id)
	return err
}

// GetNode looks a node up by id.
func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id=?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// GetNodeByName looks a node up by unique name.
func (s *Store) GetNodeByName(ctx context.Context, name string) (*Node, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE name=?`, name)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// ListNodes returns all nodes.
func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// DeleteNode removes a node that has no tunnels attached.
func (s *Store) DeleteNode(ctx context.Context, id string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tunnels WHERE node_id=?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrConflict
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
