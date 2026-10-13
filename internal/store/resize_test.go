package store

import (
	"context"
	"errors"
	"testing"
)

func TestSetSandboxResourcesIsEnvironmentScoped(t *testing.T) {
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
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "api", CPUs: 2, MemoryMiB: 2048, MaxMemoryMiB: 4096, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetSandboxResources(ctx, env.ID, sb.ID, 4, 3072, 4096); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sandbox(ctx, env.ID, sb.ID)
	if err != nil || got.CPUs != 4 || got.MemoryMiB != 3072 || got.MaxMemoryMiB != 4096 {
		t.Fatalf("sandbox after resize = %+v, %v", got, err)
	}
	if err := s.SetSandboxResources(ctx, other.ID, sb.ID, 1, 512, 512); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resize from another environment = %v, want ErrNotFound", err)
	}
	if got, _ := s.Sandbox(ctx, env.ID, sb.ID); got.CPUs != 4 {
		t.Fatalf("another environment changed the sandbox: %+v", got)
	}
}
