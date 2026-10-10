package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// --- forges ----------------------------------------------------------------------------

// Forge is a git host an environment's sandboxes push to through the virtual git remote.
// Its token is a studio-only secret: sandboxes never see it.
type Forge struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	Name          string    `json:"name" doc:"The URL segment: https://git.studio.internal/<name>/<owner>/<repo>.git"`
	Kind          string    `json:"kind" enum:"forgejo,github"`
	BaseURL       string    `json:"baseUrl"`
	SecretID      string    `json:"secretId" doc:"The studio-only vault secret holding the token"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const forgeCols = "id, environment_id, name, kind, base_url, secret_id, created_at, updated_at"

func scanForge(row interface{ Scan(...any) error }) (Forge, error) {
	var f Forge
	var created, updated int64
	err := row.Scan(&f.ID, &f.EnvironmentID, &f.Name, &f.Kind, &f.BaseURL, &f.SecretID, &created, &updated)
	f.CreatedAt, f.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return f, err
}

// CreateForge inserts f and its token secret, which the caller sealed, in one transaction.
// The secret is stored studio-only.
func (s *Store) CreateForge(ctx context.Context, f Forge, sec Secret) (Forge, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return f, err
	}
	defer tx.Rollback()
	t := now()
	_, err = tx.ExecContext(ctx, "INSERT INTO secrets ("+secretCols+", studio_only) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)",
		sec.ID, f.EnvironmentID, sec.Name, sec.Sealed, strings.Join(sec.Hosts, ","), sec.Placeholder, sec.Note, t, t)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return f, fmt.Errorf("a secret named %q: %w", sec.Name, ErrExists)
	}
	if err != nil {
		return f, err
	}
	f.SecretID = sec.ID
	_, err = tx.ExecContext(ctx, "INSERT INTO forges ("+forgeCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		f.ID, f.EnvironmentID, f.Name, f.Kind, f.BaseURL, f.SecretID, t, t)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return f, fmt.Errorf("a forge named %q: %w", f.Name, ErrExists)
	}
	if err != nil {
		return f, err
	}
	f.CreatedAt, f.UpdatedAt = time.Unix(t, 0), time.Unix(t, 0)
	return f, tx.Commit()
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

// DeleteForge removes a forge and its token. Pushes keep their copy of its name.
func (s *Store) DeleteForge(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var secretID string
	err = tx.QueryRowContext(ctx, "SELECT secret_id FROM forges WHERE environment_id = ? AND id = ?", envID, id).Scan(&secretID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM forges WHERE environment_id = ? AND id = ?", envID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE environment_id = ? AND id = ?", envID, secretID); err != nil {
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
	PersonaName   string    `json:"personaName,omitempty"`
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

const pushCols = `id, environment_id, sandbox_id, persona_name, forge_id, forge_name, owner, repo, ref,
	old_sha, new_sha, default_branch, approval_id, state, result, note, delivered_at IS NOT NULL,
	pr_approval_id, pr_url, pr_result, created_at, updated_at`

func scanPush(row interface{ Scan(...any) error }) (GitPush, error) {
	var p GitPush
	var created, updated int64
	err := row.Scan(&p.ID, &p.EnvironmentID, &p.SandboxID, &p.PersonaName, &p.ForgeID, &p.ForgeName, &p.Owner, &p.Repo, &p.Ref,
		&p.OldSHA, &p.NewSHA, &p.DefaultBranch, &p.ApprovalID, &p.State, &p.Result, &p.Note, &p.Delivered,
		&p.PRApprovalID, &p.PRURL, &p.PRResult, &created, &updated)
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return p, err
}

// CreateGitPush inserts p, which carries its ID, as pending.
func (s *Store) CreateGitPush(ctx context.Context, p GitPush) (GitPush, error) {
	t := now()
	p.State = PushPending
	_, err := s.db.ExecContext(ctx, `INSERT INTO git_pushes (id, environment_id, sandbox_id, persona_name, forge_id, forge_name,
		owner, repo, ref, old_sha, new_sha, default_branch, approval_id, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.EnvironmentID, p.SandboxID, p.PersonaName, p.ForgeID, p.ForgeName, p.Owner, p.Repo, p.Ref,
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
