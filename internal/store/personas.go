package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// InUseError refuses to delete a record that others still reference. It is an
// ErrConflict with a readable message.
type InUseError struct{ msg string }

func (e InUseError) Error() string      { return e.msg }
func (InUseError) Is(target error) bool { return target == ErrConflict }

// --- providers ------------------------------------------------------------------------

// Provider is an environment's LLM access. API-key providers own a vault secret holding
// the key; the joined secret fields describe it without its value.
type Provider struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind" enum:"anthropic_api,openai_api,openai_compatible,claude_subscription,chatgpt_subscription"`
	BaseURL       string    `json:"baseUrl,omitempty" doc:"openai_compatible only"`
	Model         string    `json:"model,omitempty" doc:"openai_compatible only: the endpoint's single model"`
	SecretID      string    `json:"secretId,omitempty" doc:"The vault secret holding the key"`
	SecretName    string    `json:"secretName,omitempty"`
	Placeholder   string    `json:"placeholder,omitempty" doc:"What guests see instead of the key"`
	Hosts         []string  `json:"hosts,omitempty" doc:"Hosts the key is sent to"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const providerSelect = `SELECT p.id, p.environment_id, p.name, p.kind, p.base_url, p.model, IFNULL(p.secret_id, ''),
	IFNULL(s.name, ''), IFNULL(s.placeholder, ''), IFNULL(s.hosts, ''), p.created_at, p.updated_at
	FROM providers p LEFT JOIN secrets s ON s.id = p.secret_id`

func scanProvider(row interface{ Scan(...any) error }) (Provider, error) {
	var p Provider
	var hosts string
	var created, updated int64
	err := row.Scan(&p.ID, &p.EnvironmentID, &p.Name, &p.Kind, &p.BaseURL, &p.Model, &p.SecretID,
		&p.SecretName, &p.Placeholder, &hosts, &created, &updated)
	if hosts != "" {
		p.Hosts = strings.Split(hosts, ",")
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return p, err
}

// CreateProvider inserts p and, for API-key providers, its secret in one transaction.
// The caller seals the secret and picks both IDs.
func (s *Store) CreateProvider(ctx context.Context, p Provider, sec *Secret) (Provider, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	t := now()
	p.SecretID = ""
	if sec != nil {
		_, err := tx.ExecContext(ctx, "INSERT INTO secrets ("+secretCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			sec.ID, p.EnvironmentID, sec.Name, sec.Sealed, strings.Join(sec.Hosts, ","), sec.Placeholder, sec.Note, t, t)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return p, fmt.Errorf("a secret named %q: %w", sec.Name, ErrExists)
		}
		if err != nil {
			return p, err
		}
		p.SecretID = sec.ID
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO providers (id, environment_id, name, kind, base_url, model, secret_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		p.ID, p.EnvironmentID, p.Name, p.Kind, p.BaseURL, p.Model, nullable(p.SecretID), t, t)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return p, fmt.Errorf("a provider named %q: %w", p.Name, ErrExists)
	}
	if err != nil {
		return p, err
	}
	if err := tx.Commit(); err != nil {
		return p, err
	}
	return s.Provider(ctx, p.EnvironmentID, p.ID)
}

// ProviderUpdate changes a provider. Nil fields keep their values.
type ProviderUpdate struct {
	BaseURL *string
	Model   *string
	Sealed  []byte   // a new sealed key
	Hosts   []string // new hosts of the key
}

// UpdateProvider applies u to a provider and its secret in one transaction.
func (s *Store) UpdateProvider(ctx context.Context, envID, id string, u ProviderUpdate) (Provider, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Provider{}, err
	}
	defer tx.Rollback()
	p, err := scanProvider(tx.QueryRowContext(ctx, providerSelect+" WHERE p.environment_id = ? AND p.id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	t := now()
	if u.BaseURL != nil {
		p.BaseURL = *u.BaseURL
	}
	if u.Model != nil {
		p.Model = *u.Model
	}
	if _, err := tx.ExecContext(ctx, "UPDATE providers SET base_url = ?, model = ?, updated_at = ? WHERE id = ?", p.BaseURL, p.Model, t, id); err != nil {
		return p, err
	}
	if p.SecretID != "" && (u.Sealed != nil || u.Hosts != nil) {
		var value any
		if u.Sealed != nil {
			value = u.Sealed
		}
		hosts := strings.Join(p.Hosts, ",")
		if u.Hosts != nil {
			hosts = strings.Join(u.Hosts, ",")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE secrets SET value = COALESCE(?, value), hosts = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
			value, hosts, t, envID, p.SecretID); err != nil {
			return p, err
		}
	}
	if err := tx.Commit(); err != nil {
		return p, err
	}
	return s.Provider(ctx, envID, id)
}

// Provider returns one provider of an environment.
func (s *Store) Provider(ctx context.Context, envID, id string) (Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, providerSelect+" WHERE p.environment_id = ? AND p.id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Providers lists the providers of an environment by name.
func (s *Store) Providers(ctx context.Context, envID string) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, providerSelect+" WHERE p.environment_id = ? ORDER BY p.name", envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProvider removes a provider and its secret. It refuses while a persona uses it.
func (s *Store) DeleteProvider(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var secretID string
	err = tx.QueryRowContext(ctx, "SELECT IFNULL(secret_id, '') FROM providers WHERE environment_id = ? AND id = ?", envID, id).Scan(&secretID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if names, err := names(ctx, tx, "SELECT name FROM personas WHERE environment_id = ? AND provider_id = ? ORDER BY name", envID, id); err != nil {
		return err
	} else if len(names) > 0 {
		return InUseError{fmt.Sprintf("the provider is used by %s; change or delete them first", describeNames("persona", names))}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM providers WHERE environment_id = ? AND id = ?", envID, id); err != nil {
		return err
	}
	if secretID != "" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE environment_id = ? AND id = ?", envID, secretID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SecretProvider returns the name of the provider that owns a secret, or "" if none does.
func (s *Store) SecretProvider(ctx context.Context, envID, secretID string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM providers WHERE environment_id = ? AND secret_id = ?", envID, secretID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// --- personas -------------------------------------------------------------------------

// Persona is a named agent identity of an environment.
type Persona struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	Soul          string    `json:"soul" doc:"Personality and working rules, as markdown"`
	Harness       string    `json:"harness" enum:"opencode,claude,codex"`
	ProviderID    string    `json:"providerId"`
	Model         string    `json:"model"`
	GitName       string    `json:"gitName"`
	GitEmail      string    `json:"gitEmail"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const personaCols = "id, environment_id, name, role, soul, harness, provider_id, model, git_name, git_email, created_at, updated_at"

func scanPersona(row interface{ Scan(...any) error }) (Persona, error) {
	var p Persona
	var created, updated int64
	err := row.Scan(&p.ID, &p.EnvironmentID, &p.Name, &p.Role, &p.Soul, &p.Harness, &p.ProviderID, &p.Model, &p.GitName, &p.GitEmail, &created, &updated)
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return p, err
}

func personaExists(err error, name string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("a persona named %q: %w", name, ErrExists)
	}
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return fmt.Errorf("provider: %w", ErrNotFound)
	}
	return err
}

// CreatePersona inserts p, assigning its times and, if it has none, its ID. Its provider
// must be in the same environment.
func (s *Store) CreatePersona(ctx context.Context, p Persona) (Persona, error) {
	t := now()
	if p.ID == "" {
		p.ID = NewID()
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(t, 0), time.Unix(t, 0)
	_, err := s.db.ExecContext(ctx, "INSERT INTO personas ("+personaCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		p.ID, p.EnvironmentID, p.Name, p.Role, p.Soul, p.Harness, p.ProviderID, p.Model, p.GitName, p.GitEmail, t, t)
	return p, personaExists(err, p.Name)
}

// UpdatePersona replaces the editable fields of a persona.
func (s *Store) UpdatePersona(ctx context.Context, p Persona) (Persona, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE personas SET name = ?, role = ?, soul = ?, harness = ?, provider_id = ?, model = ?,
		git_name = ?, git_email = ?, updated_at = ? WHERE environment_id = ? AND id = ?`,
		p.Name, p.Role, p.Soul, p.Harness, p.ProviderID, p.Model, p.GitName, p.GitEmail, now(), p.EnvironmentID, p.ID)
	if err := personaExists(err, p.Name); err != nil {
		return p, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, ErrNotFound
	}
	return s.Persona(ctx, p.EnvironmentID, p.ID)
}

// Persona returns one persona of an environment.
func (s *Store) Persona(ctx context.Context, envID, id string) (Persona, error) {
	p, err := scanPersona(s.db.QueryRowContext(ctx, "SELECT "+personaCols+" FROM personas WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Personas lists the personas of an environment by name.
func (s *Store) Personas(ctx context.Context, envID string) ([]Persona, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+personaCols+" FROM personas WHERE environment_id = ? ORDER BY name", envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Persona{}
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePersona removes a persona and the rules scoped to it. It refuses while the
// persona owns sandboxes.
func (s *Store) DeletePersona(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if names, err := names(ctx, tx, "SELECT name FROM sandboxes WHERE environment_id = ? AND persona_id = ? ORDER BY name", envID, id); err != nil {
		return err
	} else if len(names) > 0 {
		return InUseError{fmt.Sprintf("the persona owns %s; delete them first", describeNames("sandbox", names))}
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM personas WHERE environment_id = ? AND id = ?", envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SandboxPersonas maps the sandboxes of an environment that a persona owns to it.
func (s *Store) SandboxPersonas(ctx context.Context, envID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, persona_id FROM sandboxes WHERE environment_id = ? AND persona_id IS NOT NULL", envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sb, p string
		if err := rows.Scan(&sb, &p); err != nil {
			return nil, err
		}
		out[sb] = p
	}
	return out, rows.Err()
}

func names(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// describeNames reads "persona Ada" or "3 personas (Ada, Bob, Cy)".
func describeNames(noun string, names []string) string {
	if len(names) == 1 {
		return fmt.Sprintf("%s %q", noun, names[0])
	}
	plural := noun + "s"
	if strings.HasSuffix(noun, "x") {
		plural = noun + "es"
	}
	shown := names
	if len(shown) > 5 {
		shown = shown[:5]
	}
	list := strings.Join(shown, ", ")
	if len(names) > len(shown) {
		list += ", …"
	}
	return fmt.Sprintf("%d %s (%s)", len(names), plural, list)
}
