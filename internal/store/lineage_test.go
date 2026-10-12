package store

import (
	"context"
	"errors"
	"testing"
)

// Events record in order, survive their sandbox's deletion as a tombstone, and never leak
// across environments.
func TestSandboxEventsOutliveTheSandbox(t *testing.T) {
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
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "api", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []SandboxEvent{
		{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: EventCreate, Generation: 1, Name: "api", CreatedAt: sb.CreatedAt},
		{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: EventCheckpoint, Generation: 1, Name: "api", CheckpointID: "cp", CheckpointName: "snap", CreatedAt: sb.CreatedAt},
		{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: EventDelete, Generation: 1, Name: "api", OtherSandboxID: "parent"},
	} {
		if err := s.RecordSandboxEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteSandbox(ctx, env.ID, sb.ID); err != nil {
		t.Fatal(err)
	}

	events, err := s.SandboxEvents(ctx, env.ID, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != EventCreate || events[1].CheckpointName != "snap" || events[2].Kind != EventDelete {
		t.Fatalf("events after delete = %+v", events)
	}
	if events[2].OtherSandboxID != "parent" || events[2].Name != "api" || events[0].ID >= events[1].ID {
		t.Fatalf("tombstone or order lost: %+v", events)
	}
	tombstone, err := s.DeleteEvent(ctx, env.ID, sb.ID)
	if err != nil || tombstone.ID != events[2].ID {
		t.Fatalf("DeleteEvent() = %+v, %v", tombstone, err)
	}
	if _, err := s.DeleteEvent(ctx, env.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteEvent(missing) error = %v, want ErrNotFound", err)
	}
	tombstones, err := s.DeleteEvents(ctx, env.ID)
	if err != nil || len(tombstones) != 1 || tombstones[0].SandboxID != sb.ID {
		t.Fatalf("DeleteEvents() = %+v, %v", tombstones, err)
	}
	if list, err := s.DeleteEvents(ctx, other.ID); err != nil || len(list) != 0 {
		t.Fatalf("DeleteEvents(other environment) = %+v, %v", list, err)
	}
	if list, err := s.SandboxEvents(ctx, other.ID, sb.ID); err != nil || len(list) != 0 {
		t.Fatalf("SandboxEvents(other environment) = %+v, %v", list, err)
	}
}

// A rebase and a restore record their event in the same transaction as the generation
// switch, so a repeated commit records nothing new and a refused one records nothing.
func TestRebaseAndRestoreRecordEventsWithTheSwitch(t *testing.T) {
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
	from := createReadyTemplate(t, ctx, s, env.ID, "1")
	to := createReadyTemplate(t, ctx, s, env.ID, "2")
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "box", TemplateID: from.ID, CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}

	rebase, err := s.BeginRebase(ctx, RebaseOperation{SandboxID: sb.ID, EnvironmentID: env.ID, TemplateID: to.ID, CPUs: 2, MemoryMiB: 1024, MaxMemoryMiB: 1024, WorkspaceMiB: 2048, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitRebase(ctx, rebase); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitRebase(ctx, rebase); err != nil {
		t.Fatal(err)
	}
	if err := s.EndRebase(ctx, rebase); err != nil {
		t.Fatal(err)
	}
	events, err := s.SandboxEvents(ctx, env.ID, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events after a repeated commit = %+v", events)
	}
	if ev := events[0]; ev.Kind != EventRebase || ev.Generation != 2 || ev.Name != "box" || ev.TemplateID != to.ID || ev.FromTemplateID != from.ID {
		t.Fatalf("rebase event = %+v", ev)
	}

	cp, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	restore, err := s.BeginRestore(ctx, env.ID, sb.ID, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitRestore(ctx, RestoreOperation{SandboxID: sb.ID, EnvironmentID: env.ID, CheckpointID: cp.ID, FromGeneration: 1, ToGeneration: 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("CommitRestore(stale) error = %v, want ErrConflict", err)
	}
	if err := s.CommitRestore(ctx, restore); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitRestore(ctx, restore); err != nil {
		t.Fatal(err)
	}
	events, err = s.SandboxEvents(ctx, env.ID, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events after restore = %+v", events)
	}
	if ev := events[1]; ev.Kind != EventRestore || ev.Generation != 3 || ev.TemplateID != to.ID || ev.CheckpointID != cp.ID || ev.CheckpointName != "before" {
		t.Fatalf("restore event = %+v", ev)
	}
}

// A fork keeps its source, generation and time, and the source may be deleted without
// losing the reference.
func TestForkReferenceSurvivesTheSourceDeletion(t *testing.T) {
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
	source, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "source", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "fork", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024,
		ForkedFromID: source.ID, ForkedFromGeneration: 4})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "plain", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Sandbox(ctx, env.ID, fork.ID); err != nil || got.ForkedFromID != source.ID || got.ForkedFromGeneration != 4 ||
		got.ForkedAt.IsZero() || got.ForkedAt.Unix() != got.CreatedAt.Unix() {
		t.Fatalf("fork = %+v, %v", got, err)
	}
	if got, err := s.Sandbox(ctx, env.ID, plain.ID); err != nil || got.ForkedFromID != "" || !got.ForkedAt.IsZero() {
		t.Fatalf("plain sandbox = %+v, %v", got, err)
	}
	if err := s.DeleteSandbox(ctx, env.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Sandbox(ctx, env.ID, fork.ID); err != nil || got.ForkedFromID != source.ID {
		t.Fatalf("fork after its source was deleted = %+v, %v", got, err)
	}
}

func TestRenameCheckpoint(t *testing.T) {
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
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "dev", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "other", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	ready := func(name string) Checkpoint {
		t.Helper()
		cp, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetCheckpointState(ctx, env.ID, sb.ID, cp.ID, CheckpointStateReady); err != nil {
			t.Fatal(err)
		}
		return cp
	}
	first, second := ready("first"), ready("second")

	renamed, err := s.RenameCheckpoint(ctx, env.ID, sb.ID, first.ID, "before deploy")
	if err != nil || renamed.Name != "before deploy" || renamed.ID != first.ID || renamed.State != CheckpointStateReady {
		t.Fatalf("RenameCheckpoint() = %+v, %v", renamed, err)
	}
	if got, err := s.Checkpoint(ctx, env.ID, sb.ID, first.ID); err != nil || got.Name != "before deploy" {
		t.Fatalf("stored checkpoint = %+v, %v", got, err)
	}
	if _, err := s.RenameCheckpoint(ctx, env.ID, sb.ID, second.ID, "before deploy"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate name error = %v, want ErrExists", err)
	}
	if _, err := s.RenameCheckpoint(ctx, env.ID, other.ID, first.ID, "elsewhere"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename through another sandbox error = %v, want ErrNotFound", err)
	}
	creating, err := s.CreateCheckpoint(ctx, env.ID, sb.ID, "pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameCheckpoint(ctx, env.ID, sb.ID, creating.ID, "done"); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename of a creating checkpoint error = %v, want ErrConflict", err)
	}
}
