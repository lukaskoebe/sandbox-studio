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

// sampleApprovals sets three cards on the approvals screen, newest first: a git push, three
// hosts one sandbox asked for close together, and one network request that came alone.
func sampleApprovals(m *app) {
	t := m.now
	git := api.ApprovalView{
		Approval: store.Approval{ID: "g1", EnvironmentID: "env1", SandboxID: "sb2", Kind: "git.push", Status: "pending", CreatedAt: t.Add(-30 * time.Second)},
		Git: &gitreview.GitDetail{
			Push:   store.GitPush{ForgeName: "GitHub", Owner: "acme", Repo: "web", Ref: "refs/heads/feature"},
			Review: &gitreview.PushReview{Sandbox: "api-refactor", Branch: "feature/login", Commits: make([]gitreview.Commit, 2)},
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
	m.approvalCursor = 1
	m.screen = screenApprovals
	m = press(m, "r")
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

func TestNetworkKeysMakeTheirDecision(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	m.screen = screenApprovals
	m.approvalCursor = 2 // the single request
	m.client = nil
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if next.(*app).screen != screenApprovals || cmd == nil {
		t.Fatal("allow on a network card did not start a decision")
	}
	m.approvalCursor = 0
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd != nil {
		t.Fatal("allow on a git card decided it in the terminal")
	}
	if got := m.notice.text; got != "Review it first: enter opens the diff." {
		t.Fatalf("git card hint: %q", got)
	}
}

func TestApprovalsOpenFromAnywhereAndGoBack(t *testing.T) {
	m := press(sampleApp(), "j", "a")
	if m.screen != screenApprovals || m.back != screenHome {
		t.Fatalf("a: screen %v, back %v", m.screen, m.back)
	}
	if got := press(m, "esc").screen; got != screenHome {
		t.Fatalf("esc from approvals: %v", got)
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

// The approvals screen decides through the server: an allow on a card creates its rule and the
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
	m = run(m, m.loadApprovals())
	m = press(m, "a")
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

// pressCmd is press for one key, returning the command the key starts.
func pressCmd(m *app, key string) (*app, tea.Cmd) {
	var msg tea.KeyPressMsg
	if len(key) == 1 {
		msg = tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	}
	next, cmd := m.Update(msg)
	return next.(*app), cmd
}
