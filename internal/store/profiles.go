package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// CloudProfile is an installed (verified) profile document.
type CloudProfile struct {
	ID          string    `json:"id"`
	Vendor      string    `json:"vendor"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Source      string    `json:"source"`
	SHA256      string    `json:"sha256"`
	Document    string    `json:"-"`
	Enabled     bool      `json:"enabled"`
	InstalledAt time.Time `json:"installed_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProfileCredential is a named credential set for a profile. Values are stored
// encrypted with the server's data key.
type ProfileCredential struct {
	ID         string    `json:"id"`
	ProfileID  string    `json:"profile_id"`
	Label      string    `json:"label"`
	FieldsJSON string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ProfileRun records one execution attempt.
type ProfileRun struct {
	ID         string    `json:"id"`
	ProfileID  string    `json:"profile_id"`
	ProfileSHA string    `json:"profile_sha256"`
	Action     string    `json:"action"`
	NodeID     string    `json:"node_id"`
	PortMin    int       `json:"port_min"`
	PortMax    int       `json:"port_max"`
	Protocol   string    `json:"protocol"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	DurationMS int64     `json:"duration_ms"`
	ActorID    string    `json:"actor_id"`
	ActorName  string    `json:"actor_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// UpsertCloudProfile installs or refreshes a profile.
func (s *Store) UpsertCloudProfile(ctx context.Context, p *CloudProfile) error {
	now := time.Now().UTC()
	if p.InstalledAt.IsZero() {
		p.InstalledAt = now
	}
	p.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO cloud_profiles
		(id, vendor, name, version, source, sha256, document, enabled, installed_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  vendor=excluded.vendor, name=excluded.name, version=excluded.version,
		  source=excluded.source, sha256=excluded.sha256, document=excluded.document,
		  enabled=excluded.enabled, updated_at=excluded.updated_at`,
		p.ID, p.Vendor, p.Name, p.Version, p.Source, p.SHA256, p.Document,
		boolInt(p.Enabled), ts(p.InstalledAt), ts(p.UpdatedAt))
	return err
}

const profileCols = `id, vendor, name, version, source, sha256, document, enabled, installed_at, updated_at`

func scanProfile(sc interface{ Scan(...any) error }) (*CloudProfile, error) {
	var p CloudProfile
	var enabled int
	var installed, updated int64
	if err := sc.Scan(&p.ID, &p.Vendor, &p.Name, &p.Version, &p.Source, &p.SHA256,
		&p.Document, &enabled, &installed, &updated); err != nil {
		return nil, err
	}
	p.Enabled = enabled != 0
	p.InstalledAt, p.UpdatedAt = fromTS(installed), fromTS(updated)
	return &p, nil
}

// GetCloudProfile returns one installed profile.
func (s *Store) GetCloudProfile(ctx context.Context, id string) (*CloudProfile, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM cloud_profiles WHERE id=?`, id)
	p, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListCloudProfiles returns installed profiles ordered by vendor.
func (s *Store) ListCloudProfiles(ctx context.Context) ([]CloudProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+profileCols+` FROM cloud_profiles ORDER BY vendor, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloudProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// SetCloudProfileEnabled toggles a profile.
func (s *Store) SetCloudProfileEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE cloud_profiles SET enabled=?, updated_at=? WHERE id=?`,
		boolInt(enabled), ts(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCloudProfile removes a profile and its credentials.
func (s *Store) DeleteCloudProfile(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM profile_credentials WHERE profile_id=?`, id); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM cloud_profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- credentials ----

// UpsertProfileCredential stores an encrypted credential set.
func (s *Store) UpsertProfileCredential(ctx context.Context, c *ProfileCredential) error {
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO profile_credentials
		(id, profile_id, label, fields_json, created_at, updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(profile_id, label) DO UPDATE SET fields_json=excluded.fields_json, updated_at=excluded.updated_at`,
		c.ID, c.ProfileID, c.Label, c.FieldsJSON, ts(c.CreatedAt), ts(c.UpdatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrConflict
	}
	return err
}

// GetProfileCredential looks up a credential set by profile + label.
func (s *Store) GetProfileCredential(ctx context.Context, profileID, label string) (*ProfileCredential, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, profile_id, label, fields_json, created_at, updated_at
		FROM profile_credentials WHERE profile_id=? AND label=?`, profileID, label)
	var c ProfileCredential
	var created, updated int64
	err := row.Scan(&c.ID, &c.ProfileID, &c.Label, &c.FieldsJSON, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt, c.UpdatedAt = fromTS(created), fromTS(updated)
	return &c, nil
}

// ListProfileCredentials returns credential rows including the sealed blob.
//
// The blob is ciphertext and carries `json:"-"`, so it is safe to load here: the
// API layer decrypts it only to derive the masked field names. Callers must
// never serialize FieldsJSON.
func (s *Store) ListProfileCredentials(ctx context.Context, profileID string) ([]ProfileCredential, error) {
	q := `SELECT id, profile_id, label, fields_json, created_at, updated_at FROM profile_credentials`
	var args []any
	if profileID != "" {
		q += ` WHERE profile_id=?`
		args = append(args, profileID)
	}
	q += ` ORDER BY profile_id, label`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileCredential
	for rows.Next() {
		var c ProfileCredential
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.ProfileID, &c.Label, &c.FieldsJSON, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt = fromTS(created), fromTS(updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteProfileCredential removes a credential set.
func (s *Store) DeleteProfileCredential(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM profile_credentials WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- runs ----

// AddProfileRun appends an execution record.
func (s *Store) AddProfileRun(ctx context.Context, r *ProfileRun) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO profile_runs
		(id, profile_id, profile_sha, action, node_id, port_min, port_max, protocol,
		 status, message, duration_ms, actor_id, actor_name, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.ProfileID, r.ProfileSHA, r.Action, r.NodeID, r.PortMin, r.PortMax, r.Protocol,
		r.Status, r.Message, r.DurationMS, r.ActorID, r.ActorName, ts(r.CreatedAt))
	return err
}

// ListProfileRuns returns the newest runs first.
func (s *Store) ListProfileRuns(ctx context.Context, profileID string, limit int) ([]ProfileRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, profile_id, profile_sha, action, node_id, port_min, port_max, protocol,
		status, message, duration_ms, actor_id, actor_name, created_at FROM profile_runs`
	var args []any
	if profileID != "" {
		q += ` WHERE profile_id=?`
		args = append(args, profileID)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileRun
	for rows.Next() {
		var r ProfileRun
		var created int64
		if err := rows.Scan(&r.ID, &r.ProfileID, &r.ProfileSHA, &r.Action, &r.NodeID,
			&r.PortMin, &r.PortMax, &r.Protocol, &r.Status, &r.Message,
			&r.DurationMS, &r.ActorID, &r.ActorName, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = fromTS(created)
		out = append(out, r)
	}
	return out, rows.Err()
}
