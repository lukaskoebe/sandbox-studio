package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Secret is a named value sandboxes see as a placeholder environment variable. The value
// is sealed by the vault; the store only keeps the sealed bytes.
type Secret struct {
	ID            string   `json:"id"`
	EnvironmentID string   `json:"environmentId"`
	Name          string   `json:"name"`
	Sealed        []byte   `json:"-"`
	Hosts         []string `json:"hosts"`
	Placeholder   string   `json:"placeholder"`
	Note          string   `json:"note"`
	// StudioOnly secrets are used by Studio itself (a forge's token) and never reach
	// sandboxes: no placeholder, no substitution.
	StudioOnly bool      `json:"studioOnly"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// secretCols are the columns inserted; selects also read studio_only.
const secretCols = "id, environment_id, name, value, hosts, placeholder, note, created_at, updated_at"

const secretSelect = secretCols + ", studio_only"

func scanSecret(row interface{ Scan(...any) error }) (Secret, error) {
	var s Secret
	var hosts string
	var created, updated int64
	err := row.Scan(&s.ID, &s.EnvironmentID, &s.Name, &s.Sealed, &hosts, &s.Placeholder, &s.Note, &created, &updated, &s.StudioOnly)
	s.Hosts = strings.Split(hosts, ",")
	s.CreatedAt, s.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return s, err
}

// CreateSecret inserts sec. The caller picks the ID because the sealed value is bound to it:
// it has to be sealed before the row exists.
func (s *Store) CreateSecret(ctx context.Context, sec Secret) (Secret, error) {
	t := now()
	sec.CreatedAt, sec.UpdatedAt = time.Unix(t, 0), time.Unix(t, 0)
	_, err := s.db.ExecContext(ctx, "INSERT INTO secrets ("+secretCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sec.ID, sec.EnvironmentID, sec.Name, sec.Sealed, strings.Join(sec.Hosts, ","), sec.Placeholder, sec.Note, t, t)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return sec, fmt.Errorf("a secret named %q: %w", sec.Name, ErrExists)
	}
	return sec, err
}

// UpdateSecret replaces the hosts and note of a secret. A nil Sealed keeps the stored value.
// The name and placeholder never change.
func (s *Store) UpdateSecret(ctx context.Context, sec Secret) (Secret, error) {
	var value any // an untyped nil binds as NULL, and COALESCE keeps the stored value then
	if sec.Sealed != nil {
		value = sec.Sealed
	}
	res, err := s.db.ExecContext(ctx, "UPDATE secrets SET value = COALESCE(?, value), hosts = ?, note = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		value, strings.Join(sec.Hosts, ","), sec.Note, now(), sec.EnvironmentID, sec.ID)
	if err != nil {
		return sec, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sec, ErrNotFound
	}
	return s.SecretByID(ctx, sec.EnvironmentID, sec.ID)
}

// SecretByID returns one secret of an environment.
func (s *Store) SecretByID(ctx context.Context, envID, id string) (Secret, error) {
	sec, err := scanSecret(s.db.QueryRowContext(ctx, "SELECT "+secretSelect+" FROM secrets WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sec, ErrNotFound
	}
	return sec, err
}

// Secrets lists the secrets of an environment by name, sealed values included.
func (s *Store) Secrets(ctx context.Context, envID string) ([]Secret, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+secretSelect+" FROM secrets WHERE environment_id = ? ORDER BY name", envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		sec, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// DeleteSecret removes a secret.
func (s *Store) DeleteSecret(ctx context.Context, envID, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE environment_id = ? AND id = ?", envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
