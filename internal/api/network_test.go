package api

import (
	"bufio"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const testOrigin = "http://localhost:7878"

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	return v
}

func newEnvironment(t *testing.T, s *Server, name string) store.Environment {
	t.Helper()
	env, err := s.Store.CreateEnvironment(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func newSandbox(t *testing.T, s *Server, env store.Environment, name string) store.Sandbox {
	t.Helper()
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: name, CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

// awaitApproval polls the global inbox until a pending network approval for host:port exists.
func awaitApproval(t *testing.T, h http.Handler, host string, port int) ApprovalView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, a := range decode[[]ApprovalView](t, do(h, "GET", testOrigin+"/api/approvals", "")) {
			if a.Network != nil && a.Network.Host == host && a.Network.Port == port {
				return a
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pending approval for %s:%d", host, port)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRulesCRUD(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	rules := testOrigin + "/api/environments/" + env.ID + "/rules"

	rec := do(h, "POST", rules, `{"host":"*.Example.COM.","ports":[443],"action":"allow","note":"docs"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	created := decode[store.Rule](t, rec)
	if created.Host != "*.example.com" || !slices.Equal(created.Ports, []int{443}) || created.Action != store.ActionAllow || created.Note != "docs" || created.EnvironmentID != env.ID {
		t.Fatalf("created: %+v", created)
	}

	if _, err := s.Vault.Create(context.Background(), env.ID, "KEY", "value-of-key", []string{"api.example.org"}, ""); err != nil {
		t.Fatal(err)
	}
	rec = do(h, "POST", rules, `{"host":"api.example.org","action":"proxy","config":{"headers":{"x-api-key":"Bearer {secret.KEY}","x-static":"1"}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create proxy rule: %d %s", rec.Code, rec.Body)
	}
	proxy := decode[store.Rule](t, rec)
	if proxy.Action != store.ActionProxy || !maps.Equal(proxy.Config.Headers, map[string]string{"X-Api-Key": "Bearer {secret.KEY}", "X-Static": "1"}) {
		t.Fatalf("proxy rule: %+v", proxy)
	}

	// An update replaces the config; turning the rule into a deny rule drops its headers.
	rec = do(h, "PUT", rules+"/"+proxy.ID, `{"host":"api.example.net","action":"deny"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	updated := decode[store.Rule](t, rec)
	if updated.ID != proxy.ID || updated.Host != "api.example.net" || updated.Action != store.ActionDeny || len(updated.Ports) != 0 || len(updated.Config.Headers) != 0 {
		t.Fatalf("updated: %+v", updated)
	}

	if list := decode[[]store.Rule](t, do(h, "GET", rules, "")); len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	if rec := do(h, "DELETE", rules+"/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", rules+"/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", rec.Code)
	}
	if list := decode[[]store.Rule](t, do(h, "GET", rules, "")); len(list) != 1 || list[0].ID != proxy.ID {
		t.Fatalf("list after delete: %+v", list)
	}
}

func TestRuleValidation(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	foreign := newSandbox(t, s, other, "foreign")
	own := newSandbox(t, s, env, "own")
	rules := testOrigin + "/api/environments/" + env.ID + "/rules"

	for _, tc := range []struct{ name, body string }{
		{"bad pattern", `{"host":"*.*.com","action":"allow"}`},
		{"empty host", `{"host":" ","action":"allow"}`},
		{"caddy action", `{"host":"example.com","action":"caddy"}`},
		{"headers on an allow rule", `{"host":"example.com","action":"allow","config":{"headers":{"X-A":"1"}}}`},
		{"invalid header name", `{"host":"example.com","action":"proxy","config":{"headers":{"X A":"1"}}}`},
		{"Host header", `{"host":"example.com","action":"proxy","config":{"headers":{"host":"evil.com"}}}`},
		{"hop-by-hop header", `{"host":"example.com","action":"proxy","config":{"headers":{"Transfer-Encoding":"chunked"}}}`},
		{"header set twice", `{"host":"example.com","action":"proxy","config":{"headers":{"X-A":"1","x-a":"2"}}}`},
		{"newline in a value", `{"host":"example.com","action":"proxy","config":{"headers":{"X-A":"1\r\nX-B: 2"}}}`},
		{"unknown secret", `{"host":"example.com","action":"proxy","config":{"headers":{"Authorization":"Bearer {secret.NOPE}"}}}`},
		{"port out of range", `{"host":"example.com","ports":[70000],"action":"allow"}`},
		{"sandbox of another environment", `{"host":"example.com","action":"allow","sandboxId":"` + foreign.ID + `"}`},
		{"unknown sandbox", `{"host":"example.com","action":"allow","sandboxId":"nope"}`},
	} {
		if rec := do(h, "POST", rules, tc.body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422 (%s)", tc.name, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/nope/rules", `{"host":"example.com","action":"allow"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown environment: got %d, want 404", rec.Code)
	}

	rec := do(h, "POST", rules, `{"host":"example.com","action":"allow","sandboxId":"`+own.ID+`"}`)
	if rec.Code != http.StatusCreated || decode[store.Rule](t, rec).SandboxID != own.ID {
		t.Fatalf("sandbox rule: %d %s", rec.Code, rec.Body)
	}

	// Rules of another environment are invisible here, so changing them is a 404.
	theirs, err := s.Store.CreateRule(context.Background(), store.Rule{EnvironmentID: other.ID, Host: "example.com", Action: store.ActionAllow})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "PUT", rules+"/"+theirs.ID, `{"host":"example.org","action":"deny"}`); rec.Code != http.StatusNotFound {
		t.Errorf("update across environments: got %d, want 404", rec.Code)
	}
	if rec := do(h, "DELETE", rules+"/"+theirs.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete across environments: got %d, want 404", rec.Code)
	}
	if _, err := s.Store.Rule(context.Background(), other.ID, theirs.ID); err != nil {
		t.Fatalf("the other environment's rule was touched: %v", err)
	}
}

func TestDecideFlow(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	sb := newSandbox(t, s, env, "dev")
	approvals := testOrigin + "/api/environments/" + env.ID + "/approvals"

	type held struct {
		rule store.Rule
		err  error
	}
	ask := func(host string, port int) <-chan held {
		ch := make(chan held, 1)
		go func() {
			r, err := s.Policy.Decide(ctx, policy.Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Host: host, Port: port})
			ch <- held{r, err}
		}()
		return ch
	}

	npm := ask("registry.npmjs.org", 443)
	pending := awaitApproval(t, h, "registry.npmjs.org", 443)
	if pending.Status != store.StatusPending || pending.EnvironmentID != env.ID || pending.SandboxID != sb.ID {
		t.Fatalf("pending approval: %+v", pending)
	}
	if want := []string{"registry.npmjs.org", "*.npmjs.org"}; !slices.Equal(pending.Network.Patterns, want) || pending.Network.SandboxName != "dev" {
		t.Fatalf("network request: %+v", pending.Network)
	}

	decide := approvals + "/" + pending.ID + "/decide"
	rec := do(h, "POST", decide, `{"action":"allow","host":"*.npmjs.org"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("decide: %d %s", rec.Code, rec.Body)
	}
	decided := decode[ApprovalView](t, rec)
	if decided.Status != store.StatusApproved || decided.RuleID == "" || decided.Network == nil {
		t.Fatalf("decided: %+v", decided)
	}
	got := <-npm
	if got.err != nil || got.rule.ID != decided.RuleID || got.rule.Host != "*.npmjs.org" || got.rule.Action != store.ActionAllow {
		t.Fatalf("held connection: %+v %v", got.rule, got.err)
	}
	if !slices.Equal(got.rule.Ports, []int{80, 443}) {
		t.Fatalf("omitted ports should get the web defaults: %v", got.rule.Ports)
	}
	if rec := do(h, "POST", decide, `{"action":"deny"}`); rec.Code != http.StatusConflict {
		t.Fatalf("decided twice: %d %s", rec.Code, rec.Body)
	}

	// An explicit empty port list means any port, unlike an omitted one.
	anyPort := ask("api.example.com", 8443)
	pending = awaitApproval(t, h, "api.example.com", 8443)
	if rec := do(h, "POST", approvals+"/"+pending.ID+"/decide", `{"action":"allow","ports":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("any port: %d %s", rec.Code, rec.Body)
	}
	if got := <-anyPort; got.err != nil || len(got.rule.Ports) != 0 {
		t.Fatalf("explicit empty ports: %+v %v", got.rule, got.err)
	}

	sandboxOnly := ask("cdn.example.net", 443)
	pending = awaitApproval(t, h, "cdn.example.net", 443)
	if rec := do(h, "POST", approvals+"/"+pending.ID+"/decide", `{"action":"allow","scope":"sandbox"}`); rec.Code != http.StatusOK {
		t.Fatalf("sandbox scope: %d %s", rec.Code, rec.Body)
	}
	if got := <-sandboxOnly; got.err != nil || got.rule.SandboxID != sb.ID {
		t.Fatalf("sandbox-scoped rule: %+v %v", got.rule, got.err)
	}

	// A rule must cover the requested host and port, and the host must be a valid pattern.
	other := ask("example.org", 443)
	pending = awaitApproval(t, h, "example.org", 443)
	decide = approvals + "/" + pending.ID + "/decide"
	for _, body := range []string{
		`{"action":"allow","host":"*.npmjs.org"}`,
		`{"action":"allow","ports":[80]}`,
		`{"action":"allow","host":"*.*.org"}`,
	} {
		if rec := do(h, "POST", decide, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422 (%s)", body, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", decide, `{"action":"proxy"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("proxy action: got %d, want 422", rec.Code)
	}
	if rec := do(h, "POST", decide, `{"action":"dismiss"}`); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	if got := <-other; got.err != policy.ErrDismissed {
		t.Fatalf("dismissed connection: %v", got.err)
	}

	if list := decode[[]ApprovalView](t, do(h, "GET", approvals+"?status=pending", "")); len(list) != 0 {
		t.Fatalf("pending after decisions: %+v", list)
	}
	if list := decode[[]ApprovalView](t, do(h, "GET", approvals, "")); len(list) != 4 {
		t.Fatalf("all approvals of the environment: %+v", list)
	}
	if rec := do(h, "GET", testOrigin+"/api/environments/nope/approvals", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown environment: %d", rec.Code)
	}
	if rec := do(h, "GET", testOrigin+"/api/approvals?status=bogus", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad status filter: %d", rec.Code)
	}
}

func TestCreatedRuleSettlesPendingApproval(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	sb := newSandbox(t, s, env, "dev")
	base := testOrigin + "/api/environments/" + env.ID

	held := make(chan store.Rule, 1)
	go func() {
		r, _ := s.Policy.Decide(ctx, policy.Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "api.github.com", Port: 443})
		held <- r
	}()
	awaitApproval(t, h, "api.github.com", 443)

	rec := do(h, "POST", base+"/rules", `{"host":"*.github.com","action":"deny"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rule := decode[store.Rule](t, rec)
	if r := <-held; r.ID != rule.ID || r.Action != store.ActionDeny {
		t.Fatalf("held connection saw %+v, want rule %s", r, rule.ID)
	}
	denied := decode[[]ApprovalView](t, do(h, "GET", base+"/approvals?status=denied", ""))
	if len(denied) != 1 || denied[0].Status != store.StatusDenied || denied[0].RuleID != rule.ID {
		t.Fatalf("settled approvals: %+v", denied)
	}
}

func TestEventsStream(t *testing.T) {
	h, s := newTestServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers: %v", resp.Header)
	}

	done := make(chan struct{})
	defer close(done)
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-done:
				return
			}
		}
		close(lines)
	}()

	// The retry line is sent after the subscription exists, so events from here on are seen.
	awaitLine(t, lines, "retry: 2000")
	s.Bus.Publish(events.Event{Topic: events.TopicRules, EnvironmentID: "env1"})
	awaitLine(t, lines, `data: {"topic":"rules","environmentId":"env1"}`)
}

func awaitLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream ended before %q", want)
			}
			if l == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q within 5s", want)
		}
	}
}
