package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// RebaseOperation records a move of a sandbox to another template. The catalog keeps
// FromGeneration until CommitRebase adopts ToGeneration, the template and its resources.
type RebaseOperation struct {
	SandboxID      string
	EnvironmentID  string
	TemplateID     string
	FromGeneration int
	ToGeneration   int
	CPUs           int
	MemoryMiB      int
	MaxMemoryMiB   int
	WorkspaceMiB   int
	DockerMiB      int
}

const rebaseCols = "sandbox_id, environment_id, template_id, from_generation, to_generation, cpus, memory_mib, max_memory_mib, workspace_mib, docker_mib"

func scanRebase(row interface{ Scan(...any) error }) (RebaseOperation, error) {
	var op RebaseOperation
	err := row.Scan(&op.SandboxID, &op.EnvironmentID, &op.TemplateID, &op.FromGeneration, &op.ToGeneration,
		&op.CPUs, &op.MemoryMiB, &op.MaxMemoryMiB, &op.WorkspaceMiB, &op.DockerMiB)
	return op, err
}

// BeginRebase records a rebase of op.SandboxID onto op.TemplateID, which must be ready
// and in the same environment. It reserves the next generation and returns the record.
// It fails with ErrConflict while another rebase or a restore is pending.
func (s *Store) BeginRebase(ctx context.Context, op RebaseOperation) (RebaseOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RebaseOperation{}, err
	}
	defer tx.Rollback()

	var generation int
	err = tx.QueryRowContext(ctx, "SELECT generation FROM sandboxes WHERE environment_id = ? AND id = ? AND build_job_id IS NULL", op.EnvironmentID, op.SandboxID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return RebaseOperation{}, ErrNotFound
	}
	if err != nil {
		return RebaseOperation{}, err
	}
	var state string
	err = tx.QueryRowContext(ctx, "SELECT state FROM templates WHERE environment_id = ? AND id = ?", op.EnvironmentID, op.TemplateID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return RebaseOperation{}, ErrNotFound
	}
	if err != nil {
		return RebaseOperation{}, err
	}
	if state != TemplateStateReady {
		return RebaseOperation{}, ErrConflict
	}
	var pending int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM restores WHERE sandbox_id = ?) OR EXISTS (SELECT 1 FROM rebases WHERE sandbox_id = ?)", op.SandboxID, op.SandboxID).Scan(&pending); err != nil {
		return RebaseOperation{}, err
	}
	if pending != 0 {
		return RebaseOperation{}, ErrConflict
	}
	op.FromGeneration, op.ToGeneration = generation, generation+1
	if _, err := tx.ExecContext(ctx, "INSERT INTO rebases ("+rebaseCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		op.SandboxID, op.EnvironmentID, op.TemplateID, op.FromGeneration, op.ToGeneration,
		op.CPUs, op.MemoryMiB, op.MaxMemoryMiB, op.WorkspaceMiB, op.DockerMiB); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return RebaseOperation{}, ErrConflict
		}
		return RebaseOperation{}, err
	}
	if err := tx.Commit(); err != nil {
		return RebaseOperation{}, err
	}
	return op, nil
}

// Rebases lists pending operations for startup recovery, across environments.
func (s *Store) Rebases(ctx context.Context) ([]RebaseOperation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+rebaseCols+" FROM rebases ORDER BY environment_id, sandbox_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RebaseOperation{}
	for rows.Next() {
		op, err := scanRebase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// CommitRebase switches the sandbox to the new generation, template and resources with a
// compare-and-swap on the generation. Repeating it after it took effect is safe.
func (s *Store) CommitRebase(ctx context.Context, op RebaseOperation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := matchRebase(ctx, tx, op); err != nil {
		return err
	}
	generation, err := sandboxGeneration(ctx, tx, op.EnvironmentID, op.SandboxID)
	if err != nil {
		return err
	}
	if generation == op.ToGeneration {
		return tx.Commit()
	}
	if generation != op.FromGeneration {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE sandboxes SET generation = ?, template_id = ?, cpus = ?, memory_mib = ?, max_memory_mib = ?, workspace_mib = ?, docker_mib = ?
		WHERE environment_id = ? AND id = ? AND generation = ?`,
		op.ToGeneration, op.TemplateID, op.CPUs, op.MemoryMiB, op.MaxMemoryMiB, op.WorkspaceMiB, op.DockerMiB,
		op.EnvironmentID, op.SandboxID, op.FromGeneration)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// EndRebase removes a matching operation after rollback or obsolete-VM cleanup.
func (s *Store) EndRebase(ctx context.Context, op RebaseOperation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := matchRebase(ctx, tx, op); err != nil {
		return err
	}
	generation, err := sandboxGeneration(ctx, tx, op.EnvironmentID, op.SandboxID)
	if err != nil {
		return err
	}
	if generation != op.FromGeneration && generation != op.ToGeneration {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM rebases WHERE environment_id = ? AND sandbox_id = ?", op.EnvironmentID, op.SandboxID); err != nil {
		return err
	}
	return tx.Commit()
}

func matchRebase(ctx context.Context, tx *sql.Tx, op RebaseOperation) error {
	got, err := scanRebase(tx.QueryRowContext(ctx, "SELECT "+rebaseCols+" FROM rebases WHERE environment_id = ? AND sandbox_id = ?", op.EnvironmentID, op.SandboxID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if got != op {
		return ErrConflict
	}
	return nil
}

func sandboxGeneration(ctx context.Context, tx *sql.Tx, envID, id string) (int, error) {
	var generation int
	err := tx.QueryRowContext(ctx, "SELECT generation FROM sandboxes WHERE environment_id = ? AND id = ?", envID, id).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return generation, err
}
