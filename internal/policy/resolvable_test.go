package policy

import (
	"context"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestResolvable(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	other, err := e.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "other", CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []store.Rule{
		{Host: "*.allowed.example", Action: store.ActionAllow},
		{Host: "port.example", Action: store.ActionAllow, Ports: []int{22}},
		{Host: "denied.example", Action: store.ActionDeny},
		{Host: "theirs.example", Action: store.ActionAllow, SandboxID: other.ID},
		{Host: "browser.example", Action: store.ActionAllow, BrowserOnly: true},
	} {
		r.EnvironmentID = env.ID
		if _, err := e.Store.CreateRule(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Store.SetPrivateHosts(ctx, env.ID, []store.PrivateHost{{Host: "git.company.com"}}); err != nil {
		t.Fatal(err)
	}
	ask := func(req Request) bool {
		t.Helper()
		req.EnvironmentID, req.SandboxID = env.ID, sb.ID
		ok, err := e.Resolvable(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	level := func(l string) {
		t.Helper()
		if err := e.Store.SetSecurityLevel(ctx, env.ID, l); err != nil {
			t.Fatal(err)
		}
	}

	// Rules count at every level, on any port, for the sandboxes they cover.
	level(store.LevelLocked)
	for host, want := range map[string]bool{
		"a.allowed.example":  true,
		"port.example":       true,
		"denied.example":     false,
		"theirs.example":     false,
		"browser.example":    false,
		"unknown.example":    false,
		"registry.npmjs.org": false,
	} {
		if got := ask(Request{Host: host}); got != want {
			t.Errorf("locked, %s: %v, want %v", host, got, want)
		}
	}
	if !ask(Request{Host: "browser.example", Browser: true}) {
		t.Error("a browser-only rule doesn't count for a browser VM")
	}

	// A level counts as far as it allows without asking.
	for _, c := range []struct {
		level, host string
		want        bool
	}{
		{store.LevelGuarded, "registry.npmjs.org", true},
		{store.LevelGuarded, "unknown.example", false},
		{store.LevelAssisted, "registry.npmjs.org", true},
		{store.LevelAssisted, "unknown.example", false},
		{store.LevelOpen, "unknown.example", true},
		{store.LevelOpen, "git.company.com", false},
	} {
		level(c.level)
		if got := ask(Request{Host: c.host}); got != c.want {
			t.Errorf("%s, %s: %v, want %v", c.level, c.host, got, c.want)
		}
	}

	// An entry the environment turned off doesn't count.
	level(store.LevelGuarded)
	if err := e.Store.SetCuratedOff(ctx, env.ID, []string{"npm"}); err != nil {
		t.Fatal(err)
	}
	if ask(Request{Host: "registry.npmjs.org"}) {
		t.Error("a curated entry that is off counts")
	}

	// A system VM reaches only its own hosts, at any level. A template build adds its own.
	level(store.LevelOpen)
	if ask(Request{Host: "unknown.example", Allowed: []string{"api.anthropic.com"}}) || !ask(Request{Host: "api.anthropic.com", Allowed: []string{"api.anthropic.com"}}) {
		t.Error("a system VM's hosts decide its lookups")
	}
	level(store.LevelLocked)
	if !ask(Request{Host: "nodejs.org", Preapproved: []string{"nodejs.org"}}) {
		t.Error("a template build's hosts don't count")
	}

	// Nothing here asks anyone or leaves a trace.
	pending, err := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending approvals: %+v", pending)
	}
}
