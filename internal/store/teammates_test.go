package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func openTeammateStore(t *testing.T) (*Store, Environment, Environment) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	work, _ := s.CreateEnvironment(ctx, "work")
	other, _ := s.CreateEnvironment(ctx, "other")
	return s, work, other
}

func providerSecret(env, id, name string) *Secret {
	return &Secret{ID: id, EnvironmentID: env, Name: name, Sealed: []byte("sealed"), Hosts: []string{"api.anthropic.com"}, Placeholder: "studio-" + id}
}

func TestProvidersAndTeammates(t *testing.T) {
	ctx := context.Background()
	s, work, other := openTeammateStore(t)

	p, err := s.CreateProvider(ctx, Provider{ID: "p1", EnvironmentID: work.ID, Name: "anthropic", Kind: "anthropic_api"}, providerSecret(work.ID, "s1", "PROVIDER_ANTHROPIC_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	if p.SecretID != "s1" || p.SecretName != "PROVIDER_ANTHROPIC_KEY" || p.Placeholder != "studio-s1" || !slices.Equal(p.Hosts, []string{"api.anthropic.com"}) {
		t.Fatalf("provider view: %+v", p)
	}
	if _, err := s.CreateProvider(ctx, Provider{ID: "p2", EnvironmentID: work.ID, Name: "anthropic", Kind: "anthropic_api"}, providerSecret(work.ID, "s2", "OTHER_KEY")); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate provider name: %v", err)
	}
	if _, err := s.SecretByID(ctx, work.ID, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a failed provider insert left its secret: %v", err)
	}
	if _, err := s.CreateProvider(ctx, Provider{ID: "p3", EnvironmentID: work.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatalf("subscription provider: %v", err)
	}
	// The table's CHECKs: API kinds need a secret, subscriptions have none, compatible needs a URL and model.
	if _, err := s.CreateProvider(ctx, Provider{ID: "p4", EnvironmentID: work.ID, Name: "nokey", Kind: "openai_api"}, nil); err == nil {
		t.Fatal("API provider without a secret accepted")
	}
	if _, err := s.CreateProvider(ctx, Provider{ID: "p5", EnvironmentID: work.ID, Name: "compat", Kind: "openai_compatible"}, providerSecret(work.ID, "s5", "COMPAT_KEY")); err == nil {
		t.Fatal("compatible provider without a base URL accepted")
	}
	if _, err := s.CreateSecret(ctx, Secret{ID: "s6", EnvironmentID: other.ID, Name: "X_KEY", Sealed: []byte("x"), Hosts: []string{"a.com"}, Placeholder: "studio-s6"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO providers (id, environment_id, name, kind, secret_id, created_at, updated_at) VALUES ('p6', ?, 'x', 'anthropic_api', 's6', 0, 0)", work.ID); err == nil {
		t.Fatal("provider with another environment's secret accepted")
	}

	if name, err := s.SecretProvider(ctx, work.ID, "s1"); err != nil || name != "anthropic" {
		t.Fatalf("secret owner: %q %v", name, err)
	}

	model := "claude-sonnet"
	if _, err := s.UpdateProvider(ctx, work.ID, "p1", ProviderUpdate{Sealed: []byte("sealed-2")}); err != nil {
		t.Fatal(err)
	}
	if sec, _ := s.SecretByID(ctx, work.ID, "s1"); string(sec.Sealed) != "sealed-2" || sec.Placeholder != "studio-s1" {
		t.Fatalf("key rotation: %+v", sec)
	}
	if _, err := s.UpdateProvider(ctx, other.ID, "p1", ProviderUpdate{Model: &model}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment provider update: %v", err)
	}

	teammate := Teammate{ID: "a1", EnvironmentID: work.ID, Name: "Ada", Role: "reviewer", Soul: "# Ada", Harness: "claude", ProviderID: "p1", Model: model, GitName: "Ada", GitEmail: "ada@agents.invalid"}
	if _, err := s.CreateTeammate(ctx, teammate); err != nil {
		t.Fatal(err)
	}
	dup := teammate
	dup.ID = "a2"
	if _, err := s.CreateTeammate(ctx, dup); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate teammate name: %v", err)
	}
	cross := teammate
	cross.ID, cross.EnvironmentID = "a3", other.ID
	if _, err := s.CreateTeammate(ctx, cross); err == nil {
		t.Fatal("teammate with another environment's provider accepted")
	}
	if _, err := s.Teammate(ctx, other.ID, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment teammate read: %v", err)
	}
	teammate.Role = "author"
	if got, err := s.UpdateTeammate(ctx, teammate); err != nil || got.Role != "author" {
		t.Fatalf("update teammate: %+v %v", got, err)
	}

	// A provider in use, and a provider's secret, can't be deleted.
	if err := s.DeleteProvider(ctx, work.ID, "p1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete provider in use: %v", err)
	}
	if err := s.DeleteSecret(ctx, work.ID, "s1"); err == nil {
		t.Fatal("deleting a provider's secret directly succeeded")
	}

	// Ownership: a sandbox owned by a teammate of another environment is refused.
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "owned", TeammateID: "a1"})
	if err != nil || sb.TeammateID != "a1" {
		t.Fatalf("owned sandbox: %+v %v", sb, err)
	}
	if _, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: other.ID, Name: "foreign", TeammateID: "a1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sandbox owned across environments: %v", err)
	}
	unowned, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "free"})
	if err != nil || unowned.TeammateID != "" {
		t.Fatalf("unowned sandbox: %+v %v", unowned, err)
	}
	if owners, err := s.SandboxTeammates(ctx, work.ID); err != nil || owners[sb.ID] != "a1" || owners[unowned.ID] != "" {
		t.Fatalf("owners: %v %v", owners, err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE sandboxes SET teammate_id = NULL WHERE id = ?", sb.ID); err == nil {
		t.Fatal("sandbox ownership changed")
	}

	// Teammate rules: same environment, and not both scopes.
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: work.ID, TeammateID: "a1", Host: "example.com", Action: "allow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: other.ID, TeammateID: "a1", Host: "example.com", Action: "allow"}); err == nil {
		t.Fatal("rule scoped to another environment's teammate accepted")
	}
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: work.ID, TeammateID: "a1", SandboxID: sb.ID, Host: "example.com", Action: "allow"}); err == nil {
		t.Fatal("rule with both scopes accepted")
	}

	if err := s.DeleteTeammate(ctx, work.ID, "a1"); !errors.Is(err, ErrConflict) || err.Error() == "" {
		t.Fatalf("delete teammate owning sandboxes: %v", err)
	}
	if err := s.DeleteSandbox(ctx, work.ID, sb.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTeammate(ctx, other.ID, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment teammate delete: %v", err)
	}
	if err := s.DeleteTeammate(ctx, work.ID, "a1"); err != nil {
		t.Fatal(err)
	}
	if rules, _ := s.Rules(ctx, work.ID); len(rules) != 0 {
		t.Fatalf("teammate rules survived the teammate: %+v", rules)
	}
	if err := s.DeleteTeammate(ctx, work.ID, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete teammate twice: %v", err)
	}

	if err := s.DeleteProvider(ctx, work.ID, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SecretByID(ctx, work.ID, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provider secret survived the provider: %v", err)
	}
}

func TestTeammatesDeletedWithEnvironment(t *testing.T) {
	ctx := context.Background()
	s, work, _ := openTeammateStore(t)
	if _, err := s.CreateProvider(ctx, Provider{ID: "p1", EnvironmentID: work.ID, Name: "anthropic", Kind: "anthropic_api"}, providerSecret(work.ID, "s1", "K")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTeammate(ctx, Teammate{ID: "a1", EnvironmentID: work.ID, Name: "Ada", Harness: "claude", ProviderID: "p1", Model: "m", GitName: "Ada", GitEmail: "ada@agents.invalid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: work.ID, TeammateID: "a1", Host: "example.com", Action: "allow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM environments WHERE id = ?", work.ID); err != nil {
		t.Fatalf("environment with teammates, providers and rules: %v", err)
	}
}
