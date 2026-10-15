package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// sampleActivity opens the Activity screen over a fixed log: connections of blub and
// api-refactor, a decision with a rule, one taken once, and one the curated list made.
func sampleActivity(m *app) *app {
	m = press(m, "l")
	t := m.now
	ago := func(d time.Duration) time.Time { return t.Add(-d) }
	ended := ago(4 * time.Second)
	hit := ago(time.Minute)
	expires := t.Add(50 * time.Minute)
	a := &m.activity
	a.loaded = true
	a.conns = map[string][]gateway.Conn{
		"sb1": {
			{ID: 1, Started: ago(12 * time.Second), Ended: &ended, Host: "registry.npmjs.org", Port: 443, Verdict: gateway.VerdictAllowed, RuleID: "r1"},
			{ID: 2, Started: ago(40 * time.Second), Host: "tracker.example.com", Port: 443, Verdict: gateway.VerdictDenied, RuleID: "r2"},
			{ID: 3, Started: ago(3 * time.Minute), Host: "nope.invalid", Verdict: gateway.VerdictFailed, Error: "no such host"},
		},
		"sb2": {
			{ID: 4, Started: ago(20 * time.Second), Host: "proxy.golang.org", Port: 443, Verdict: gateway.VerdictAllowed},
		},
	}
	decided := func(id, sandbox, host, status, rule string, at time.Time) api.ApprovalView {
		return api.ApprovalView{
			Approval: store.Approval{ID: id, SandboxID: sandbox, Kind: "network", Status: status, RuleID: rule, UpdatedAt: at},
			Network:  &policy.NetworkRequest{Host: host, Port: 443},
		}
	}
	once := decided("d2", "sb2", "api.github.com", "approved", "", ago(2*time.Minute))
	once.Once = true
	curated := decided("d3", "sb2", "proxy.golang.org", "approved", "", ago(21*time.Second))
	curated.Network.Decided = "curated"
	a.decisions = []api.ApprovalView{
		decided("d1", "sb1", "registry.npmjs.org", "approved", "r1", ago(time.Minute)),
		once, curated,
		decided("d4", "sb1", "pypi.org", "pending", "", ago(5*time.Second)),
	}
	a.rules = map[string]store.Rule{
		"r1": {ID: "r1", TeammateID: "t1", Host: "*.npmjs.org", Ports: []int{443}, Action: "allow", Origin: "approval", Hits: 14, LastHitAt: &hit, ExpiresAt: &expires, CreatedAt: ago(26 * time.Hour), Note: "npm installs"},
		"r2": {ID: "r2", Host: "tracker.example.com", Action: "deny", Origin: "manual", CreatedAt: ago(72 * time.Hour)},
	}
	return m
}

func TestGoldenActivity(t *testing.T) {
	golden(t, "activity", sampleActivity(sampleApp()))
}

func TestGoldenActivityEntry(t *testing.T) {
	golden(t, "activity_entry", press(sampleActivity(sampleApp()), "enter"))
}

func TestGoldenActivityFiltered(t *testing.T) {
	// l on api-refactor's detail narrows the log to it.
	golden(t, "activity_filtered", sampleActivity(press(sampleApp(), "j", "enter")))
}

func TestActivityListsNewestFirstWithoutPending(t *testing.T) {
	m := sampleActivity(sampleApp())
	var hosts []string
	for _, e := range m.activityEntries() {
		hosts = append(hosts, e.host)
		if e.host == "pypi.org:443" {
			t.Error("a pending request is in the log")
		}
	}
	want := "registry.npmjs.org:443,proxy.golang.org:443,proxy.golang.org:443,tracker.example.com:443,registry.npmjs.org:443,api.github.com:443,nope.invalid"
	if got := strings.Join(hosts, ","); got != want {
		t.Fatalf("entries\n got %s\nwant %s", got, want)
	}
}

func TestActivityFiltersBySandbox(t *testing.T) {
	m := sampleActivity(sampleApp())
	m = press(m, "/", "a", "p", "i", "enter")
	if m.filtering || m.activity.filter != "api" {
		t.Fatalf("filter: typing %v, %q", m.filtering, m.activity.filter)
	}
	for _, e := range m.activityEntries() {
		if e.sandbox != "api-refactor" {
			t.Fatalf("an entry of %s passes the filter", e.sandbox)
		}
	}
	if m = press(m, "esc"); m.activity.filter != "" || m.screen != screenActivity {
		t.Fatalf("esc: filter %q, screen %v", m.activity.filter, m.screen)
	}
	if m = press(m, "esc"); m.screen != screenHome {
		t.Fatalf("esc again returns to %v", m.screen)
	}
	m = press(sampleApp(), "j", "enter", "l")
	if m.screen != screenActivity || m.activity.filter != "api-refactor" {
		t.Fatalf("l on the detail: screen %v, filter %q", m.screen, m.activity.filter)
	}
	if m = press(m, "q"); m.screen != screenDetail {
		t.Fatalf("q returns to %v", m.screen)
	}
}

func TestActivityEntryShowsItsRule(t *testing.T) {
	m := press(sampleActivity(sampleApp()), "enter")
	body := strings.Join(m.entryBody(80, 19), "\n")
	for _, want := range []string{"*.npmjs.org", "Iris's sandboxes", "an approval", "14, the last 1m ago", "npm installs"} {
		if !strings.Contains(body, want) {
			t.Errorf("the entry lacks %q:\n%s", want, body)
		}
	}
	m = press(m, "esc", "j", "j")
	m = press(m, "enter") // the curated decision
	if body := strings.Join(m.entryBody(80, 19), "\n"); !strings.Contains(body, "curated list") {
		t.Fatalf("the curated decision:\n%s", body)
	}
}

func TestDecisionWords(t *testing.T) {
	view := func(status string, n *policy.NetworkRequest) api.ApprovalView {
		return api.ApprovalView{Approval: store.Approval{Status: status}, Network: n}
	}
	once := view("denied", nil)
	once.Once = true
	reverted := view("approved", &policy.NetworkRequest{Decided: "reviewer"})
	reverted.Reverted = true
	for _, c := range []struct {
		a    api.ApprovalView
		want string
	}{
		{view("approved", nil), "Allowed"},
		{view("dismissed", nil), "Ignored"},
		{once, "Denied once"},
		{reverted, "Reverted · reviewer"},
		{view("denied", &policy.NetworkRequest{Decided: "reviewer"}), "Denied · reviewer"},
		{view("approved", &policy.NetworkRequest{Decided: "open"}), "Allowed · level Open"},
		{view("denied", &policy.NetworkRequest{Tracker: true}), "Denied · tracker"},
	} {
		if got := decisionWords(c.a); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a.Approval, got, c.want)
		}
	}
}

func TestStaleActivityTicksStop(t *testing.T) {
	m := sampleActivity(sampleApp())
	gen := m.activity.gen
	m = press(m, "esc")
	if cmd, _ := m.activityUpdate(activityTick{gen: gen}); cmd != nil {
		t.Fatal("a tick reloads the log after the screen closed")
	}
	m = press(m, "l")
	if cmd, _ := m.activityUpdate(activityTick{gen: gen}); cmd != nil {
		t.Fatal("a tick of an earlier visit reloads the log")
	}
}

func TestActivityLoadsFromTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	rule, err := s.Store.CreateRule(ctx, store.Rule{EnvironmentID: s.Env.ID, Host: "*.npmjs.org", Ports: []int{443}, Action: "allow", Origin: "approval"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"host": "registry.npmjs.org", "port": 443, "sandboxName": "blub"})
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: s.Env.ID, SandboxID: sb.ID, Kind: "network", Subject: "registry.npmjs.org:443", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DecideApproval(ctx, s.Env.ID, a.ID, "approved", rule.ID); err != nil {
		t.Fatal(err)
	}
	m := newApp(ctx, connect(t, s), s.Addr)
	m.env = s.Env
	m = run(m, m.loadSandboxes())
	m = run(m, m.openActivity(""))
	list := m.activityEntries()
	if !m.activity.loaded || len(list) != 1 {
		t.Fatalf("activity: loaded %v, %d entries, notice %q", m.activity.loaded, len(list), m.notice.text)
	}
	if e := list[0]; e.sandbox != "blub" || e.verdict != "Allowed" || m.ruleCell(e) != "*.npmjs.org" {
		t.Fatalf("entry: %+v, rule %q", e, m.ruleCell(e))
	}
}
