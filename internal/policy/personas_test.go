package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestMatchPersonaScope(t *testing.T) {
	rules := []store.Rule{
		{ID: "env-allow", Host: "*.example.com", Action: store.ActionAllow},
		{ID: "ada-deny", PersonaID: "ada", Host: "*.example.com", Action: store.ActionDeny},
		{ID: "ada-api", PersonaID: "ada", Host: "api.example.com", Action: store.ActionAllow},
		{ID: "sb-allow", SandboxID: "sb-ada", Host: "*.example.com", Action: store.ActionAllow},
		{ID: "bob-allow", PersonaID: "bob", Host: "bob.example.net", Action: store.ActionAllow},
		{ID: "fence", Host: "evil.example.org", Action: store.ActionDeny},
		{ID: "ada-evil", PersonaID: "ada", Host: "evil.example.org", Action: store.ActionAllow},
	}
	cases := []struct {
		sandbox, persona, host, want string
	}{
		{"sb-other", "ada", "www.example.com", "ada-deny"}, // persona beats environment
		{"sb-other", "ada", "api.example.com", "ada-api"},  // most specific within the persona scope
		{"sb-ada", "ada", "www.example.com", "sb-allow"},   // sandbox beats persona
		{"sb-bob", "bob", "www.example.com", "env-allow"},  // another persona's rule never applies
		{"sb-free", "", "www.example.com", "env-allow"},    // nor to an unowned sandbox
		{"sb-free", "", "bob.example.net", ""},
		{"sb-ada", "ada", "bob.example.net", ""},
		{"sb-bob", "bob", "bob.example.net", "bob-allow"},
		{"sb-ada", "ada", "evil.example.org", "fence"}, // an environment deny still fences
	}
	for _, c := range cases {
		r, ok := Match(rules, c.sandbox, c.persona, c.host, 443)
		if got := map[bool]string{true: r.ID}[ok]; got != c.want {
			t.Errorf("%s (%s) %s: got %q, want %q", c.sandbox, c.persona, c.host, got, c.want)
		}
	}
}

// newPersonaEngine adds two personas, each owning one sandbox, to a new engine; the
// engine's own sandbox stays unowned.
func newPersonaEngine(t *testing.T) (e *Engine, env store.Environment, unowned, ada, bob store.Sandbox) {
	t.Helper()
	ctx := context.Background()
	e, env, unowned = newEngine(t)
	if _, err := e.Store.CreateProvider(ctx, store.Provider{ID: "p1", EnvironmentID: env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	owned := map[string]store.Sandbox{}
	for _, name := range []string{"ada", "bob"} {
		p, err := e.Store.CreatePersona(ctx, store.Persona{EnvironmentID: env.ID, Name: name, Harness: "claude", ProviderID: "p1", GitName: name, GitEmail: name + "@agents.invalid"})
		if err != nil {
			t.Fatal(err)
		}
		if owned[name], err = e.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: name + "-dev", CPUs: 1, PersonaID: p.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return e, env, unowned, owned["ada"], owned["bob"]
}

func TestPersonaRuleOnlyReachesItsSandboxes(t *testing.T) {
	ctx := context.Background()
	e, env, unowned, ada, bob := newPersonaEngine(t)
	e.Hold = 20 * time.Millisecond
	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, PersonaID: ada.PersonaID, Host: "example.com", Action: store.ActionAllow}); err != nil {
		t.Fatal(err)
	}
	r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: ada.ID, Host: "example.com", Port: 443})
	if err != nil || r.PersonaID != ada.PersonaID {
		t.Fatalf("the persona's own sandbox: %+v %v", r, err)
	}
	for _, sb := range []store.Sandbox{bob, unowned} {
		if r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "example.com", Port: 443}); !errors.Is(err, ErrUndecided) {
			t.Fatalf("persona rule reached sandbox %s: %+v %v", sb.Name, r, err)
		}
	}
}

func TestResolvePersonaScope(t *testing.T) {
	ctx := context.Background()
	e, env, unowned, ada, bob := newPersonaEngine(t)
	e.Hold = 20 * time.Millisecond
	approval := func(sb store.Sandbox) store.Approval {
		t.Helper()
		if _, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "example.com", Port: 443}); !errors.Is(err, ErrUndecided) {
			t.Fatalf("got %v", err)
		}
		pending, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
		for _, a := range pending {
			if a.SandboxID == sb.ID {
				return a
			}
		}
		t.Fatalf("no approval for %s", sb.Name)
		return store.Approval{}
	}

	free := approval(unowned)
	if _, err := e.Resolve(ctx, env.ID, free.ID, Decision{Action: "allow", Scope: "persona"}); !errors.Is(err, ErrNoPersona) {
		t.Fatalf("persona scope for an unowned sandbox: %v", err)
	}

	a := approval(ada)
	approvalBob := approval(bob)
	resolved, err := e.Resolve(ctx, env.ID, a.ID, Decision{Action: "allow", Scope: "persona"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := e.Store.Rule(ctx, env.ID, resolved.RuleID)
	if err != nil || rule.PersonaID != ada.PersonaID || rule.SandboxID != "" {
		t.Fatalf("persona-scoped rule: %+v %v", rule, err)
	}
	// Settling must leave the other persona's and the unowned sandbox's requests pending.
	for _, id := range []string{approvalBob.ID, free.ID} {
		if cur, _ := e.Store.Approval(ctx, env.ID, id); cur.Status != store.StatusPending {
			t.Fatalf("approval %s settled by another persona's rule: %+v", id, cur)
		}
	}
}
