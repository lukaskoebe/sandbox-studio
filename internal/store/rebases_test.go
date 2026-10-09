package store

import (
	"context"
	"errors"
	"testing"
)

func TestRebaseLifecycle(t *testing.T) {
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
	other, err := s.CreateEnvironment(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	from := createReadyTemplate(t, ctx, s, env.ID, "1")
	to := createReadyTemplate(t, ctx, s, env.ID, "2")
	foreign := createReadyTemplate(t, ctx, s, other.ID, "3")
	creating, err := s.CreateTemplate(ctx, templateForEnvironment(env.ID, "4"))
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "box", TemplateID: from.ID, CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	want := RebaseOperation{SandboxID: sb.ID, EnvironmentID: env.ID, TemplateID: to.ID, CPUs: 2, MemoryMiB: 1024, MaxMemoryMiB: 2048, WorkspaceMiB: 4096, DockerMiB: 2048}

	if _, err := s.BeginRebase(ctx, RebaseOperation{SandboxID: sb.ID, EnvironmentID: env.ID, TemplateID: foreign.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("BeginRebase(foreign template) error = %v, want ErrNotFound", err)
	}
	if _, err := s.BeginRebase(ctx, RebaseOperation{SandboxID: sb.ID, EnvironmentID: env.ID, TemplateID: creating.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginRebase(creating template) error = %v, want ErrConflict", err)
	}
	op, err := s.BeginRebase(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if op.FromGeneration != 1 || op.ToGeneration != 2 {
		t.Fatalf("BeginRebase() generations = %d → %d", op.FromGeneration, op.ToGeneration)
	}
	if _, err := s.BeginRebase(ctx, want); !errors.Is(err, ErrConflict) {
		t.Fatalf("second BeginRebase() error = %v, want ErrConflict", err)
	}
	cp, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, "cp")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginRestore() during rebase error = %v, want ErrConflict", err)
	}
	// The pending rebase holds its target template.
	if err := s.SetTemplateDeleting(ctx, env.ID, to.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("SetTemplateDeleting(rebase target) error = %v, want ErrConflict", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE templates SET state = ? WHERE id = ?", TemplateStateDeleting, to.ID); err == nil {
		t.Fatal("database allowed a rebase target to become deleting")
	}
	ops, err := s.Rebases(ctx)
	if err != nil || len(ops) != 1 || ops[0] != op {
		t.Fatalf("Rebases() = %+v, %v", ops, err)
	}

	stale := op
	stale.CPUs = 8
	if err := s.CommitRebase(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("CommitRebase(mismatched) error = %v, want ErrConflict", err)
	}
	if err := s.CommitRebase(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitRebase(ctx, op); err != nil {
		t.Fatalf("repeated CommitRebase() error = %v", err)
	}
	got, err := s.Sandbox(ctx, env.ID, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 2 || got.TemplateID != to.ID || got.CPUs != 2 || got.MemoryMiB != 1024 || got.MaxMemoryMiB != 2048 || got.WorkspaceMiB != 4096 || got.DockerMiB != 2048 {
		t.Fatalf("sandbox after CommitRebase() = %+v", got)
	}
	// The old template is free once the sandbox has moved.
	if err := s.SetTemplateDeleting(ctx, env.ID, from.ID); err != nil {
		t.Fatalf("SetTemplateDeleting(old template) error = %v", err)
	}
	if err := s.EndRebase(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := s.EndRebase(ctx, op); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated EndRebase() error = %v, want ErrNotFound", err)
	}
	if ops, err := s.Rebases(ctx); err != nil || len(ops) != 0 {
		t.Fatalf("Rebases() after end = %+v, %v", ops, err)
	}
}

func TestRebaseRefusedDuringRestore(t *testing.T) {
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
	to := createReadyTemplate(t, ctx, s, env.ID, "2")
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "box", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, "cp")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginRebase(ctx, RebaseOperation{SandboxID: sb.ID, EnvironmentID: env.ID, TemplateID: to.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginRebase() during restore error = %v, want ErrConflict", err)
	}
}
