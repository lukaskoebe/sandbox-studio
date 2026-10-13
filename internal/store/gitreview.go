package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// --- forges ----------------------------------------------------------------------------

// Forge is a git host an environment's sandboxes push to through the virtual git remote.
// Its credential (a token, or for kind ssh a private key) is a studio-only secret:
// sandboxes never see it. An ssh forge with auth agent has no secret: Studio signs with
// the host's SSH agent.
type Forge struct {
	ID                 string     `json:"id"`
	EnvironmentID      string     `json:"environmentId"`
	Name               string     `json:"name" doc:"The URL segment: https://git.studio.internal/<name>/<path>.git"`
	Kind               string     `json:"kind" enum:"forgejo,github,https,ssh"`
	BaseURL            string     `json:"baseUrl" doc:"https://host[/path], or ssh://user@host[:port] for kind ssh"`
	Username           string     `json:"username,omitempty" doc:"The basic-auth user (https) or SSH user (ssh)"`
	Auth               string     `json:"auth,omitempty" enum:"agent,key" doc:"Kind ssh: sign with the host's SSH agent, or with a key Studio generated"`
	SecretID           string     `json:"secretId,omitempty" doc:"The studio-only vault secret holding the token or SSH key; none for auth agent"`
	PublicKey          string     `json:"publicKey,omitempty" doc:"The public half of the SSH key Studio generated (auth key), to add to the host"`
	HostKey            string     `json:"hostKey,omitempty" doc:"The pinned SSH host key in authorized_keys format (ssh)"`
	HostKeyFingerprint string     `json:"hostKeyFingerprint,omitempty" doc:"SHA256 fingerprint of the pinned host key"`
	HostKeyMismatch    string     `json:"hostKeyMismatch,omitempty" doc:"Fingerprint of a different host key the host presented since it was pinned; re-pin to clear"`
	AgentSlowAt        *time.Time `json:"agentSlowAt,omitempty" doc:"When a signature by the SSH agent last waited for the user to confirm it"`
	AgentHintDismissed bool       `json:"agentHintDismissed,omitempty" doc:"The user dismissed the suggestion to use a Studio key instead"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// Authentication of ssh forges.
const (
	ForgeAuthAgent = "agent"
	ForgeAuthKey   = "key"
)

const forgeCols = `id, environment_id, name, kind, base_url, username, auth, IFNULL(secret_id, ''), public_key, host_key,
	host_key_offered, agent_slow_at, agent_hint_dismissed, created_at, updated_at`

func scanForge(row interface{ Scan(...any) error }) (Forge, error) {
	var f Forge
	var created, updated int64
	var slow sql.NullInt64
	err := row.Scan(&f.ID, &f.EnvironmentID, &f.Name, &f.Kind, &f.BaseURL, &f.Username, &f.Auth, &f.SecretID, &f.PublicKey, &f.HostKey,
		&f.HostKeyMismatch, &slow, &f.AgentHintDismissed, &created, &updated)
	f.CreatedAt, f.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	if slow.Valid {
		t := time.Unix(slow.Int64, 0)
		f.AgentSlowAt = &t
	}
	f.HostKeyFingerprint = fingerprint(f.HostKey)
	return f, err
}

// fingerprint is the SHA256 fingerprint of a host key in authorized_keys form.
func fingerprint(hostKey string) string {
	if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hostKey)); err == nil {
		return ssh.FingerprintSHA256(k)
	}
	return ""
}

// CreateForge inserts f and its token secret, which the caller sealed, in one transaction.
// The secret is stored studio-only. An ssh forge names its auth; with auth agent there is
// no secret and sec is ignored.
func (s *Store) CreateForge(ctx context.Context, f Forge, sec Secret) (Forge, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return f, err
	}
	defer tx.Rollback()
	t := now()
	f.SecretID = ""
	if f.Auth != ForgeAuthAgent {
		if err := insertForgeSecret(ctx, tx, f.EnvironmentID, sec, t); err != nil {
			return f, err
		}
		f.SecretID = sec.ID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO forges (id, environment_id, name, kind, base_url, username, auth, secret_id, public_key, host_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.EnvironmentID, f.Name, f.Kind, f.BaseURL, f.Username, f.Auth, nullable(f.SecretID), f.PublicKey, f.HostKey, t, t)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return f, fmt.Errorf("a forge named %q: %w", f.Name, ErrExists)
	}
	if err != nil {
		return f, err
	}
	f.CreatedAt, f.UpdatedAt = time.Unix(t, 0), time.Unix(t, 0)
	f.HostKeyFingerprint, f.HostKeyMismatch = fingerprint(f.HostKey), ""
	f.AgentSlowAt, f.AgentHintDismissed = nil, false
	return f, tx.Commit()
}

func insertForgeSecret(ctx context.Context, tx *sql.Tx, envID string, sec Secret, t int64) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO secrets ("+secretCols+", studio_only) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)",
		sec.ID, envID, sec.Name, sec.Sealed, strings.Join(sec.Hosts, ","), sec.Placeholder, sec.Note, t, t)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("a secret named %q: %w", sec.Name, ErrExists)
	}
	return err
}

// SetForgeKey switches an ssh forge from the SSH agent to a key Studio generated: sec holds
// the private key, which the caller sealed, and pub its public half. A forge that already
// has a key is ErrExists.
func (s *Store) SetForgeKey(ctx context.Context, envID, id, pub string, sec Secret) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	old, err := forgeSecret(ctx, tx, envID, id, "ssh")
	if err != nil {
		return err
	}
	if old != "" {
		return fmt.Errorf("the forge has a key: %w", ErrExists)
	}
	t := now()
	if err := insertForgeSecret(ctx, tx, envID, sec, t); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE forges SET auth = 'key', secret_id = ?, public_key = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		sec.ID, pub, t, envID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetForgeAgent switches an ssh forge to the host's SSH agent and deletes its key.
func (s *Store) SetForgeAgent(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	old, err := forgeSecret(ctx, tx, envID, id, "ssh")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE forges SET auth = 'agent', secret_id = NULL, public_key = '', updated_at = ? WHERE environment_id = ? AND id = ?",
		now(), envID, id); err != nil {
		return err
	}
	if old != "" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE environment_id = ? AND id = ?", envID, old); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// forgeSecret returns the secret ID of a forge, "" if it has none. With kind set, a forge of
// another kind is ErrNotFound.
func forgeSecret(ctx context.Context, tx *sql.Tx, envID, id, kind string) (string, error) {
	var secretID sql.NullString
	var k string
	err := tx.QueryRowContext(ctx, "SELECT secret_id, kind FROM forges WHERE environment_id = ? AND id = ?", envID, id).Scan(&secretID, &k)
	if errors.Is(err, sql.ErrNoRows) || err == nil && kind != "" && k != kind {
		return "", ErrNotFound
	}
	return secretID.String, err
}

// SetForgeAgentSlow records that the SSH agent took long to sign for a forge.
func (s *Store) SetForgeAgentSlow(ctx context.Context, envID, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE forges SET agent_slow_at = ? WHERE environment_id = ? AND id = ?", now(), envID, id)
	return err
}

// DismissForgeAgentHint hides the suggestion to use a Studio key for a forge.
func (s *Store) DismissForgeAgentHint(ctx context.Context, envID, id string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE forges SET agent_hint_dismissed = 1 WHERE environment_id = ? AND id = ?", envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAgentForges counts the agent forges in the catalog at path, opened read-only and not
// migrated, for `studio doctor` next to a running Studio. A catalog from before the agent
// existed is an error.
func CountAgentForges(ctx context.Context, path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM forges WHERE auth = 'agent'").Scan(&n)
	return n, err
}

// AgentForges counts the forges of all environments that sign with the host's SSH agent.
func (s *Store) AgentForges(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM forges WHERE auth = 'agent'").Scan(&n)
	return n, err
}

// PinForgeHostKey replaces the pinned host key of a forge and clears a recorded mismatch.
func (s *Store) PinForgeHostKey(ctx context.Context, envID, id, hostKey string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE forges SET host_key = ?, host_key_offered = '', updated_at = ? WHERE environment_id = ? AND id = ?",
		hostKey, now(), envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetForgeHostKeyOffered records the fingerprint of a host key that didn't match the pin.
func (s *Store) SetForgeHostKeyOffered(ctx context.Context, envID, id, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE forges SET host_key_offered = ? WHERE environment_id = ? AND id = ?", fingerprint, envID, id)
	return err
}

// Forges lists the forges of an environment by name.
func (s *Store) Forges(ctx context.Context, envID string) ([]Forge, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+forgeCols+" FROM forges WHERE environment_id = ? ORDER BY name", envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Forge{}
	for rows.Next() {
		f, err := scanForge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Forge returns one forge of an environment.
func (s *Store) Forge(ctx context.Context, envID, id string) (Forge, error) {
	f, err := scanForge(s.db.QueryRowContext(ctx, "SELECT "+forgeCols+" FROM forges WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// ForgeByName returns the forge of an environment with the given URL name.
func (s *Store) ForgeByName(ctx context.Context, envID, name string) (Forge, error) {
	f, err := scanForge(s.db.QueryRowContext(ctx, "SELECT "+forgeCols+" FROM forges WHERE environment_id = ? AND name = ?", envID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// DeleteForge removes a forge and its token or key, if any. Pushes keep their copy of its name.
func (s *Store) DeleteForge(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	secretID, err := forgeSecret(ctx, tx, envID, id, "")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM forges WHERE environment_id = ? AND id = ?", envID, id); err != nil {
		return err
	}
	if secretID != "" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE environment_id = ? AND id = ?", envID, secretID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetForgeSecret replaces the sealed value of a forge's secret, such as an expired token.
// The caller sealed it for the secret's ID.
func (s *Store) SetForgeSecret(ctx context.Context, envID, id string, sealed []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.ExecContext(ctx, `UPDATE secrets SET value = ?, updated_at = ?
		WHERE environment_id = ? AND id = (SELECT secret_id FROM forges WHERE environment_id = ? AND id = ?)`, sealed, t, envID, envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, "UPDATE forges SET updated_at = ? WHERE environment_id = ? AND id = ?", t, envID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SecretForge returns the name of the forge whose token a secret is, or "" if none.
func (s *Store) SecretForge(ctx context.Context, envID, secretID string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM forges WHERE environment_id = ? AND secret_id = ?", envID, secretID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// --- pushes ----------------------------------------------------------------------------

// Push states.
const (
	PushPending    = "pending"    // staged, waiting for the user
	PushPushed     = "pushed"     // approved and pushed upstream
	PushFailed     = "failed"     // approved, but the upstream push failed
	PushRejected   = "rejected"   // the user rejected it
	PushSuperseded = "superseded" // a later push to the same branch replaced it
)

// GitPush is a push a sandbox made to the virtual git remote.
type GitPush struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	SandboxID     string    `json:"sandboxId"`
	TeammateName  string    `json:"teammateName,omitempty"`
	ForgeID       string    `json:"forgeId"`
	ForgeName     string    `json:"forgeName"`
	Owner         string    `json:"owner"`
	Repo          string    `json:"repo"`
	Ref           string    `json:"ref"`
	OldSHA        string    `json:"oldSha"`
	NewSHA        string    `json:"newSha"`
	DefaultBranch string    `json:"defaultBranch,omitempty"`
	ApprovalID    string    `json:"approvalId"`
	State         string    `json:"state" enum:"pending,pushed,failed,rejected,superseded"`
	Result        string    `json:"result,omitempty" doc:"What the forge answered, or why the push failed"`
	Note          string    `json:"note,omitempty" doc:"The reviewer's note"`
	Delivered     bool      `json:"delivered" doc:"The sandbox has been told it was rejected or failed"`
	PRApprovalID  string    `json:"prApprovalId,omitempty"`
	PRURL         string    `json:"prUrl,omitempty"`
	PRResult      string    `json:"prResult,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const pushCols = `id, environment_id, sandbox_id, teammate_name, forge_id, forge_name, owner, repo, ref,
	old_sha, new_sha, default_branch, approval_id, state, result, note, delivered_at IS NOT NULL,
	pr_approval_id, pr_url, pr_result, created_at, updated_at`

func scanPush(row interface{ Scan(...any) error }) (GitPush, error) {
	var p GitPush
	var created, updated int64
	err := row.Scan(&p.ID, &p.EnvironmentID, &p.SandboxID, &p.TeammateName, &p.ForgeID, &p.ForgeName, &p.Owner, &p.Repo, &p.Ref,
		&p.OldSHA, &p.NewSHA, &p.DefaultBranch, &p.ApprovalID, &p.State, &p.Result, &p.Note, &p.Delivered,
		&p.PRApprovalID, &p.PRURL, &p.PRResult, &created, &updated)
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return p, err
}

// CreateGitPush inserts p, which carries its ID, as pending.
func (s *Store) CreateGitPush(ctx context.Context, p GitPush) (GitPush, error) {
	t := now()
	p.State = PushPending
	_, err := s.db.ExecContext(ctx, `INSERT INTO git_pushes (id, environment_id, sandbox_id, teammate_name, forge_id, forge_name,
		owner, repo, ref, old_sha, new_sha, default_branch, approval_id, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.EnvironmentID, p.SandboxID, p.TeammateName, p.ForgeID, p.ForgeName, p.Owner, p.Repo, p.Ref,
		p.OldSHA, p.NewSHA, p.DefaultBranch, p.ApprovalID, p.State, t, t)
	p.CreatedAt, p.UpdatedAt = time.Unix(t, 0), time.Unix(t, 0)
	return p, err
}

// GitPush returns one push of an environment.
func (s *Store) GitPush(ctx context.Context, envID, id string) (GitPush, error) {
	p, err := scanPush(s.db.QueryRowContext(ctx, "SELECT "+pushCols+" FROM git_pushes WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// GitPushByApproval returns the push a git.push or git.pr approval is about.
func (s *Store) GitPushByApproval(ctx context.Context, envID, approvalID string) (GitPush, error) {
	p, err := scanPush(s.db.QueryRowContext(ctx, "SELECT "+pushCols+` FROM git_pushes
		WHERE environment_id = ? AND (approval_id = ? OR pr_approval_id = ?)`, envID, approvalID, approvalID))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// PendingGitPushes lists the pending pushes of one sandbox to one branch.
func (s *Store) PendingGitPushes(ctx context.Context, envID, sandboxID, forgeID, owner, repo, ref string) ([]GitPush, error) {
	return s.gitPushes(ctx, `environment_id = ? AND sandbox_id = ? AND forge_id = ? AND owner = ? AND repo = ? AND ref = ?
		AND state = 'pending'`, envID, sandboxID, forgeID, owner, repo, ref)
}

// SandboxGitPushes lists a sandbox's pushes, newest first. ref, when set, keeps only the
// pushes to that branch (refs/heads/...).
func (s *Store) SandboxGitPushes(ctx context.Context, envID, sandboxID, ref string, limit int) ([]GitPush, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pushCols+` FROM git_pushes
		WHERE environment_id = ? AND sandbox_id = ? AND (? = '' OR ref = ?)
		ORDER BY created_at DESC, rowid DESC LIMIT ?`, envID, sandboxID, ref, ref, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GitPush{}
	for rows.Next() {
		p, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UndeliveredGitPushes lists the rejected or failed pushes of one sandbox to one
// repository that the sandbox has not been told about yet.
func (s *Store) UndeliveredGitPushes(ctx context.Context, envID, sandboxID, forgeID, owner, repo string) ([]GitPush, error) {
	return s.gitPushes(ctx, `environment_id = ? AND sandbox_id = ? AND forge_id = ? AND owner = ? AND repo = ?
		AND state IN ('rejected', 'failed') AND delivered_at IS NULL`, envID, sandboxID, forgeID, owner, repo)
}

func (s *Store) gitPushes(ctx context.Context, where string, args ...any) ([]GitPush, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pushCols+" FROM git_pushes WHERE "+where+" ORDER BY created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GitPush{}
	for rows.Next() {
		p, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SettleGitPush moves a push from state from to state to, recording result and note. It
// returns ErrNotFound unless the push was in state from, so only one caller settles it.
func (s *Store) SettleGitPush(ctx context.Context, envID, id, from, to, result, note string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE git_pushes SET state = ?, result = ?, note = ?, updated_at = ?
		WHERE environment_id = ? AND id = ? AND state = ?`, to, result, note, now(), envID, id, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkGitPushDelivered records that the sandbox was told about a rejected or failed push.
func (s *Store) MarkGitPushDelivered(ctx context.Context, envID, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE git_pushes SET delivered_at = ? WHERE environment_id = ? AND id = ? AND delivered_at IS NULL",
		now(), envID, id)
	return err
}

// SetGitPushPR records the pull request proposal of a push and, later, its outcome.
func (s *Store) SetGitPushPR(ctx context.Context, envID, id, approvalID, url, result string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE git_pushes SET pr_approval_id = ?, pr_url = ?, pr_result = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		approvalID, url, result, now(), envID, id)
	return err
}
