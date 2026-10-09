package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// --- settings -------------------------------------------------------------------------

// Secret returns the install-wide random value stored under key, creating it on first use.
func (s *Store) Secret(ctx context.Context, key string, size int) ([]byte, error) {
	var v []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	v = make([]byte, size)
	rand.Read(v)
	if _, err := s.db.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT DO NOTHING", key, v); err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	return v, err
}

// Setting returns the value stored under key, or ErrNotFound.
func (s *Store) Setting(ctx context.Context, key string) ([]byte, error) {
	var v []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// SetSetting stores value under key, replacing any previous value.
func (s *Store) SetSetting(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// --- rules ----------------------------------------------------------------------------

// Rule actions.
const (
	ActionAllow = "allow" // splice the connection through untouched
	ActionProxy = "proxy" // terminate TLS with the Studio CA and rewrite requests
	ActionCaddy = "caddy" // terminate TLS and hand requests to a Caddyfile snippet
	ActionDeny  = "deny"
)

// Rule decides what happens to connections to Host. SandboxID is empty for rules that
// cover the whole environment.
type Rule struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"environmentId"`
	SandboxID     string     `json:"sandboxId,omitempty"`
	Host          string     `json:"host" doc:"example.com, *.example.com (includes example.com), or an IP address"`
	Ports         []int      `json:"ports" doc:"Empty means any port"`
	Action        string     `json:"action" enum:"allow,proxy,caddy,deny"`
	Config        RuleConfig `json:"config"`
	Note          string     `json:"note"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// RuleConfig holds the settings of proxy and caddy rules.
type RuleConfig struct {
	// Headers are set on every request; values may reference secrets as {secret.NAME}.
	Headers map[string]string `json:"headers,omitempty"`
	// Caddyfile is the site block body used by caddy rules.
	Caddyfile string `json:"caddyfile,omitempty"`
}

const ruleCols = "id, environment_id, IFNULL(sandbox_id, ''), host, ports, action, config, note, created_at"

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var ports, config string
	var created int64
	if err := row.Scan(&r.ID, &r.EnvironmentID, &r.SandboxID, &r.Host, &ports, &r.Action, &config, &r.Note, &created); err != nil {
		return r, err
	}
	r.Ports = []int{}
	for _, p := range strings.Split(ports, ",") {
		if n, err := strconv.Atoi(p); err == nil {
			r.Ports = append(r.Ports, n)
		}
	}
	r.CreatedAt = time.Unix(created, 0)
	return r, json.Unmarshal([]byte(config), &r.Config)
}

func joinPorts(ports []int) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ",")
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CreateRule inserts r, assigning its ID and creation time.
func (s *Store) CreateRule(ctx context.Context, r Rule) (Rule, error) {
	r.ID, r.CreatedAt = NewID(), time.Unix(now(), 0)
	if r.Ports == nil {
		r.Ports = []int{}
	}
	config, err := json.Marshal(r.Config)
	if err != nil {
		return r, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO rules (id, environment_id, sandbox_id, host, ports, action, config, note, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.EnvironmentID, nullable(r.SandboxID), r.Host, joinPorts(r.Ports), r.Action, string(config), r.Note, r.CreatedAt.Unix())
	return r, err
}

// UpdateRule replaces the editable fields of a rule.
func (s *Store) UpdateRule(ctx context.Context, r Rule) (Rule, error) {
	config, err := json.Marshal(r.Config)
	if err != nil {
		return r, err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE rules SET sandbox_id = ?, host = ?, ports = ?, action = ?, config = ?, note = ? WHERE environment_id = ? AND id = ?",
		nullable(r.SandboxID), r.Host, joinPorts(r.Ports), r.Action, string(config), r.Note, r.EnvironmentID, r.ID)
	if err != nil {
		return r, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return r, ErrNotFound
	}
	return s.Rule(ctx, r.EnvironmentID, r.ID)
}

// Rule returns one rule of an environment.
func (s *Store) Rule(ctx context.Context, envID, id string) (Rule, error) {
	r, err := scanRule(s.db.QueryRowContext(ctx, "SELECT "+ruleCols+" FROM rules WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// Rules lists the rules of an environment, including sandbox-scoped ones.
func (s *Store) Rules(ctx context.Context, envID string) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+ruleCols+" FROM rules WHERE environment_id = ? ORDER BY host, created_at", envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRule removes a rule.
func (s *Store) DeleteRule(ctx context.Context, envID, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM rules WHERE environment_id = ? AND id = ?", envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- approvals ------------------------------------------------------------------------

// Approval statuses.
const (
	StatusPending   = "pending"
	StatusApproved  = "approved"
	StatusDenied    = "denied"
	StatusDismissed = "dismissed"
)

// Approval is a request for a decision. Repeated identical requests (same Subject) while
// one is pending raise Attempts instead of creating new rows.
type Approval struct {
	ID            string          `json:"id"`
	EnvironmentID string          `json:"environmentId"`
	SandboxID     string          `json:"sandboxId,omitempty"`
	Kind          string          `json:"kind"`
	Subject       string          `json:"subject"`
	Payload       json.RawMessage `json:"-"`
	Attempts      int             `json:"attempts"`
	Status        string          `json:"status" enum:"pending,approved,denied,dismissed"`
	RuleID        string          `json:"ruleId,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

const approvalCols = "id, environment_id, IFNULL(sandbox_id, ''), kind, subject, payload, attempts, status, IFNULL(rule_id, ''), created_at, updated_at"

func scanApproval(row interface{ Scan(...any) error }) (Approval, error) {
	var a Approval
	var payload string
	var created, updated int64
	err := row.Scan(&a.ID, &a.EnvironmentID, &a.SandboxID, &a.Kind, &a.Subject, &payload, &a.Attempts, &a.Status, &a.RuleID, &created, &updated)
	a.Payload = json.RawMessage(payload)
	a.CreatedAt, a.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return a, err
}

// RequestApproval records a pending request, or counts another attempt of the pending
// request with the same subject. created reports whether a new row was inserted.
func (s *Store) RequestApproval(ctx context.Context, a Approval) (out Approval, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return a, false, err
	}
	defer tx.Rollback()
	t := now()
	const match = "environment_id = ? AND IFNULL(sandbox_id, '') = ? AND kind = ? AND subject = ? AND status = 'pending'"
	res, err := tx.ExecContext(ctx, "UPDATE approvals SET attempts = attempts + 1, updated_at = ? WHERE "+match,
		t, a.EnvironmentID, a.SandboxID, a.Kind, a.Subject)
	if err != nil {
		return a, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = tx.ExecContext(ctx, "INSERT INTO approvals (id, environment_id, sandbox_id, kind, subject, payload, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			NewID(), a.EnvironmentID, nullable(a.SandboxID), a.Kind, a.Subject, string(a.Payload), t, t)
		if err != nil {
			return a, false, err
		}
		created = true
	}
	out, err = scanApproval(tx.QueryRowContext(ctx, "SELECT "+approvalCols+" FROM approvals WHERE "+match,
		a.EnvironmentID, a.SandboxID, a.Kind, a.Subject))
	if err != nil {
		return a, false, err
	}
	return out, created, tx.Commit()
}

// Approval returns one approval of an environment.
func (s *Store) Approval(ctx context.Context, envID, id string) (Approval, error) {
	a, err := scanApproval(s.db.QueryRowContext(ctx, "SELECT "+approvalCols+" FROM approvals WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// Approvals lists approvals, newest first. An empty envID lists all environments, for
// the inbox that pops up wherever the user is; an empty status lists every status.
func (s *Store) Approvals(ctx context.Context, envID, status string, limit int) ([]Approval, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+approvalCols+` FROM approvals
		WHERE (? = '' OR environment_id = ?) AND (? = '' OR status = ?)
		ORDER BY updated_at DESC LIMIT ?`, envID, envID, status, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DecideApproval moves a pending approval to status. It returns ErrNotFound if the
// approval does not exist or was already decided.
func (s *Store) DecideApproval(ctx context.Context, envID, id, status, ruleID string) error {
	t := now()
	res, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ?, rule_id = ?, decided_at = ?, updated_at = ?
		WHERE environment_id = ? AND id = ? AND status = 'pending'`, status, nullable(ruleID), t, t, envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
