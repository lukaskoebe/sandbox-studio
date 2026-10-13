package policy

import (
	"context"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestRefusedLookups(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	other, err := e.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "other", CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	add := func(r store.Rule) store.Rule {
		t.Helper()
		r.EnvironmentID = env.ID
		created, err := e.Store.CreateRule(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	wild := add(store.Rule{Host: "*.evil.example", Action: store.ActionDeny})
	add(store.Rule{Host: "port.example", Action: store.ActionDeny, Ports: []int{80}})
	add(store.Rule{Host: "split.example", Action: store.ActionDeny})
	add(store.Rule{Host: "split.example", Action: store.ActionAllow, Ports: []int{443}})
	add(store.Rule{Host: "theirs.example", Action: store.ActionDeny, SandboxID: other.ID})
	add(store.Rule{Host: "dev-only.example", Action: store.ActionDeny, SandboxID: sb.ID})
	add(store.Rule{Host: "fence.example", Action: store.ActionDeny})
	add(store.Rule{Host: "api.example", Action: store.ActionDeny, SandboxID: sb.ID})
	add(store.Rule{Host: "api.example", Action: store.ActionAllow, SandboxID: sb.ID, Method: "GET", PathPrefix: "/repos/*"})
	add(store.Rule{Host: "allowed.example", Action: store.ActionAllow})

	cases := map[string]bool{
		"sub.evil.example":    true,  // a wildcard deny on any port
		"evil.example":        true,  // the wildcard covers its own domain
		"EVIL.example.":       true,  // normalized
		"fence.example":       true,  // an environment-wide deny
		"dev-only.example":    true,  // this sandbox's own deny
		"port.example":        false, // a deny on one port only: the lookup must go on
		"split.example":       false, // a rule with ports allows some of its connections
		"api.example":         false, // a request rule allows some of its requests
		"theirs.example":      false, // another sandbox's rule
		"allowed.example":     false, // an allow
		"unknown.example":     false, // nothing matches: the connection asks for approval
		"sub.allowed.example": false,
	}
	for host, want := range cases {
		rule, refused, err := e.Refused(ctx, env.ID, sb.ID, host)
		if err != nil || refused != want {
			t.Errorf("%s: refused = %v (%v), want %v", host, refused, err, want)
		}
		if refused && rule.Action != store.ActionDeny {
			t.Errorf("%s: refused by %+v, not a deny", host, rule)
		}
	}
	if rule, refused, _ := e.Refused(ctx, env.ID, sb.ID, "sub.evil.example"); !refused || rule.ID != wild.ID {
		t.Fatalf("refused by %+v, want the wildcard %s", rule, wild.ID)
	}
	// Every refusal counts as a hit: the three names of the wildcard above, and the one just now.
	if hits := e.hits[wild.ID].count; hits != 4 {
		t.Errorf("the wildcard rule counted %d hits, want 4", hits)
	}
	if _, refused, _ := e.Refused(ctx, env.ID, other.ID, "dev-only.example"); refused {
		t.Error("another sandbox was refused by this sandbox's rule")
	}
}
