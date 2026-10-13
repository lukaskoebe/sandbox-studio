package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
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

// Rule origins: how a rule came to exist.
const (
	OriginManual   = "manual"   // written on the Rules page or through the API
	OriginApproval = "approval" // created by approving a network request
	OriginCurated  = "curated"  // reserved for the curated list
	OriginReviewer = "reviewer" // reserved for the AI reviewer
)

// Rule decides what happens to connections to Host. SandboxID and TeammateID are empty for
// rules that cover the whole environment; at most one of them is set.
type Rule struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"environmentId"`
	SandboxID     string     `json:"sandboxId,omitempty"`
	TeammateID    string     `json:"teammateId,omitempty" doc:"Set for rules that apply to every sandbox of one teammate"`
	Host          string     `json:"host" doc:"example.com, *.example.com (includes example.com), or an IP address"`
	Ports         []int      `json:"ports" doc:"Empty means any port"`
	Action        string     `json:"action" enum:"allow,proxy,caddy,deny"`
	Config        RuleConfig `json:"config"`
	Note          string     `json:"note"`
	Origin        string     `json:"origin" enum:"manual,approval,curated,reviewer" doc:"Where the rule came from. Read-only: set when the rule is created"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty" doc:"When the rule stops matching; empty means it never expires"`
	Hits          int        `json:"hits" doc:"Connections the rule decided, as of the last write, which happens about once a minute"`
	LastHitAt     *time.Time `json:"lastHitAt,omitempty" doc:"When the rule last decided a connection, as of the last write"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// RuleConfig holds the settings of proxy and caddy rules.
type RuleConfig struct {
	// Headers are set on every request; values may reference secrets as {secret.NAME}.
	Headers map[string]string `json:"headers,omitempty"`
	// Caddyfile is the site block body used by caddy rules.
	Caddyfile string `json:"caddyfile,omitempty"`
	// Upstream, HostHeader, SNI and CA change where a proxy rule sends its requests. Upstream is
	// a host or host:port to send them to instead of the rule's host; HostHeader and SNI replace
	// the Host header and the TLS server name, which otherwise follow the request and the
	// upstream; CA holds PEM certificates trusted for the upstream, besides the system's.
	Upstream   string `json:"upstream,omitempty"`
	HostHeader string `json:"hostHeader,omitempty"`
	SNI        string `json:"sni,omitempty"`
	CA         string `json:"ca,omitempty"`
}

const ruleCols = "id, environment_id, IFNULL(sandbox_id, ''), IFNULL(teammate_id, ''), host, ports, action, config, note, origin, expires_at, hits, last_hit_at, created_at"

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var ports, config string
	var expires, lastHit sql.NullInt64
	var created int64
	if err := row.Scan(&r.ID, &r.EnvironmentID, &r.SandboxID, &r.TeammateID, &r.Host, &ports, &r.Action, &config, &r.Note, &r.Origin, &expires, &r.Hits, &lastHit, &created); err != nil {
		return r, err
	}
	r.Ports = []int{}
	for _, p := range strings.Split(ports, ",") {
		if n, err := strconv.Atoi(p); err == nil {
			r.Ports = append(r.Ports, n)
		}
	}
	if expires.Valid {
		t := time.Unix(expires.Int64, 0)
		r.ExpiresAt = &t
	}
	if lastHit.Valid {
		t := time.Unix(lastHit.Int64, 0)
		r.LastHitAt = &t
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

// CreateRule inserts r, assigning its ID, creation time and, unless set, the manual origin.
func (s *Store) CreateRule(ctx context.Context, r Rule) (Rule, error) {
	r.ID, r.CreatedAt = NewID(), time.Unix(now(), 0)
	if r.Ports == nil {
		r.Ports = []int{}
	}
	if r.Origin == "" {
		r.Origin = OriginManual
	}
	return r, insertRule(ctx, s.db, r)
}

// insertRule writes a rule with the ID and creation time it has. A rule without an origin
// was made by hand.
func insertRule(ctx context.Context, q execer, r Rule) error {
	if r.Origin == "" {
		r.Origin = OriginManual
	}
	config, err := json.Marshal(r.Config)
	if err != nil {
		return err
	}
	var expires any // NULL: the rule never expires
	if r.ExpiresAt != nil {
		expires = r.ExpiresAt.Unix()
	}
	_, err = q.ExecContext(ctx, "INSERT INTO rules (id, environment_id, sandbox_id, teammate_id, host, ports, action, config, note, origin, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.EnvironmentID, nullable(r.SandboxID), nullable(r.TeammateID), r.Host, joinPorts(r.Ports), r.Action, string(config), r.Note, r.Origin, expires, r.CreatedAt.Unix())
	return err
}

// UpdateRule replaces the editable fields of a rule.
func (s *Store) UpdateRule(ctx context.Context, r Rule) (Rule, error) {
	config, err := json.Marshal(r.Config)
	if err != nil {
		return r, err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE rules SET sandbox_id = ?, teammate_id = ?, host = ?, ports = ?, action = ?, config = ?, note = ? WHERE environment_id = ? AND id = ?",
		nullable(r.SandboxID), nullable(r.TeammateID), r.Host, joinPorts(r.Ports), r.Action, string(config), r.Note, r.EnvironmentID, r.ID)
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

// DeleteExpiredRules deletes the rules that expired at or before now. It returns the IDs of
// the environments they belonged to, each once.
func (s *Store) DeleteExpiredRules(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "DELETE FROM rules WHERE expires_at IS NOT NULL AND expires_at <= ? RETURNING environment_id", now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var envs []string
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return nil, err
		}
		if !slices.Contains(envs, env) {
			envs = append(envs, env)
		}
	}
	return envs, rows.Err()
}

// RuleHit is the connections a rule decided since the policy last wrote its hits.
type RuleHit struct {
	ID    string
	Count int
	Last  time.Time // when the last of them was decided
}

// AddRuleHits adds batched hits to their rules, in one transaction. Hits for rules deleted
// in the meantime are dropped.
func (s *Store) AddRuleHits(ctx context.Context, hits []RuleHit) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, h := range hits {
		if _, err := tx.ExecContext(ctx, "UPDATE rules SET hits = hits + ?, last_hit_at = MAX(IFNULL(last_hit_at, 0), ?) WHERE id = ?", h.Count, h.Last.Unix(), h.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	Once          bool            `json:"once,omitempty" doc:"Set when a network request was decided for its one connection, without a rule"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

const approvalCols = "id, environment_id, IFNULL(sandbox_id, ''), kind, subject, payload, attempts, status, IFNULL(rule_id, ''), created_at, updated_at, once"

func scanApproval(row interface{ Scan(...any) error }) (Approval, error) {
	var a Approval
	var payload string
	var created, updated int64
	err := row.Scan(&a.ID, &a.EnvironmentID, &a.SandboxID, &a.Kind, &a.Subject, &payload, &a.Attempts, &a.Status, &a.RuleID, &created, &updated, &a.Once)
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
	out, created, err = requestApprovalTx(ctx, tx, a)
	if err != nil {
		return out, created, err
	}
	return out, created, tx.Commit()
}

// RequestApprovalAndUpdateChatItem creates an approval and links it to its chat item in
// the same transaction. A permission approval is not visible without its item link.
func (s *Store) RequestApprovalAndUpdateChatItem(ctx context.Context, a Approval, it ChatItem) (out Approval, updated ChatItem, created bool, err error) {
	err = s.inChatTx(ctx, it.ChatID, func(tx *sql.Tx, rev int64) error {
		var chatEnvironmentID, chatSandboxID string
		if err := tx.QueryRowContext(ctx, "SELECT environment_id, IFNULL(sandbox_id, '') FROM chats WHERE id = ?", it.ChatID).
			Scan(&chatEnvironmentID, &chatSandboxID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if it.ID == "" || it.Kind != "permission" || a.Kind != "chat.permission" ||
			a.EnvironmentID != chatEnvironmentID || a.SandboxID != chatSandboxID ||
			a.Subject != "chat/"+it.ChatID+"/"+it.ID {
			return ErrConflict
		}
		var requestErr error
		out, created, requestErr = requestApprovalTx(ctx, tx, a)
		if requestErr != nil {
			return requestErr
		}
		it.ApprovalID = out.ID
		updated, requestErr = updateChatItemTx(ctx, tx, rev, it)
		return requestErr
	})
	return out, updated, created, err
}

// DecideApprovalAndUpdateChatItem changes an approval and its chat item atomically.
func (s *Store) DecideApprovalAndUpdateChatItem(ctx context.Context, envID, id, status, ruleID string, it ChatItem) (updated ChatItem, err error) {
	err = s.inChatTx(ctx, it.ChatID, func(tx *sql.Tx, rev int64) error {
		var chatEnvironmentID, chatSandboxID string
		if err := tx.QueryRowContext(ctx, "SELECT environment_id, IFNULL(sandbox_id, '') FROM chats WHERE id = ?", it.ChatID).
			Scan(&chatEnvironmentID, &chatSandboxID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var approvalSandboxID, approvalSubject, approvalKind string
		if err := tx.QueryRowContext(ctx, "SELECT IFNULL(sandbox_id, ''), subject, kind FROM approvals WHERE environment_id = ? AND id = ?", envID, id).
			Scan(&approvalSandboxID, &approvalSubject, &approvalKind); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if envID != chatEnvironmentID || approvalSandboxID != chatSandboxID || approvalKind != "chat.permission" ||
			it.Kind != "permission" || it.ApprovalID != id || approvalSubject != "chat/"+it.ChatID+"/"+it.ID {
			return ErrConflict
		}
		if err := DecideApprovalTx(ctx, tx, envID, id, status, ruleID); err != nil {
			return err
		}
		var updateErr error
		updated, updateErr = updateChatItemTx(ctx, tx, rev, it)
		return updateErr
	})
	return updated, err
}

type approvalQuerier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func requestApprovalTx(ctx context.Context, tx approvalQuerier, a Approval) (out Approval, created bool, err error) {
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
	return out, created, nil
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

// ApprovalsByKind lists approvals of one kind, newest first.
func (s *Store) ApprovalsByKind(ctx context.Context, kind, status string, limit int) ([]Approval, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+approvalCols+` FROM approvals
		WHERE kind = ? AND (? = '' OR status = ?)
		ORDER BY updated_at DESC LIMIT ?`, kind, status, status, limit)
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
	return DecideApprovalTx(ctx, s.db, envID, id, status, ruleID)
}

// DecideApprovalOnce moves a pending network request to status for its one connection: no
// rule is involved, and the decision is recorded as once. It returns ErrNotFound if the
// approval is not pending.
func (s *Store) DecideApprovalOnce(ctx context.Context, envID, id, status string) error {
	t := now()
	res, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ?, once = 1, decided_at = ?, updated_at = ?
		WHERE environment_id = ? AND id = ? AND status = 'pending'`, status, t, t, envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DecideApprovalTx is DecideApproval inside tx, a transaction on the catalog database, so
// that the decision commits together with what it causes or not at all.
func DecideApprovalTx(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, envID, id, status, ruleID string) error {
	t := now()
	res, err := tx.ExecContext(ctx, `UPDATE approvals SET status = ?, rule_id = ?, decided_at = ?, updated_at = ?
		WHERE environment_id = ? AND id = ? AND status = 'pending'`, status, nullable(ruleID), t, t, envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
