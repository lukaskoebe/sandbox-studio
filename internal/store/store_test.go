package store

import (
	"context"
	"errors"
	"testing"
)

func TestSandboxesAreEnvironmentScoped(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	work, _ := s.CreateEnvironment(ctx, "work")
	private, _ := s.CreateEnvironment(ctx, "private")

	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "dev", CPUs: 2, MemoryMiB: 2048, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "dev", CPUs: 1}); err == nil {
		t.Fatal("duplicate name in one environment accepted")
	}
	if _, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: private.ID, Name: "dev", CPUs: 1}); err != nil {
		t.Fatalf("same name in another environment rejected: %v", err)
	}
	if _, err := s.Sandbox(ctx, private.ID, sb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment read: %v", err)
	}
	if err := s.DeleteSandbox(ctx, private.ID, sb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment delete: %v", err)
	}
	got, err := s.Sandbox(ctx, work.ID, sb.ID)
	if err != nil || got.Name != "dev" || got.Generation != 1 || got.MemoryMiB != 2048 {
		t.Fatalf("got %+v %v", got, err)
	}
	list, _ := s.Sandboxes(ctx, work.ID)
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
}
