package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrConflict reports a state conflict, such as an in-progress restore or a failed
// generation compare-and-swap.
var ErrConflict = errors.New("conflict")

const (
	CheckpointStateCreating = "creating"
	CheckpointStateReady    = "ready"
	CheckpointStateDeleting = "deleting"
)

// Checkpoint is a durable snapshot record. Its runtime reference is derived by the
// runtime package and is intentionally not persisted or exposed here.
type Checkpoint struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	SandboxID     string    `json:"sandboxId"`
	Name          string    `json:"name"`
	State         string    `json:"state" enum:"creating,ready,deleting"`
	Generation    int       `json:"generation"`
	CreatedAt     time.Time `json:"createdAt"`
}

const checkpointCols = "id, environment_id, sandbox_id, name, state, generation, created_at"

func scanCheckpoint(row interface{ Scan(...any) error }) (Checkpoint, error) {
	var cp Checkpoint
	var created int64
	err := row.Scan(&cp.ID, &cp.EnvironmentID, &cp.SandboxID, &cp.Name, &cp.State, &cp.Generation, &created)
	cp.CreatedAt = time.Unix(created, 0)
	return cp, err
}

// CreateCheckpoint inserts a creating checkpoint for the sandbox's current generation.
func (s *Store) CreateCheckpoint(ctx context.Context, envID, sandboxID, name string) (Checkpoint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Checkpoint{}, err
	}
	defer tx.Rollback()

	var generation int
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM sandboxes WHERE environment_id = ? AND id = ?", envID, sandboxID).Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Checkpoint{}, ErrNotFound
		}
		return Checkpoint{}, err
	}
	cp := Checkpoint{
		ID: NewID(), EnvironmentID: envID, SandboxID: sandboxID, Name: name,
		State: CheckpointStateCreating, Generation: generation, CreatedAt: time.Unix(now(), 0),
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO checkpoints ("+checkpointCols+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		cp.ID, cp.EnvironmentID, cp.SandboxID, cp.Name, cp.State, cp.Generation, cp.CreatedAt.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Checkpoint{}, fmt.Errorf("a checkpoint named %q: %w", name, ErrExists)
		}
		return Checkpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// Checkpoints lists checkpoint records for a sandbox, including records whose runtime
// artifacts are still being created or deleted.
func (s *Store) Checkpoints(ctx context.Context, envID, sandboxID string) ([]Checkpoint, error) {
	if _, err := s.Sandbox(ctx, envID, sandboxID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+checkpointCols+" FROM checkpoints WHERE environment_id = ? AND sandbox_id = ? ORDER BY created_at, id", envID, sandboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Checkpoint{}
	for rows.Next() {
		cp, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}

// Checkpoint returns one checkpoint owned by the specified sandbox and environment.
func (s *Store) Checkpoint(ctx context.Context, envID, sandboxID, checkpointID string) (Checkpoint, error) {
	cp, err := scanCheckpoint(s.db.QueryRowContext(ctx, "SELECT "+checkpointCols+" FROM checkpoints WHERE environment_id = ? AND sandbox_id = ? AND id = ?", envID, sandboxID, checkpointID))
	if errors.Is(err, sql.ErrNoRows) {
		return cp, ErrNotFound
	}
	return cp, err
}

// SetCheckpointState changes a checkpoint's lifecycle state.
func (s *Store) SetCheckpointState(ctx context.Context, envID, sandboxID, checkpointID, state string) error {
	if !validCheckpointState(state) {
		return fmt.Errorf("invalid checkpoint state %q", state)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if state == CheckpointStateDeleting {
		var inUse int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM restores WHERE environment_id = ? AND sandbox_id = ? AND checkpoint_id = ?", envID, sandboxID, checkpointID).Scan(&inUse)
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, "UPDATE checkpoints SET state = ? WHERE environment_id = ? AND sandbox_id = ? AND id = ?", state, envID, sandboxID, checkpointID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func validCheckpointState(state string) bool {
	switch state {
	case CheckpointStateCreating, CheckpointStateReady, CheckpointStateDeleting:
		return true
	default:
		return false
	}
}

// DeleteCheckpoint removes a checkpoint record after its runtime artifact is gone. An
// operation that still needs the checkpoint prevents deletion until cleanup completes.
func (s *Store) DeleteCheckpoint(ctx context.Context, envID, sandboxID, checkpointID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM sandboxes WHERE environment_id = ? AND id = ?", envID, sandboxID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var inUse int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM restores WHERE environment_id = ? AND sandbox_id = ? AND checkpoint_id = ?", envID, sandboxID, checkpointID).Scan(&inUse)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM checkpoints WHERE environment_id = ? AND sandbox_id = ? AND id = ?", envID, sandboxID, checkpointID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// RestoreOperation records the catalog transition and cleanup identity for a restore.
type RestoreOperation struct {
	SandboxID      string `json:"sandboxId"`
	EnvironmentID  string `json:"environmentId"`
	CheckpointID   string `json:"checkpointId"`
	FromGeneration int    `json:"fromGeneration"`
	ToGeneration   int    `json:"toGeneration"`
}

const restoreCols = "sandbox_id, environment_id, checkpoint_id, from_generation, to_generation"

func scanRestore(row interface{ Scan(...any) error }) (RestoreOperation, error) {
	var op RestoreOperation
	err := row.Scan(&op.SandboxID, &op.EnvironmentID, &op.CheckpointID, &op.FromGeneration, &op.ToGeneration)
	return op, err
}

// BeginRestore reserves the next generation for a ready checkpoint. The sandbox's
// catalog generation remains at FromGeneration until CommitRestore adopts the candidate.
func (s *Store) BeginRestore(ctx context.Context, envID, sandboxID, checkpointID string) (RestoreOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RestoreOperation{}, err
	}
	defer tx.Rollback()

	var generation int
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM sandboxes WHERE environment_id = ? AND id = ?", envID, sandboxID).Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RestoreOperation{}, ErrNotFound
		}
		return RestoreOperation{}, err
	}
	var state string
	err = tx.QueryRowContext(ctx, "SELECT state FROM checkpoints WHERE environment_id = ? AND sandbox_id = ? AND id = ?", envID, sandboxID, checkpointID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return RestoreOperation{}, ErrNotFound
	}
	if err != nil {
		return RestoreOperation{}, err
	}
	if state != CheckpointStateReady {
		return RestoreOperation{}, ErrConflict
	}
	var pending int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM restores WHERE environment_id = ? AND sandbox_id = ?", envID, sandboxID).Scan(&pending)
	if err == nil {
		return RestoreOperation{}, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RestoreOperation{}, err
	}
	op := RestoreOperation{SandboxID: sandboxID, EnvironmentID: envID, CheckpointID: checkpointID, FromGeneration: generation, ToGeneration: generation + 1}
	if _, err := tx.ExecContext(ctx, "INSERT INTO restores ("+restoreCols+") VALUES (?, ?, ?, ?, ?)", op.SandboxID, op.EnvironmentID, op.CheckpointID, op.FromGeneration, op.ToGeneration); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return RestoreOperation{}, ErrConflict
		}
		return RestoreOperation{}, err
	}
	if err := tx.Commit(); err != nil {
		return RestoreOperation{}, err
	}
	return op, nil
}

// Restores lists pending operations, including catalog-adopted operations that still
// need runtime cleanup. It is for startup recovery and spans environments by design.
func (s *Store) Restores(ctx context.Context) ([]RestoreOperation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+restoreCols+" FROM restores ORDER BY environment_id, sandbox_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RestoreOperation{}
	for rows.Next() {
		op, err := scanRestore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// CommitRestore adopts the candidate generation with a compare-and-swap. The operation
// record remains until EndRestore confirms the obsolete runtime was removed.
func (s *Store) CommitRestore(ctx context.Context, op RestoreOperation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := matchRestore(ctx, tx, op); err != nil {
		return err
	}
	var generation int
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM sandboxes WHERE environment_id = ? AND id = ?", op.EnvironmentID, op.SandboxID).Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if generation == op.ToGeneration {
		return tx.Commit()
	}
	if generation != op.FromGeneration {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "UPDATE sandboxes SET generation = ? WHERE environment_id = ? AND id = ? AND generation = ?", op.ToGeneration, op.EnvironmentID, op.SandboxID, op.FromGeneration)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// EndRestore removes a matching operation after rollback or obsolete-runtime cleanup.
// The sandbox generation must still identify either side of this operation.
func (s *Store) EndRestore(ctx context.Context, op RestoreOperation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := matchRestore(ctx, tx, op); err != nil {
		return err
	}
	var generation int
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM sandboxes WHERE environment_id = ? AND id = ?", op.EnvironmentID, op.SandboxID).Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if generation != op.FromGeneration && generation != op.ToGeneration {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM restores WHERE "+restoreColsWhere(), op.SandboxID, op.EnvironmentID, op.CheckpointID, op.FromGeneration, op.ToGeneration)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

func matchRestore(ctx context.Context, tx *sql.Tx, op RestoreOperation) error {
	got, err := scanRestore(tx.QueryRowContext(ctx, "SELECT "+restoreCols+" FROM restores WHERE environment_id = ? AND sandbox_id = ?", op.EnvironmentID, op.SandboxID))
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

func restoreColsWhere() string {
	return "sandbox_id = ? AND environment_id = ? AND checkpoint_id = ? AND from_generation = ? AND to_generation = ?"
}
