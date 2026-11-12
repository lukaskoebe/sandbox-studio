package store

import (
	"context"
	"fmt"
	"testing"
)

// Deleting an environment removes every row recorded for it, whatever points at what, and
// leaves the other environments as they were.
func TestDeleteEnvironmentRemovesEverythingOfIt(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fill := func(name string) Environment {
		t.Helper()
		env, err := s.CreateEnvironment(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		p, err := s.CreateProvider(ctx, Provider{ID: NewID(), EnvironmentID: env.ID, Name: "anthropic", Kind: "anthropic_api"}, providerSecret(env.ID, NewID(), "PROVIDER_ANTHROPIC_KEY"))
		must(err)
		tm, err := s.CreateTeammate(ctx, Teammate{EnvironmentID: env.ID, Name: "ada", Harness: "claude", ProviderID: p.ID})
		must(err)
		from := createReadyTemplate(t, ctx, s, env.ID, "1")
		to := createReadyTemplate(t, ctx, s, env.ID, "2")
		job := newBuildJobForTest(t, ctx, s, env.ID, "req-"+name)
		sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "box", TemplateID: from.ID, TeammateID: tm.ID, BuildJobID: job.ID, CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
		must(err)
		_, err = s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "plain", ForkedFromID: sb.ID, CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
		must(err)
		_, err = s.BeginRebase(ctx, RebaseOperation{SandboxID: sb.ID, EnvironmentID: env.ID, TemplateID: to.ID, CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
		must(err)
		_, err = s.CreateCheckpoint(ctx, env.ID, sb.ID, "cp")
		must(err)
		_, err = s.CreateChat(ctx, Chat{EnvironmentID: env.ID, SandboxID: sb.ID, TeammateID: tm.ID, Harness: "claude"})
		must(err)
		_, err = s.CreateRule(ctx, Rule{EnvironmentID: env.ID, TeammateID: tm.ID, Host: "example.com", Action: "allow"})
		must(err)
		_, err = s.CreateSecret(ctx, Secret{ID: NewID(), EnvironmentID: env.ID, SandboxID: sb.ID, Name: "PLAIN", Sealed: []byte("x"), Placeholder: "plain-" + name})
		must(err)
		_, err = s.CreateForge(ctx, Forge{ID: NewID(), EnvironmentID: env.ID, Name: "gh", Kind: "github", BaseURL: "https://github.com"},
			Secret{ID: NewID(), Name: "FORGE_GH_TOKEN", Sealed: []byte("sealed"), Hosts: []string{"github.com"}, Placeholder: "gh-" + name})
		must(err)
		return env
	}
	gone, kept := fill("gone"), fill("kept")

	// Every table with an environment_id, and how many rows each environment has in it.
	counts := func(envID string) map[string]int {
		t.Helper()
		rows, err := s.db.QueryContext(ctx, "SELECT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) c WHERE m.type = 'table' AND c.name = 'environment_id'")
		if err != nil {
			t.Fatal(err)
		}
		var tables []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, n)
		}
		rows.Close()
		out := map[string]int{}
		for _, tb := range tables {
			var n int
			if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %q WHERE environment_id = ?", tb), envID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				out[tb] = n
			}
		}
		return out
	}
	before := counts(kept.ID)
	if len(counts(gone.ID)) < 8 {
		t.Fatalf("the fixture filled too little: %v", counts(gone.ID))
	}
	if err := s.DeleteEnvironment(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if left := counts(gone.ID); len(left) != 0 {
		t.Fatalf("rows left of the deleted environment: %v", left)
	}
	if _, err := s.Environment(ctx, gone.ID); err != ErrNotFound {
		t.Fatalf("the environment is still there: %v", err)
	}
	if after := counts(kept.ID); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("the other environment changed: %v, was %v", after, before)
	}
	if err := s.DeleteEnvironment(ctx, gone.ID); err != ErrNotFound {
		t.Fatalf("deleting it again: %v", err)
	}
}
