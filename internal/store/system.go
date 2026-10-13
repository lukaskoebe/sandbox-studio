package store

import (
	"context"
	"database/sql"
	"errors"
)

// System sandboxes are the short-lived VMs Studio runs for a subscription provider: one per
// login attempt, and one long-lived utility VM for one-shot CLI calls (docs/teammates.md,
// subscription logins). Like browser VMs they are catalog rows that the sandbox API hides, and
// their kind decides which hosts they may reach (policy.SystemHosts).
const (
	SandboxKindClaudeLogin   = "claude-login"
	SandboxKindCodexLogin    = "codex-login"
	SandboxKindClaudeUtility = "claude-utility"
	SandboxKindCodexUtility  = "codex-utility"
)

// IsSystemKind reports whether kind is one of the system sandbox kinds.
func IsSystemKind(kind string) bool {
	switch kind {
	case SandboxKindClaudeLogin, SandboxKindCodexLogin, SandboxKindClaudeUtility, SandboxKindCodexUtility:
		return true
	}
	return false
}

// CreateSystemSandbox inserts the row of a system VM of kind. Its name is unique in the
// environment like any sandbox's, so a second VM of the same name is ErrExists.
func (s *Store) CreateSystemSandbox(ctx context.Context, sb Sandbox, kind string) (Sandbox, error) {
	if !IsSystemKind(kind) {
		return sb, errors.New("not a system sandbox kind: " + kind)
	}
	sb.Kind, sb.TemplateID, sb.TeammateID = kind, "", ""
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sb, err
	}
	defer tx.Rollback()
	if sb, err = insertSandbox(ctx, tx, sb); err != nil {
		return sb, err
	}
	return sb, tx.Commit()
}

// SystemSandbox returns the system VM of kind and name in an environment.
func (s *Store) SystemSandbox(ctx context.Context, envID, kind, name string) (Sandbox, error) {
	sb, err := scanSandbox(s.db.QueryRowContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE environment_id = ? AND kind = ? AND name = ?", envID, kind, name))
	if errors.Is(err, sql.ErrNoRows) {
		return sb, ErrNotFound
	}
	return sb, err
}

// SystemSandboxes lists every system VM row, of every environment.
func (s *Store) SystemSandboxes(ctx context.Context) ([]Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE kind IN ('claude-login', 'codex-login', 'claude-utility', 'codex-utility') ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sandbox{}
	for rows.Next() {
		sb, err := scanSandbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}
