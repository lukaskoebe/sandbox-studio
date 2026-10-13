package policy

import (
	"context"
	"errors"
	"slices"
	"sync"
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
		r, ok := Match(rules, c.sandbox, "", c.host, c.port)
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

// testClock is the engine's clock in tests; held connections read it from their goroutines.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// awaitPending waits until the environment has a pending approval and returns it.
func awaitPending(t *testing.T, e *Engine, envID string) store.Approval {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if p, _ := e.Store.Approvals(context.Background(), envID, store.StatusPending, 10); len(p) > 0 {
			return p[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("no pending approval")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestExpiredRulesStopMatching(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	clock := &testClock{t: time.Unix(1_800_000_000, 0)}
	e.Now = clock.now
	e.Hold = 20 * time.Millisecond
	until := clock.now().Add(time.Hour)
	rule, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "cdn.example.org", Action: store.ActionAllow, Ports: []int{443}, Origin: store.OriginApproval, ExpiresAt: &until})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "cdn.example.org", Port: 443}
	if r, err := e.Decide(ctx, req); err != nil || r.ID != rule.ID {
		t.Fatalf("before expiry: %+v %v", r, err)
	}
	clock.advance(time.Hour - time.Second)
	if r, err := e.Decide(ctx, req); err != nil || r.ID != rule.ID {
		t.Fatalf("a second before expiry: %+v %v", r, err)
	}
	// From the expiry on the rule no longer matches, even before the sweep deletes it.
	clock.advance(time.Second)
	if _, err := e.Decide(ctx, req); !errors.Is(err, ErrUndecided) {
		t.Fatalf("at expiry: %v", err)
	}
	if err := e.Settle(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10); len(p) != 1 {
		t.Fatalf("an expired rule settled a request: %+v", p)
	}
	if rules, _ := e.Store.Rules(ctx, env.ID); len(rules) != 1 {
		t.Fatalf("the sweep has not run, so the rule is still stored: %+v", rules)
	}
}

func TestAllowForAnHour(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	clock := &testClock{t: time.Unix(1_800_000_000, 0)}
	e.Now = clock.now
	type result struct {
		rule store.Rule
		err  error
	}
	held := make(chan result, 1)
	go func() {
		r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Host: "api.example.org", Port: 443})
		held <- result{r, err}
	}()
	pending := awaitPending(t, e, env.ID)

	past := clock.now().Add(-time.Second)
	if _, err := e.Resolve(ctx, env.ID, pending.ID, Decision{Action: "allow", ExpiresAt: past}); !errors.Is(err, ErrExpired) {
		t.Fatalf("a rule that expired already was accepted: %v", err)
	}
	until := clock.now().Add(time.Hour)
	a, err := e.Resolve(ctx, env.ID, pending.ID, Decision{Action: "allow", Scope: "environment", ExpiresAt: until})
	if err != nil || a.Status != store.StatusApproved || a.RuleID == "" {
		t.Fatalf("resolve: %+v %v", a, err)
	}
	r := <-held
	if r.err != nil || r.rule.ExpiresAt == nil || r.rule.ExpiresAt.Unix() != until.Unix() {
		t.Fatalf("held connection: %+v %v", r.rule, r.err)
	}
	stored, err := e.Store.Rule(ctx, env.ID, a.RuleID)
	if err != nil || stored.Origin != store.OriginApproval || stored.ExpiresAt == nil || stored.ExpiresAt.Unix() != until.Unix() {
		t.Fatalf("stored rule: %+v %v", stored, err)
	}
}

func TestSweepDeletesExpiredRules(t *testing.T) {
	ctx := context.Background()
	e, env, _ := newEngine(t)
	clock := &testClock{t: time.Unix(1_800_000_000, 0)}
	e.Now = clock.now
	soon, later := clock.now().Add(time.Minute), clock.now().Add(time.Hour)
	for _, r := range []store.Rule{
		{Host: "short.example.org", ExpiresAt: &soon},
		{Host: "long.example.org", ExpiresAt: &later},
		{Host: "forever.example.org"},
	} {
		r.EnvironmentID, r.Action, r.Origin = env.ID, store.ActionAllow, store.OriginApproval
		if _, err := e.Store.CreateRule(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	sub, cancel := e.Bus.Subscribe()
	defer cancel()

	clock.advance(2 * time.Minute)
	if err := e.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	rules, err := e.Store.Rules(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, r := range rules {
		hosts = append(hosts, r.Host)
	}
	if want := []string{"forever.example.org", "long.example.org"}; !slices.Equal(hosts, want) {
		t.Fatalf("rules after the sweep: %v, want %v", hosts, want)
	}
	// Clients refetch the environment's rules after a sweep that deleted some.
	notified := false
	for len(sub) > 0 {
		if ev := <-sub; ev.Topic == events.TopicRules && ev.EnvironmentID == env.ID {
			notified = true
		}
	}
	if !notified {
		t.Fatal("no rules event after the sweep")
	}
}

func TestOnceDecisions(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	type result struct {
		rule store.Rule
		err  error
	}
	ask := func(host string) <-chan result {
		ch := make(chan result, 1)
		go func() {
			r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Host: host, Port: 443})
			ch <- result{r, err}
		}()
		return ch
	}

	allowed := ask("once.example.org")
	first := awaitPending(t, e, env.ID)
	a, err := e.Resolve(ctx, env.ID, first.ID, Decision{Action: ActionAllowOnce})
	if err != nil || a.Status != store.StatusApproved || !a.Once || a.RuleID != "" {
		t.Fatalf("allow once: %+v %v", a, err)
	}
	if r := <-allowed; r.err != nil || r.rule.Action != store.ActionAllow || r.rule.ID != "" {
		t.Fatalf("held connection after allow once: %+v %v", r.rule, r.err)
	}
	if rules, _ := e.Store.Rules(ctx, env.ID); len(rules) != 0 {
		t.Fatalf("allow once created a rule: %+v", rules)
	}
	if _, err := e.Resolve(ctx, env.ID, first.ID, Decision{Action: ActionDenyOnce}); !errors.Is(err, ErrDecided) {
		t.Fatalf("decided twice: %v", err)
	}

	// The next connection to the host asks again, and deny once refuses it, still without a rule.
	denied := ask("once.example.org")
	second := awaitPending(t, e, env.ID)
	if second.ID == first.ID {
		t.Fatalf("the decided request is still pending")
	}
	if a, err := e.Resolve(ctx, env.ID, second.ID, Decision{Action: ActionDenyOnce}); err != nil || a.Status != store.StatusDenied || !a.Once {
		t.Fatalf("deny once: %+v %v", a, err)
	}
	if r := <-denied; !errors.Is(r.err, ErrDenied) {
		t.Fatalf("held connection after deny once: %+v %v", r.rule, r.err)
	}
	if rules, _ := e.Store.Rules(ctx, env.ID); len(rules) != 0 {
		t.Fatalf("deny once created a rule: %+v", rules)
	}
	all, _ := e.Store.Approvals(ctx, env.ID, "", 10)
	if once := slices.DeleteFunc(all, func(a store.Approval) bool { return !a.Once }); len(once) != 2 {
		t.Fatalf("once decisions in the activity: %+v", all)
	}
}

// Connections count as hits of their rule in memory and reach the store in batches; a shutdown
// writes what is left.
func TestRuleHitsAreBatched(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	clock := &testClock{t: time.Unix(1_800_000_000, 0)}
	e.Now = clock.now
	rule, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "*.example.org", Action: store.ActionAllow, Origin: store.OriginManual})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "a.example.org", Port: 443}
	for range 3 {
		if _, err := e.Decide(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	stored, _ := e.Store.Rule(ctx, env.ID, rule.ID)
	if stored.Hits != 0 || stored.LastHitAt != nil {
		t.Fatalf("connections wrote to the store: %+v", stored)
	}
	if err := e.flushHits(ctx); err != nil {
		t.Fatal(err)
	}
	stored, _ = e.Store.Rule(ctx, env.ID, rule.ID)
	if stored.Hits != 3 || stored.LastHitAt == nil || stored.LastHitAt.Unix() != clock.now().Unix() {
		t.Fatalf("after the flush: %+v", stored)
	}

	clock.advance(time.Minute)
	if _, err := e.Decide(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := e.flushHits(ctx); err != nil {
		t.Fatal(err)
	}
	stored, _ = e.Store.Rule(ctx, env.ID, rule.ID)
	if stored.Hits != 4 || stored.LastHitAt.Unix() != clock.now().Unix() {
		t.Fatalf("after the second flush: %+v", stored)
	}

	// A rule deleted before its hits are written drops them.
	if _, err := e.Decide(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.DeleteRule(ctx, env.ID, rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.flushHits(ctx); err != nil || len(e.hits) != 0 {
		t.Fatalf("flush after a delete: %v, pending %v", err, e.hits)
	}
}

func TestRunWritesHitsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e, env, sb := newEngine(t)
	rule, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "example.org", Action: store.ActionAllow, Origin: store.OriginManual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "example.org", Port: 443}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		e.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}
	if stored, _ := e.Store.Rule(context.Background(), env.ID, rule.ID); stored.Hits != 1 {
		t.Fatalf("the shutdown did not write the hit: %+v", stored)
	}
}
