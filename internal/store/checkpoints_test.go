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

// A schedule is per sandbox and goes with it; its checkpoints are marked auto until renamed.
func TestCheckpointSchedules(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, _ := s.CreateEnvironment(ctx, "work")
	other, _ := s.CreateEnvironment(ctx, "other")
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if cs, err := s.CheckpointSchedule(ctx, env.ID, sb.ID); err != nil || cs.EveryHours != 0 {
		t.Fatalf("no schedule: %+v %v", cs, err)
	}
	if err := s.SetCheckpointSchedule(ctx, other.ID, sb.ID, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("schedule in another environment: %v", err)
	}
	if err := s.SetCheckpointSchedule(ctx, env.ID, sb.ID, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCheckpointScheduleNote(ctx, env.ID, sb.ID, "the sandbox is suspended"); err != nil {
		t.Fatal(err)
	}
	if all, err := s.CheckpointSchedules(ctx); err != nil || len(all) != 1 || all[0].EveryHours != 4 || all[0].Note != "the sandbox is suspended" || all[0].EnvironmentID != env.ID {
		t.Fatalf("schedules %+v %v", all, err)
	}
	// Changing the interval forgets the note.
	if err := s.SetCheckpointSchedule(ctx, env.ID, sb.ID, 24); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.CheckpointSchedule(ctx, env.ID, sb.ID); cs.EveryHours != 24 || cs.Note != "" {
		t.Fatalf("changed schedule %+v", cs)
	}

	auto, err := s.CreateAutoCheckpoint(ctx, env.ID, sb.ID, "Auto 1")
	if err != nil || !auto.Auto {
		t.Fatalf("auto checkpoint %+v %v", auto, err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, auto.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Checkpoint(ctx, env.ID, sb.ID, auto.ID); !got.Auto {
		t.Fatal("the auto mark was not stored")
	}
	if renamed, err := s.RenameCheckpoint(ctx, env.ID, sb.ID, auto.ID, "keep this"); err != nil || renamed.Auto {
		t.Fatalf("renamed %+v %v", renamed, err)
	}
	if got, _ := s.Checkpoint(ctx, env.ID, sb.ID, auto.ID); got.Auto {
		t.Fatal("a renamed checkpoint is still the schedule's")
	}

	if err := s.SetCheckpointSchedule(ctx, env.ID, sb.ID, 0); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.CheckpointSchedules(ctx); len(all) != 0 {
		t.Fatalf("schedule off, still listed: %+v", all)
	}
	_ = s.SetCheckpointSchedule(ctx, env.ID, sb.ID, 1)
	if err := s.DeleteSandbox(ctx, env.ID, sb.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.CheckpointSchedules(ctx); len(all) != 0 {
		t.Fatalf("a deleted sandbox's schedule stayed: %+v", all)
	}
}
