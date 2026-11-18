package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// netApproval is a pending network approval of sandbox sb1 that arrived at the given time.
func netApproval(id, host string, at time.Time, patterns ...string) api.ApprovalView {
	return api.ApprovalView{
		Approval: store.Approval{ID: id, EnvironmentID: "env1", SandboxID: "sb1", Kind: "network", Status: "pending", CreatedAt: at},
		Network:  &policy.NetworkRequest{Host: host, Port: 443, SandboxName: "blub", Patterns: patterns},
	}
}

// sampleApprovals sets three requests on Home's list, newest first: a git push, three
// hosts one sandbox asked for close together, and one network request that came alone.
func sampleApprovals(m *app) {
	t := m.now
	git := api.ApprovalView{
		Approval: store.Approval{ID: "g1", EnvironmentID: "env1", SandboxID: "sb2", Kind: "git.push", Status: "pending", CreatedAt: t.Add(-30 * time.Second)},
		Git: &gitreview.GitDetail{
			Push: store.GitPush{ForgeName: "GitHub", Owner: "acme", Repo: "web", Ref: "refs/heads/feature"},
			Review: &gitreview.PushReview{
				Sandbox: "api-refactor", Branch: "feature/login", New: "a4c2f0d9e1",
				Commits: []gitreview.Commit{
					{SHA: "a4c2f0d9e1", Author: "Finn", When: t.Add(-2 * time.Minute), Subject: "Test that enter submits the login form"},
					{SHA: "77d0b2c4aa", Author: "Finn", When: t.Add(-5 * time.Minute), Subject: "Submit the login form on enter"},
				},
				Files: 2, Additions: 7, Deletions: 1, Diff: sampleDiff,
			},
		},
	}
	group1 := netApproval("n2", "pypi.org", t.Add(-2*time.Minute+2*time.Second), "pypi.org", "*.pypi.org")
	group1.SandboxID, group1.Network.SandboxName = "sb2", "api-refactor"
	group2 := netApproval("n3", "files.pythonhosted.org", t.Add(-2*time.Minute+3*time.Second), "files.pythonhosted.org")
	group2.SandboxID, group2.Network.SandboxName = "sb2", "api-refactor"
	group3 := netApproval("n4", "pypi.python.org", t.Add(-2*time.Minute+4*time.Second), "pypi.python.org")
	group3.SandboxID, group3.Network.SandboxName = "sb2", "api-refactor"
	single := netApproval("n1", "registry.npmjs.org", t.Add(-10*time.Minute), "registry.npmjs.org", "*.npmjs.org")
	single.Teammate = &api.ApprovalTeammate{ID: "t1", Name: "Iris", ApprovalScope: "teammate", PatternLevel: "wildcard"}
	m.approvals = []api.ApprovalView{git, group3, group2, group1, single}
}

func TestCardsGroupCloseRequestsOfOneSandbox(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	cards := m.cards()
	if len(cards) != 3 {
		t.Fatalf("cards: %d, want git, a group of three hosts and a single request", len(cards))
	}
	if cards[0].first().ID != "g1" || cards[0].group {
		t.Errorf("git push card: %+v", cards[0])
	}
	if !cards[1].group || len(cards[1].approvals) != 3 {
		t.Errorf("hosts card: group %v, %d members", cards[1].group, len(cards[1].approvals))
	}
	if cards[2].group || cards[2].first().ID != "n1" {
		t.Errorf("single card: %+v", cards[2])
	}
}

func TestReviewOneByOneSplitsTheGroup(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	m = press(onCard(m, 1), "r")
	if got := len(m.cards()); got != 5 {
		t.Fatalf("after review one by one: %d cards, want every approval on its own", got)
	}
}

func TestRequestsFarApartDoNotGroup(t *testing.T) {
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	list := []api.ApprovalView{
		netApproval("a", "one.example", at),
		netApproval("b", "two.example", at.Add(10*time.Second)),
	}
	if got := len(approvalCards(list, nil)); got != 2 {
		t.Fatalf("requests ten seconds apart: %d cards", got)
	}
}

func TestNavigationStaysSingle(t *testing.T) {
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	nav := netApproval("nav", "example.com", at)
	nav.Network.Navigation, nav.Network.Site = true, "example.com"
	list := []api.ApprovalView{nav, netApproval("other", "other.example", at)}
	cards := approvalCards(list, nil)
	if len(cards) != 2 || cards[0].group {
		t.Fatalf("a navigation grouped: %+v", cards)
	}
}

func TestChoiceStartsWithTheTeammatesLevel(t *testing.T) {
	a := netApproval("n", "registry.npmjs.org", time.Now(), "registry.npmjs.org", "*.npmjs.org")
	a.Teammate = &api.ApprovalTeammate{ApprovalScope: "teammate", PatternLevel: "wildcard"}
	c := defaultChoice(a)
	if c.pattern != 1 || c.scope != "teammate" || c.anyPort {
		t.Fatalf("default choice: %+v", c)
	}
}

func TestPatternCycleVisitsTheRequestLevelLast(t *testing.T) {
	a := netApproval("n", "registry.npmjs.org", time.Now(), "registry.npmjs.org", "*.npmjs.org")
	a.Network.Method, a.Network.PathPattern = "GET", "/repos/*"
	c := choice{}
	var seen []string
	for range 4 {
		c.nextPattern(a)
		seen = append(seen, patternText(a, c))
	}
	want := []string{"*.npmjs.org", "GET /repos/*", "registry.npmjs.org", "*.npmjs.org"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("pattern cycle: %v, want %v", seen, want)
	}
}

// The wire format of a decision: the server reads a missing ports field as the default ports and
// an empty list as any port.
func TestDecisionBodiesOnTheWire(t *testing.T) {
	a := netApproval("n", "registry.npmjs.org", time.Now(), "registry.npmjs.org", "*.npmjs.org")
	body := func(d Decision) string {
		data, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := body(decisionFor(a, choice{scope: "environment"}, "allow")); got != `{"action":"allow","host":"registry.npmjs.org","scope":"environment"}` {
		t.Errorf("default ports: %s", got)
	}
	if got := body(decisionFor(a, choice{scope: "sandbox", anyPort: true, pattern: 1}, "deny")); !strings.Contains(got, `"ports":[]`) || !strings.Contains(got, `"host":"*.npmjs.org"`) {
		t.Errorf("any port: %s", got)
	}
	if got := body(decisionFor(a, choice{scope: "environment"}, "allow_once")); got != `{"action":"allow_once","scope":"environment"}` {
		t.Errorf("once: %s", got)
	}
}

// browserApproval is a pending request of sandbox sb1's browser, on a page of example.com.
func browserApproval(id, host string, at time.Time) api.ApprovalView {
	a := netApproval(id, host, at, host)
	a.Kind = "network.request"
	a.Network.Method, a.Network.Path, a.Network.PathPattern, a.Network.Site = "GET", "/app.js", "/app.js", "example.com"
	return a
}

// A browser request decides as the web card does: its reach picks the scope, and its rule covers
// only the browsers unless it reaches the sandboxes too.
func TestBrowserRequestDecisionBodies(t *testing.T) {
	a := browserApproval("b", "cdn.example.com", time.Now())
	body := func(a api.ApprovalView, c choice, action string) string {
		data, err := json.Marshal(decisionFor(a, c, action))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	c := defaultChoice(a)
	if c.reach != "browser" {
		t.Fatalf("default reach without a teammate: %q", c.reach)
	}
	if got := body(a, c, "allow"); got != `{"action":"allow","host":"cdn.example.com","scope":"sandbox","browserOnly":true}` {
		t.Errorf("this browser: %s", got)
	}
	c.reach = "browsers"
	if got := body(a, c, "deny"); got != `{"action":"deny","host":"cdn.example.com","scope":"environment","browserOnly":true}` {
		t.Errorf("all browsers: %s", got)
	}
	c.reach = "everywhere"
	if got := body(a, c, "allow"); got != `{"action":"allow","host":"cdn.example.com","scope":"environment"}` {
		t.Errorf("browsers and sandboxes: %s", got)
	}
	if got := body(a, defaultChoice(a), "allow_once"); got != `{"action":"allow_once","scope":"sandbox"}` {
		t.Errorf("once: %s", got)
	}

	a.Teammate = &api.ApprovalTeammate{ID: "t1", Name: "Iris", ApprovalScope: "teammate"}
	if got := body(a, defaultChoice(a), "allow"); got != `{"action":"allow","host":"cdn.example.com","scope":"teammate","browserOnly":true}` {
		t.Errorf("the teammate's browser: %s", got)
	}
	a.Teammate.ApprovalScope = "environment"
	if c := defaultChoice(a); c.reach != "browsers" {
		t.Errorf("default reach of a teammate that decides for the environment: %q", c.reach)
	}
}

// The scope key cycles a browser request's reach, and the card says who its rule covers.
func TestScopeKeyCyclesABrowserRequestsReach(t *testing.T) {
	m := sampleApp()
	a := browserApproval("b", "cdn.example.com", m.now)
	a.Teammate = &api.ApprovalTeammate{ID: "t1", Name: "Iris", ApprovalScope: "teammate"}
	m.approvals = []api.ApprovalView{a}
	m = onCard(m, 0)
	var seen []string
	for range 3 {
		seen = append(seen, stripANSI(strings.Join(m.cardLines(m.cards()[0], true, 120), "\n")))
		m = press(m, "s")
	}
	for i, want := range []string{"who Iris's browser", "who all browsers", "who browsers and computers"} {
		if !strings.Contains(seen[i], want) || strings.Contains(seen[i], "scope ") {
			t.Errorf("after %d presses: %q, want %q", i, seen[i], want)
		}
	}
	if got := m.choiceOf(m.cards()[0]).reach; got != "browser" {
		t.Errorf("reach after a full cycle: %q", got)
	}
}

func TestNetworkKeysMakeTheirDecision(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	m = onCard(m, 2) // the single request
	m.client = nil
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !next.(*app).inboxShows() || cmd == nil {
		t.Fatal("allow on a network card did not start a decision")
	}
}

// \ gives the keys to the sidebar and back to Home's list.
func TestBackslashMovesBetweenTheListAndTheSidebar(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	if !m.inboxKeys() {
		t.Fatal("Home's list starts without the keys")
	}
	m = press(m, "\\")
	if m.inboxKeys() || !m.sidebarKeys() || !m.drawer || m.at != atHome {
		t.Fatalf("\\ from the list: at %v, drawer %v, the list has the keys %v", m.at, m.drawer, m.inboxKeys())
	}
	if m = press(m, "j"); m.at == atHome {
		t.Fatal("j after \\ moved the list's cursor, not the sidebar's")
	}
	if m = press(m, "esc"); !m.inboxKeys() || m.drawer {
		t.Fatal("esc in the drawer did not give the list the keys")
	}
	m = press(m, "\\", "\\")
	if !m.inboxKeys() || m.drawer {
		t.Fatalf("\\ twice: drawer %v, the list has the keys %v", m.drawer, m.inboxKeys())
	}
	m.width = 140 // a sidebar beside the list
	m = press(m, "\\")
	if !m.sidebarKeys() || m.drawer {
		t.Fatalf("\\ at 140 columns: drawer %v, the sidebar has the keys %v", m.drawer, m.sidebarKeys())
	}
}

func TestBellRingsForAnApprovalThatArrivesWhileOpen(t *testing.T) {
	m := sampleApp()
	m.primed = true
	m.known = map[string]bool{"a1": true}
	cmd := m.setApprovals(append(m.approvals, netApproval("fresh", "new.example", m.now)))
	if cmd == nil {
		t.Fatal("no bell for a new approval")
	}
}

// Deciding goes through the server: an allow creates a rule, and a once decision creates none.
func TestDecisionsCreateRulesOnTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	c := connect(t, s)
	request := func(host string) store.Approval {
		payload, _ := json.Marshal(map[string]any{"host": host, "port": 443, "sandboxName": "blub", "patterns": []string{host}})
		a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: s.Env.ID, SandboxID: sb.ID, Kind: "network", Subject: host + ":443", Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	allowed := request("registry.npmjs.org")
	if _, err := c.Decide(ctx, s.Env.ID, allowed.ID, decisionFor(api.ApprovalView{Approval: allowed, Network: &policy.NetworkRequest{Host: "registry.npmjs.org", Port: 443, Patterns: []string{"registry.npmjs.org"}}}, choice{scope: "environment", anyPort: true}, "allow")); err != nil {
		t.Fatal(err)
	}
	rules, err := s.Store.Rules(ctx, s.Env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Host != "registry.npmjs.org" || len(rules[0].Ports) != 0 || rules[0].Ports == nil {
		t.Fatalf("rules after allow: %+v", rules)
	}

	once := request("pypi.org")
	if _, err := c.Decide(ctx, s.Env.ID, once.ID, Decision{Action: "allow_once", Scope: "environment"}); err != nil {
		t.Fatal(err)
	}
	if rules, _ := s.Store.Rules(ctx, s.Env.ID); len(rules) != 1 {
		t.Fatalf("a once decision made a rule: %d rules", len(rules))
	}
	if list, _ := c.PendingApprovals(ctx); len(list) != 0 {
		t.Fatalf("decided approvals still pending: %d", len(list))
	}
}

// Home's list decides through the server: an allow on a card creates its rule and the
// card goes; allow all on a group creates a rule per host.
func TestApprovalsScreenDecidesThroughTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "api-refactor", runtime.StatusRunning)
	request := func(host string) {
		payload, _ := json.Marshal(map[string]any{"host": host, "port": 443, "sandboxName": sb.Name, "patterns": []string{host}})
		if _, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: s.Env.ID, SandboxID: sb.ID, Kind: "network", Subject: host + ":443", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	request("pypi.org")
	request("files.pythonhosted.org")
	request("pypi.python.org")

	m := newApp(ctx, connect(t, s), s.Addr)
	m.env, m.loaded = s.Env, true
	m = run(m, m.loadApprovals())
	m = onCard(m, 0)
	if cards := m.cards(); len(cards) != 1 || !cards[0].group {
		t.Fatalf("three close requests: %d cards", len(cards))
	}

	// Allow all: one rule per host, then the stack is empty.
	m, cmd := pressCmd(m, "A")
	m = run(m, cmd)
	if m.notice.text != "Allowed 3 hosts." || m.notice.failed {
		t.Fatalf("allow all notice: %+v", m.notice)
	}
	if rules, _ := s.Store.Rules(ctx, s.Env.ID); len(rules) != 3 {
		t.Fatalf("allow all made %d rules, want 3", len(rules))
	}
	m = run(m, m.loadApprovals()) // the refresh a decision starts
	if len(m.approvals) != 0 {
		t.Fatalf("decided approvals still on the stack: %d", len(m.approvals))
	}
}

// Allow all on a browser page's group makes rules for the browser only: the sandbox that asked
// gets no rule for its own connections.
func TestBrowserPageGroupAllowsTheBrowserOnly(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "api-refactor", runtime.StatusRunning)
	request := func(host string) {
		payload, _ := json.Marshal(map[string]any{"host": host, "port": 443, "sandboxName": sb.Name, "patterns": []string{host},
			"method": "GET", "path": "/app.js", "site": "example.com", "pathPattern": "/app.js"})
		if _, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: s.Env.ID, SandboxID: sb.ID, Kind: "network.request", Subject: host + ":443 example.com", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	request("cdn.example.com")
	request("fonts.example.com")

	m := newApp(ctx, connect(t, s), s.Addr)
	m.env, m.loaded = s.Env, true
	m = run(m, m.loadApprovals())
	m = onCard(m, 0)
	if cards := m.cards(); len(cards) != 1 || !cards[0].group {
		t.Fatalf("one page's requests: %d cards", len(cards))
	}
	m, cmd := pressCmd(m, "A")
	m = run(m, cmd)
	if m.notice.text != "Allowed 2 hosts." || m.notice.failed {
		t.Fatalf("allow all notice: %+v", m.notice)
	}
	rules, err := s.Store.Rules(ctx, s.Env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("allow all made %d rules, want 2", len(rules))
	}
	for _, r := range rules {
		if !r.BrowserOnly || r.SandboxID != sb.ID {
			t.Errorf("rule %s: browser only %v, sandbox %q", r.Host, r.BrowserOnly, r.SandboxID)
		}
	}
}

// pressCmd is press for one key, returning the command the key starts.
func pressCmd(m *app, key string) (*app, tea.Cmd) {
	next, cmd := m.Update(keyMsg(key))
	return next.(*app), cmd
}
