package store

import (
	"context"
	"errors"
	"testing"
)

func TestCheckpointsAreSandboxAndEnvironmentScoped(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	work, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateEnvironment(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.CreateCheckpoint(ctx, work.ID, sb.ID, "before-edit")
	if err != nil || cp.Generation != sb.Generation || cp.State != CheckpointStateCreating {
		t.Fatalf("created checkpoint %+v: %v", cp, err)
	}
	if _, err := s.Checkpoint(ctx, other.ID, sb.ID, cp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment checkpoint lookup: %v", err)
	}
	if _, err := s.Checkpoints(ctx, other.ID, sb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment checkpoint list: %v", err)
	}
	if _, err := s.CreateCheckpoint(ctx, other.ID, sb.ID, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment checkpoint create: %v", err)
	}
	if _, err := s.BeginRestore(ctx, other.ID, sb.ID, cp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment restore begin: %v", err)
	}
	if err := s.SetCheckpointState(ctx, work.ID, sb.ID, cp.ID, "unknown"); err == nil {
		t.Fatal("accepted unknown checkpoint state")
	}
	if err := s.SetCheckpointState(ctx, work.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	got, err := s.Checkpoint(ctx, work.ID, sb.ID, cp.ID)
	if err != nil || got.State != CheckpointStateReady || got.ID != cp.ID {
		t.Fatalf("checkpoint after state change %+v: %v", got, err)
	}
	list, err := s.Checkpoints(ctx, work.ID, sb.ID)
	if err != nil || len(list) != 1 || list[0].ID != cp.ID {
		t.Fatalf("checkpoint list %+v: %v", list, err)
	}
	if _, err := s.CreateCheckpoint(ctx, work.ID, sb.ID, cp.Name); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate checkpoint name: %v", err)
	}
}

func TestRestoreOperationCASAndCleanup(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore from non-ready checkpoint: %v", err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	op, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID)
	if err != nil || op.FromGeneration != 1 || op.ToGeneration != 2 {
		t.Fatalf("begin restore %+v: %v", op, err)
	}
	if _, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("second pending restore: %v", err)
	}
	if err := s.DeleteCheckpoint(ctx, env.ID, sb.ID, cp.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted checkpoint during restore: %v", err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateDeleting); !errors.Is(err, ErrConflict) {
		t.Fatalf("marked checkpoint deleting during restore: %v", err)
	}
	ops, err := s.Restores(ctx)
	if err != nil || len(ops) != 1 || ops[0] != op {
		t.Fatalf("pending restores %+v: %v", ops, err)
	}
	if err := s.CommitRestore(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitRestore(ctx, op); err != nil {
		t.Fatalf("commit was not idempotent: %v", err)
	}
	got, err := s.Sandbox(ctx, env.ID, sb.ID)
	if err != nil || got.Generation != op.ToGeneration {
		t.Fatalf("sandbox after commit %+v: %v", got, err)
	}
	if err := s.EndRestore(ctx, op); err != nil {
		t.Fatal(err)
	}
	if ops, err := s.Restores(ctx); err != nil || len(ops) != 0 {
		t.Fatalf("restore after cleanup %+v: %v", ops, err)
	}
	if err := s.EndRestore(ctx, op); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ending an absent operation: %v", err)
	}
	if err := s.DeleteCheckpoint(ctx, env.ID, sb.ID, cp.ID); err != nil {
		t.Fatalf("delete checkpoint after cleanup: %v", err)
	}
}

func TestEndRestoreRollsBackReservationWithoutChangingGeneration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	op, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndRestore(ctx, op); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sandbox(ctx, env.ID, sb.ID)
	if err != nil || got.Generation != op.FromGeneration {
		t.Fatalf("sandbox after rollback %+v: %v", got, err)
	}
	if _, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID); err != nil {
		t.Fatalf("new restore after rollback: %v", err)
	}
}

func TestCommitRestoreRejectsUnexpectedGenerationAndRetainsOperation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	op, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE sandboxes SET generation = ? WHERE environment_id = ? AND id = ?", op.ToGeneration+1, env.ID, sb.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitRestore(ctx, op); !errors.Is(err, ErrConflict) {
		t.Fatalf("committed over an unexpected generation: %v", err)
	}
	if ops, err := s.Restores(ctx); err != nil || len(ops) != 1 || ops[0] != op {
		t.Fatalf("failed CAS discarded recovery record: %+v %v", ops, err)
	}
	if err := s.EndRestore(ctx, op); !errors.Is(err, ErrConflict) {
		t.Fatalf("ended restore against unexpected generation: %v", err)
	}
}
