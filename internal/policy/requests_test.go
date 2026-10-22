package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestRequestRulesDecideTheRequestsTheyNameOnly(t *testing.T) {
	rules := []store.Rule{
		{ID: "all", Host: "api.example.com", Action: store.ActionAllow},
		{ID: "no-delete", Host: "api.example.com", Method: "DELETE", PathPrefix: "/repos/*", Action: store.ActionDeny},
		{ID: "public", Host: "api.example.com", Method: "GET", PathPrefix: "/public/*", Action: store.ActionAllow},
		{ID: "any-method", Host: "*.example.com", PathPrefix: "/admin/*", Action: store.ActionDeny},
	}
	cases := []struct {
		name, method, path, want string
	}{
		{"a connection ignores request rules", "", "", "all"},
		{"a deny names its request", "DELETE", "/repos/a/b", "no-delete"},
		{"the request rule beats the host rule", "GET", "/public/x", "public"},
		{"other requests keep the host rule", "POST", "/repos/a", "all"},
		{"a method-less rule covers any method", "PUT", "/admin/x", "any-method"},
		{"deny matches the cleaned path", "DELETE", "/repos/../repos/a", "no-delete"},
		{"a walk out of an allow is not covered", "GET", "/public/../private", "all"},
		{"a prefix needs its slash", "GET", "/publicity", "all"},
	}
	for _, c := range cases {
		var req *HTTPRequest
		if c.method != "" {
			req = &HTTPRequest{Method: c.method, Path: c.path}
		}
		r, ok := matchRule(rules, "", "", "api.example.com", 443, req)
		if !ok || r.ID != c.want {
			t.Errorf("%s: %s %s got %q (%v), want %q", c.name, c.method, c.path, r.ID, ok, c.want)
		}
	}
}

func TestRequestPatternsAreChecked(t *testing.T) {
	for _, ok := range []string{"", "/*", "/repos/*", "/a-b_c/d.e/*"} {
		if _, err := ValidPathPrefix(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"/repos", "repos/*", "*", "/a*/b*", "/a?b*", "/a#b*", "/a b*", "/a/../b*", "/a//b*", "/a\\b*"} {
		if _, err := ValidPathPrefix(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"", "GET", "DELETE", "OPTIONS"} {
		if _, err := ValidMethod(ok); err != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"get", "CONNECT", "BREW"} {
		if _, err := ValidMethod(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for path, want := range map[string]string{"/repos/a/b": "/repos/*", "/repos/": "/repos/*", "/index.html": "", "/": "", "/repos/../x/y": ""} {
		if got := PathSuggestion(path); got != want {
			t.Errorf("PathSuggestion(%q) = %q, want %q", path, got, want)
		}
	}
}

// An approval for a request can allow its method and path: the held request of that path passes,
// and a path the rule doesn't cover is refused.
func TestRequestApprovalAllowsAMethodAndPath(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	held := make(chan heldResult, 1)
	go func() {
		r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "api.example.com", Port: 443, Deferred: true,
			HTTP: &HTTPRequest{Method: "GET", Path: "/repos/a/b"}})
		held <- heldResult{r, err}
	}()
	pending := waitPending(t, e, env.ID, 1)
	a, ok := pending["api.example.com:443"]
	if !ok {
		t.Fatalf("pending: %v", pending)
	}
	var req NetworkRequest
	if err := json.Unmarshal(a.Payload, &req); err != nil || req.PathPattern != "/repos/*" {
		t.Fatalf("payload %+v %v", req, err)
	}
	if _, err := e.Resolve(ctx, env.ID, a.ID, Decision{Action: "allow", Scope: "environment", Method: "GET", PathPrefix: "/other/*"}); !errors.Is(err, ErrDoesNotCover) {
		t.Fatalf("a path the request is not on was allowed: %v", err)
	}
	if _, err := e.Resolve(ctx, env.ID, a.ID, Decision{Action: "allow", Scope: "environment", Method: "GET", PathPrefix: "/repos/*"}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-held:
		if r.err != nil || r.rule.Method != "GET" || r.rule.PathPrefix != "/repos/*" || r.rule.Action != store.ActionAllow {
			t.Fatalf("held request: %+v %v", r.rule, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held request was not answered")
	}
	rules, _ := e.Store.Rules(ctx, env.ID)
	if len(rules) != 1 || rules[0].Method != "GET" || rules[0].PathPrefix != "/repos/*" {
		t.Fatalf("rules: %+v", rules)
	}
	// Allowed without a note, the rule says what was asked, by which sandbox and when.
	if want := RuleNote("allow", req, e.now()); rules[0].Note != want || !strings.Contains(want, "(GET api.example.com/repos/a/b)") {
		t.Fatalf("note %q, want %q", rules[0].Note, want)
	}

	// A connection's approval can't carry a method or path.
	go func() {
		_, _ = e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "other.example", Port: 443})
	}()
	for _, p := range waitPending(t, e, env.ID, 1) {
		if p.Kind == KindNetwork {
			if _, err := e.Resolve(ctx, env.ID, p.ID, Decision{Action: "allow", Scope: "environment", Method: "GET", PathPrefix: "/*"}); !errors.Is(err, ErrNotRequest) {
				t.Errorf("a connection's approval took a path: %v", err)
			}
		}
	}
}

// The note given with an allow is kept on the rule; one that is too long is refused.
func TestResolveKeepsTheNote(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	go func() {
		_, _ = e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Host: "notes.example.org", Port: 443})
	}()
	a := waitPending(t, e, env.ID, 1)["notes.example.org:443"]
	long := strings.Repeat("x", MaxNote+1)
	if _, err := e.Resolve(ctx, env.ID, a.ID, Decision{Action: "allow", Scope: "environment", Note: long}); !errors.Is(err, ErrNoteTooLong) {
		t.Fatalf("a long note: %v", err)
	}
	if _, err := e.Resolve(ctx, env.ID, a.ID, Decision{Action: "allow", Scope: "environment", Note: "  pip installs for the docs build "}); err != nil {
		t.Fatal(err)
	}
	rules, _ := e.Store.Rules(ctx, env.ID)
	if len(rules) != 1 || rules[0].Note != "pip installs for the docs build" {
		t.Fatalf("rules: %+v", rules)
	}
}

func TestRuleNote(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	if got := RuleNote("deny", NetworkRequest{Host: "x.example.org", Port: 443, SandboxName: "blub"}, at); got != "Denied for blub on 22 Oct 2026 (x.example.org:443)" {
		t.Errorf("got %q", got)
	}
	long := NetworkRequest{Host: "a.example.org", Port: 443, HTTPRequest: HTTPRequest{Method: "GET", Path: "/" + strings.Repeat("p", 600)}}
	if got := []rune(RuleNote("allow", long, at)); len(got) != MaxNote {
		t.Errorf("a long request's note has %d characters", len(got))
	}
}
