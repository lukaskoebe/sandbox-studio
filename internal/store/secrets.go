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
// is sealed by the vault; the store only keeps the sealed bytes. Like a rule, a secret
// covers the environment, the sandboxes of one teammate or one sandbox; at most one of
// TeammateID and SandboxID is set, and the scope never changes.
type Secret struct {
	ID            string   `json:"id"`
	EnvironmentID string   `json:"environmentId"`
	SandboxID     string   `json:"sandboxId,omitempty" doc:"Set for a secret only this sandbox sees"`
	TeammateID    string   `json:"teammateId,omitempty" doc:"Set for a secret every sandbox of this teammate sees"`
	Name          string   `json:"name"`
	Sealed        []byte   `json:"-"`
	Hosts         []string `json:"hosts"`
	Placeholder   string   `json:"placeholder"`
	Note          string   `json:"note"`
	// StudioOnly secrets are used by Studio itself (a forge's token) and never reach
	// sandboxes: no placeholder, no substitution.
	StudioOnly bool `json:"studioOnly"`
	// BrowserOnly secrets are a teammate's browser logins: Studio fills them into pages of
	// their hosts and the gateway sends them to those hosts as Basic credentials with
	// Username. No sandbox sees them.
	BrowserOnly bool      `json:"browserOnly"`
	Username    string    `json:"username,omitempty" doc:"A browser login's username, sent with it as HTTP Basic credentials"`
	AgentMayUse bool      `json:"agentMayUse" doc:"A browser login the teammate's agents fill, and send as Basic credentials, without asking the user"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// secretCols are the columns inserted; selects also read studio_only and the scope.
const secretCols = "id, environment_id, name, value, hosts, placeholder, note, created_at, updated_at"

const secretSelect = secretCols + ", studio_only, IFNULL(teammate_id, ''), IFNULL(sandbox_id, ''), browser_only, username, agent_may_use"

func scanSecret(row interface{ Scan(...any) error }) (Secret, error) {
	var s Secret
	var hosts string
	var created, updated int64
	err := row.Scan(&s.ID, &s.EnvironmentID, &s.Name, &s.Sealed, &hosts, &s.Placeholder, &s.Note, &created, &updated, &s.StudioOnly, &s.TeammateID, &s.SandboxID, &s.BrowserOnly, &s.Username, &s.AgentMayUse)
	if hosts != "" {
		s.Hosts = strings.Split(hosts, ",")
	}
	s.CreatedAt, s.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return s, err
}

// CreateSecret inserts sec. The caller picks the ID because the sealed value is bound to it:
// it has to be sealed before the row exists.
func (s *Store) CreateSecret(ctx context.Context, sec Secret) (Secret, error) {
	t := now()
	sec.CreatedAt, sec.UpdatedAt = time.Unix(t, 0), time.Unix(t, 0)
	_, err := s.db.ExecContext(ctx, "INSERT INTO secrets ("+secretCols+", studio_only, teammate_id, sandbox_id, browser_only, username, agent_may_use) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sec.ID, sec.EnvironmentID, sec.Name, sec.Sealed, strings.Join(sec.Hosts, ","), sec.Placeholder, sec.Note, t, t,
		sec.StudioOnly, nullable(sec.TeammateID), nullable(sec.SandboxID), sec.BrowserOnly, sec.Username, sec.AgentMayUse)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return sec, fmt.Errorf("a secret named %q: %w", sec.Name, ErrExists)
	}
	return sec, err
}

// UpdateSecret replaces the hosts, note and username of a secret. A nil Sealed keeps the stored
// value. The name, scope and placeholder never change.
func (s *Store) UpdateSecret(ctx context.Context, sec Secret) (Secret, error) {
	var value any // an untyped nil binds as NULL, and COALESCE keeps the stored value then
	if sec.Sealed != nil {
		value = sec.Sealed
	}
	res, err := s.db.ExecContext(ctx, "UPDATE secrets SET value = COALESCE(?, value), hosts = ?, note = ?, username = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		value, strings.Join(sec.Hosts, ","), sec.Note, sec.Username, now(), sec.EnvironmentID, sec.ID)
	if err != nil {
		return sec, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sec, ErrNotFound
	}
	return s.SecretByID(ctx, sec.EnvironmentID, sec.ID)
}

// SetAgentMayUse sets whether a browser login's agents use it without asking.
func (s *Store) SetAgentMayUse(ctx context.Context, envID, id string, on bool) (Secret, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE secrets SET agent_may_use = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND browser_only = 1",
		on, now(), envID, id)
	if err != nil {
		return Secret{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Secret{}, ErrNotFound
	}
	return s.SecretByID(ctx, envID, id)
}

// SecretByID returns one secret of an environment.
func (s *Store) SecretByID(ctx context.Context, envID, id string) (Secret, error) {
	sec, err := scanSecret(s.db.QueryRowContext(ctx, "SELECT "+secretSelect+" FROM secrets WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sec, ErrNotFound
	}
	return sec, err
}

// Secrets lists the secrets of an environment by name, sealed values included, whatever
// their scope.
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

// SecretRank tells how specific sec is for a sandbox sandboxID owned by teammateID: 3 for
// the sandbox's own secrets, 2 for its teammate's, 1 for the environment's, and 0 if the
// sandbox does not see it at all. Studio-only secrets and browser logins are never seen by
// sandboxes.
func SecretRank(sec Secret, sandboxID, teammateID string) int {
	switch {
	case sec.StudioOnly, sec.BrowserOnly:
		return 0
	case sec.SandboxID != "":
		if sec.SandboxID == sandboxID {
			return 3
		}
		return 0
	case sec.TeammateID != "":
		if teammateID != "" && sec.TeammateID == teammateID {
			return 2
		}
		return 0
	}
	return 1
}

// BrowserLogins keeps the browser logins of teammateID, in the order of list.
func BrowserLogins(list []Secret, teammateID string) []Secret {
	out := []Secret{}
	for _, sec := range list {
		if sec.BrowserOnly && teammateID != "" && sec.TeammateID == teammateID {
			out = append(out, sec)
		}
	}
	return out
}

// VisibleSecrets keeps the secrets of an environment that a sandbox sandboxID owned by
// teammateID sees: the environment's, its teammate's and its own. Of secrets sharing a name
// only the most specific one is kept. The order of list is preserved.
func VisibleSecrets(list []Secret, sandboxID, teammateID string) []Secret {
	best := map[string]int{}
	for _, sec := range list {
		if r := SecretRank(sec, sandboxID, teammateID); r > best[sec.Name] {
			best[sec.Name] = r
		}
	}
	out := []Secret{}
	for _, sec := range list {
		if r := SecretRank(sec, sandboxID, teammateID); r > 0 && r == best[sec.Name] {
			out = append(out, sec)
		}
	}
	return out
}
