package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A system VM reaches its kind's hosts without anyone being asked, and nothing else.
func TestSystemSandboxReachesOnlyItsHosts(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Port: 443,
		Allowed: SystemHosts(store.SandboxKindClaudeUtility)}

	req.Host = "API.anthropic.com."
	if rule, err := e.Decide(ctx, req); err != nil || rule.Action != store.ActionAllow {
		t.Fatalf("api.anthropic.com: rule %+v, err %v", rule, err)
	}
	req.Host = "example.com"
	if _, err := e.Decide(ctx, req); !errors.Is(err, ErrUndecided) {
		t.Fatalf("example.com: err %v, want ErrUndecided", err)
	}
	if pending, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10); len(pending) != 0 {
		t.Fatalf("a system VM asked about a host: %d approvals pending", len(pending))
	}
}

// A rule still decides first: a fence the user set holds for a system VM too.
func TestSystemSandboxKeepsRules(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "api.anthropic.com", Action: store.ActionDeny, Origin: "manual"}); err != nil {
		t.Fatal(err)
	}
	e.changed(env.ID, true)
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "api.anthropic.com", Port: 443,
		Allowed: SystemHosts(store.SandboxKindClaudeUtility)}
	if rule, err := e.Decide(ctx, req); err != nil || rule.Action != store.ActionDeny {
		t.Fatalf("rule %+v, err %v; want the deny rule", rule, err)
	}
}

func TestSystemHostsOnlyForSystemKinds(t *testing.T) {
	if SystemHosts("") != nil || SystemHosts(store.SandboxKindBrowser) != nil {
		t.Fatal("an ordinary or browser sandbox has no system hosts")
	}
	for _, kind := range []string{store.SandboxKindClaudeLogin, store.SandboxKindCodexLogin, store.SandboxKindClaudeUtility, store.SandboxKindCodexUtility} {
		if len(SystemHosts(kind)) == 0 || !store.IsSystemKind(kind) {
			t.Fatalf("%s: no hosts or not a system kind", kind)
		}
	}
}
