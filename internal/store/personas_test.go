package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func openPersonaStore(t *testing.T) (*Store, Environment, Environment) {
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

func TestProvidersAndPersonas(t *testing.T) {
	ctx := context.Background()
	s, work, other := openPersonaStore(t)

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

	persona := Persona{ID: "a1", EnvironmentID: work.ID, Name: "Ada", Role: "reviewer", Soul: "# Ada", Harness: "claude", ProviderID: "p1", Model: model, GitName: "Ada", GitEmail: "ada@agents.invalid"}
	if _, err := s.CreatePersona(ctx, persona); err != nil {
		t.Fatal(err)
	}
	dup := persona
	dup.ID = "a2"
	if _, err := s.CreatePersona(ctx, dup); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate persona name: %v", err)
	}
	cross := persona
	cross.ID, cross.EnvironmentID = "a3", other.ID
	if _, err := s.CreatePersona(ctx, cross); err == nil {
		t.Fatal("persona with another environment's provider accepted")
	}
	if _, err := s.Persona(ctx, other.ID, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment persona read: %v", err)
	}
	persona.Role = "author"
	if got, err := s.UpdatePersona(ctx, persona); err != nil || got.Role != "author" {
		t.Fatalf("update persona: %+v %v", got, err)
	}

	// A provider in use, and a provider's secret, can't be deleted.
	if err := s.DeleteProvider(ctx, work.ID, "p1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete provider in use: %v", err)
	}
	if err := s.DeleteSecret(ctx, work.ID, "s1"); err == nil {
		t.Fatal("deleting a provider's secret directly succeeded")
	}

	// Ownership: a sandbox owned by a persona of another environment is refused.
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "owned", PersonaID: "a1"})
	if err != nil || sb.PersonaID != "a1" {
		t.Fatalf("owned sandbox: %+v %v", sb, err)
	}
	if _, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: other.ID, Name: "foreign", PersonaID: "a1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sandbox owned across environments: %v", err)
	}
	unowned, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "free"})
	if err != nil || unowned.PersonaID != "" {
		t.Fatalf("unowned sandbox: %+v %v", unowned, err)
	}
	if owners, err := s.SandboxPersonas(ctx, work.ID); err != nil || owners[sb.ID] != "a1" || owners[unowned.ID] != "" {
		t.Fatalf("owners: %v %v", owners, err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE sandboxes SET persona_id = NULL WHERE id = ?", sb.ID); err == nil {
		t.Fatal("sandbox ownership changed")
	}

	// Persona rules: same environment, and not both scopes.
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: work.ID, PersonaID: "a1", Host: "example.com", Action: "allow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: other.ID, PersonaID: "a1", Host: "example.com", Action: "allow"}); err == nil {
		t.Fatal("rule scoped to another environment's persona accepted")
	}
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: work.ID, PersonaID: "a1", SandboxID: sb.ID, Host: "example.com", Action: "allow"}); err == nil {
		t.Fatal("rule with both scopes accepted")
	}

	if err := s.DeletePersona(ctx, work.ID, "a1"); !errors.Is(err, ErrConflict) || err.Error() == "" {
		t.Fatalf("delete persona owning sandboxes: %v", err)
	}
	if err := s.DeleteSandbox(ctx, work.ID, sb.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePersona(ctx, other.ID, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment persona delete: %v", err)
	}
	if err := s.DeletePersona(ctx, work.ID, "a1"); err != nil {
		t.Fatal(err)
	}
	if rules, _ := s.Rules(ctx, work.ID); len(rules) != 0 {
		t.Fatalf("persona rules survived the persona: %+v", rules)
	}
	if err := s.DeletePersona(ctx, work.ID, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete persona twice: %v", err)
	}

	if err := s.DeleteProvider(ctx, work.ID, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SecretByID(ctx, work.ID, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provider secret survived the provider: %v", err)
	}
}

func TestPersonasDeletedWithEnvironment(t *testing.T) {
	ctx := context.Background()
	s, work, _ := openPersonaStore(t)
	if _, err := s.CreateProvider(ctx, Provider{ID: "p1", EnvironmentID: work.ID, Name: "anthropic", Kind: "anthropic_api"}, providerSecret(work.ID, "s1", "K")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePersona(ctx, Persona{ID: "a1", EnvironmentID: work.ID, Name: "Ada", Harness: "claude", ProviderID: "p1", Model: "m", GitName: "Ada", GitEmail: "ada@agents.invalid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(ctx, Rule{EnvironmentID: work.ID, PersonaID: "a1", Host: "example.com", Action: "allow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM environments WHERE id = ?", work.ID); err != nil {
		t.Fatalf("environment with personas, providers and rules: %v", err)
	}
}
