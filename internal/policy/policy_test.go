package policy

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestMatch(t *testing.T) {
	rules := []store.Rule{
		{ID: "any-npm", Host: "*.npmjs.org", Action: store.ActionAllow, Ports: []int{80, 443}},
		{ID: "registry-deny-sb", SandboxID: "sb1", Host: "registry.npmjs.org", Action: store.ActionDeny, Ports: []int{80, 443}},
		{ID: "github-ssh", Host: "github.com", Action: store.ActionAllow, Ports: []int{22}},
		{ID: "evil-fence", Host: "*.evil.com", Action: store.ActionDeny},
		{ID: "evil-api-sb", SandboxID: "sb1", Host: "api.evil.com", Action: store.ActionAllow},
		{ID: "ip", Host: "1.1.1.1", Action: store.ActionAllow, Ports: []int{443}},
		{ID: "other-sb", SandboxID: "sb2", Host: "*", Action: store.ActionAllow},
	}
	cases := []struct {
		sandbox, host string
		port          int
		want          string
	}{
		{"sb0", "registry.npmjs.org", 443, "any-npm"},
		{"sb0", "npmjs.org", 443, "any-npm"},                   // a wildcard covers its own domain
		{"sb0", "Registry.NPMJS.org.", 443, "any-npm"},         // names are normalized
		{"sb1", "registry.npmjs.org", 443, "registry-deny-sb"}, // sandbox scope first
		{"sb0", "registry.npmjs.org", 8080, ""},                // ports are part of the rule
		{"sb0", "github.com", 22, "github-ssh"},
		{"sb0", "github.com", 443, ""},
		{"sb1", "api.evil.com", 443, "evil-fence"}, // an environment deny always wins
		{"sb0", "1.1.1.1", 443, "ip"},
		{"sb0", "notnpmjs.org", 443, ""},
		{"sb2", "anything.example", 9999, "other-sb"},
		{"sb0", "anything.example", 9999, ""},
	}
	for _, c := range cases {
		r, ok := Match(rules, c.sandbox, c.host, c.port)
		if got := map[bool]string{true: r.ID}[ok]; got != c.want {
			t.Errorf("%s %s:%d: got %q, want %q", c.sandbox, c.host, c.port, got, c.want)
		}
	}
}

func TestSuggestionsAndPatterns(t *testing.T) {
	if got := Suggestions("registry.npmjs.org"); !slices.Equal(got, []string{"registry.npmjs.org", "*.npmjs.org"}) {
		t.Errorf("got %v", got)
	}
	if got := Suggestions("lukas.github.io"); !slices.Equal(got, []string{"lukas.github.io", "*.lukas.github.io"}) {
		t.Errorf("public suffix offered as a wildcard: %v", got)
	}
	for _, ok := range []string{"*", "*.example.com", "Example.COM.", "10.0.0.1", "::1", "_dmarc.example.com"} {
		if _, err := ValidPattern(ok); err != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "*.", "a..b", "-a.com", "exa mple.com", "*.*.com", "foo/bar"} {
		if _, err := ValidPattern(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func newEngine(t *testing.T) (*Engine, store.Environment, store.Sandbox) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, _ := st.CreateEnvironment(ctx, "work")
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "dev", CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{Store: st, Bus: &events.Bus{}, Hold: 5 * time.Second}, env, sb
}

func TestHoldAndApprove(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	sub, cancel := e.Bus.Subscribe()
	defer cancel()

	type result struct {
		rule store.Rule
		err  error
	}
	held := make(chan result, 3)
	for _, port := range []int{443, 443, 80} {
		go func() {
			r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Host: "registry.npmjs.org", Port: port})
			held <- result{r, err}
		}()
	}
	// Wait until both subjects (443 twice, 80 once) are pending.
	var pending []store.Approval
	for len(pending) < 2 || pending[0].Attempts+pending[1].Attempts < 3 {
		<-sub
		pending, _ = e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
	}
	var https store.Approval
	for _, a := range pending {
		if a.Subject == "registry.npmjs.org:443" {
			https = a
		}
	}
	if https.Attempts != 2 {
		t.Fatalf("repeated attempts not merged: %+v", pending)
	}

	if _, err := e.Resolve(ctx, env.ID, https.ID, Decision{Action: "allow", Host: "*.example.com"}); !errors.Is(err, ErrDoesNotCover) {
		t.Fatalf("a rule that doesn't cover the request was accepted: %v", err)
	}
	a, err := e.Resolve(ctx, env.ID, https.ID, Decision{Action: "allow", Host: "*.npmjs.org", Scope: "environment"})
	if err != nil || a.Status != store.StatusApproved || a.RuleID == "" {
		t.Fatalf("resolve: %+v %v", a, err)
	}
	for range 3 {
		r := <-held
		if r.err != nil || r.rule.Host != "*.npmjs.org" {
			t.Fatalf("held connection: %+v %v", r.rule, r.err)
		}
	}
	if left, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10); len(left) != 0 {
		t.Fatalf("the port-80 request was not settled by the new rule: %+v", left)
	}
	if _, err := e.Resolve(ctx, env.ID, https.ID, Decision{Action: "deny"}); !errors.Is(err, ErrDecided) {
		t.Fatalf("decided twice: %v", err)
	}
}

func TestHoldTimesOutAndDismiss(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 50 * time.Millisecond
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "example.org", Port: 443}
	if _, err := e.Decide(ctx, req); !errors.Is(err, ErrUndecided) {
		t.Fatalf("got %v", err)
	}
	pending, _ := e.Store.Approvals(ctx, "", store.StatusPending, 10)
	if len(pending) != 1 {
		t.Fatalf("the request should stay pending after the hold: %+v", pending)
	}

	e.Hold = 5 * time.Second
	done := make(chan error)
	go func() {
		_, err := e.Decide(ctx, req)
		done <- err
	}()
	for {
		if p, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10); len(p) == 1 && p[0].Attempts == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := e.Resolve(ctx, env.ID, pending[0].ID, Decision{Action: "dismiss"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrDismissed) {
		t.Fatalf("held connection after dismiss: %v", err)
	}
}
