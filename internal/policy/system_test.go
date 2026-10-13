package policy

import (
	"context"
	"errors"
	"testing"
	"time"

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

// A template build's VM reaches what its own steps download on 80 and 443 without asking, even at
// the level locked; another port or host is asked as for any sandbox.
func TestBuildSandboxReachesBuildHosts(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 50 * time.Millisecond
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelLocked); err != nil {
		t.Fatal(err)
	}
	sb.BuildJobID = "job"
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Preapproved: BuildHosts(sb)}
	for _, hp := range []struct {
		host string
		port int
	}{{"nodejs.org", 443}, {"Deb.Debian.org.", 80}, {"mise-versions.jdx.dev", 443}} {
		req.Host, req.Port = hp.host, hp.port
		if rule, err := e.Decide(ctx, req); err != nil || rule.Action != store.ActionAllow {
			t.Fatalf("%s:%d: rule %+v, err %v", hp.host, hp.port, rule, err)
		}
	}
	if pending, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10); len(pending) != 0 {
		t.Fatalf("a build host was asked about: %d approvals pending", len(pending))
	}
	for _, hp := range []struct {
		host string
		port int
	}{{"github.com", 22}, {"example.com", 443}} {
		req.Host, req.Port = hp.host, hp.port
		if _, err := e.Decide(ctx, req); !errors.Is(err, ErrUndecided) {
			t.Fatalf("%s:%d: err %v, want ErrUndecided", hp.host, hp.port, err)
		}
	}
	if BuildHosts(store.Sandbox{}) != nil {
		t.Fatal("an ordinary sandbox has build hosts")
	}
}

// A deny rule the user set still holds for a build's VM.
func TestBuildSandboxKeepsDenyRules(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "github.com", Action: store.ActionDeny, Origin: "manual"}); err != nil {
		t.Fatal(err)
	}
	e.changed(env.ID, true)
	sb.BuildJobID = "job"
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "github.com", Port: 443, Preapproved: BuildHosts(sb)}
	if rule, err := e.Decide(ctx, req); err != nil || rule.Action != store.ActionDeny {
		t.Fatalf("rule %+v, err %v; want the deny rule", rule, err)
	}
}
