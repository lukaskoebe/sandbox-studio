package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// teamApp is the sample with its teammates loaded: Iris has blub and docs-site, Finn has
// api-refactor alone, and demo has no teammate.
func teamApp() *app {
	m := sampleApp()
	m.team = []store.Teammate{{ID: "t1", Name: "Iris", Role: "frontend"}, {ID: "t2", Name: "Finn"}}
	m.at, m.mate = atHome, ""
	return m
}

func TestTheTreeIsTeammateFirst(t *testing.T) {
	m := withSessions(teamApp())
	rows := m.homeRows()
	if rows[0].kind != atHome {
		t.Fatalf("the first row is %+v, not Home", rows[0])
	}
	var got []string
	for _, r := range rows {
		switch {
		case r.kind == atMate:
			got = append(got, "mate "+r.mate)
		case r.kind == atSandbox && r.sub < 0:
			got = append(got, r.label+"sandbox "+m.visible()[r.sb].Name)
		}
	}
	// Finn's one sandbox isn't a row: its sessions are right under him.
	want := []string{"mate t1", "sandbox blub", "sandbox docs-site", "mate t2", "no teammatesandbox demo"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("the tree: %v, want %v", got, want)
	}
	for _, r := range rows {
		if r.kind == atSandbox && r.sb >= 0 && m.visible()[r.sb].Name == "api-refactor" && r.indent != 0 {
			t.Errorf("Finn's sessions are indented by %d", r.indent)
		}
	}
	out := ansi.Strip(strings.Join(m.sidebar(80, 30), "\n"))
	for _, want := range []string{"Home", "need you", "Iris · 2 sandboxes", "Finn · api-refactor", "no teammate"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list lacks %q:\n%s", want, out)
		}
	}
}

func TestHomeIsWhereTheListStarts(t *testing.T) {
	m := teamApp()
	if m.selected() != nil {
		t.Fatal("Home's row selects a sandbox")
	}
	m = press(m, "j")
	if tm, ok := m.selectedMate(); !ok || tm.ID != "t1" {
		t.Fatalf("j goes to Iris: %+v %v", tm, ok)
	}
	m = press(m, "j", "j", "j")
	if m.at != atMate || m.mate != "t2" || m.selectedID() != "sb2" {
		t.Fatalf("Finn's row selects his one sandbox: at %v, mate %q, %q", m.at, m.mate, m.selectedID())
	}
	m = press(m, "esc")
	if m.at != atHome {
		t.Errorf("esc goes back to Home: %v", m.at)
	}
}

func TestHomePaneListsWhatNeedsYou(t *testing.T) {
	m := withSessions(teamApp())
	m.width = 140
	sampleApprovals(m)
	m.home.loaded = true
	m.home.decided = []api.ApprovalView{{
		Approval: store.Approval{ID: "d1", Kind: "git.push", Status: "approved", UpdatedAt: m.now.Add(-time.Hour)},
		Git: &gitreview.GitDetail{
			Push:   store.GitPush{ForgeName: "GitHub", Owner: "acme", Repo: "web", Ref: "refs/heads/fix"},
			Review: &gitreview.PushReview{Sandbox: "blub", Branch: "fix"},
		},
	}}
	out := ansi.Strip(strings.Join(m.needsPane(90, 30, false), "\n"))
	for _, want := range []string{"Needs you · ", "WORKING NOW", "EARLIER TODAY", "Pushed fix"} {
		if !strings.Contains(out, want) {
			t.Errorf("Home's pane lacks %q:\n%s", want, out)
		}
	}
	m = press(m, "tab")
	if m.approvalCursor != 1 {
		t.Errorf("tab moves to the next card: %d", m.approvalCursor)
	}
}

func TestTheTeammatePane(t *testing.T) {
	m := withSessions(teamApp())
	m.at, m.mate = atMate, "t1"
	m.mates = mateState{mate: "t1", loaded: true, tasks: []store.Task{{Title: "Ship the login", Status: "in_progress"}, {Title: "Write docs", Status: "done"}}}
	out := ansi.Strip(strings.Join(m.matePane(90, 40), "\n"))
	for _, want := range []string{"Iris · frontend", "SANDBOXES", "blub", "docs-site", "SESSIONS", "TASKS", "◐ Ship the login", "KNOWS", "nothing yet", "NETWORK", "Default, the environment's"} {
		if !strings.Contains(out, want) {
			t.Errorf("the teammate's pane lacks %q:\n%s", want, out)
		}
	}
	m.at, m.mate = atMate, "t2"
	if out := ansi.Strip(strings.Join(m.matePane(90, 40), "\n")); !strings.Contains(out, "COMPUTER") {
		t.Errorf("one sandbox is the teammate's computer:\n%s", out)
	}
}

func TestSettingsListsTheWebsSections(t *testing.T) {
	m := press(teamApp(), ",")
	if m.screen != screenSettings || m.activeTab() != tabSettings {
		t.Fatalf(", opens %v", m.screen)
	}
	out := ansi.Strip(strings.Join(m.settingsBody(80, 30), "\n"))
	for _, want := range []string{"THIS ENVIRONMENT", "Network", "ACCOUNTS", "Git forges", "THIS COMPUTER", "Updates"} {
		if !strings.Contains(out, want) {
			t.Errorf("Settings lacks %q:\n%s", want, out)
		}
	}
	m = press(m, "j", "enter")
	if !strings.Contains(m.notice.text, "/settings/network") {
		t.Errorf("enter on Network: %q", m.notice.text)
	}
	m = press(m, "j", "j", "j", "enter")
	if m.screen != screenActivity {
		t.Errorf("enter on Activity opens %v", m.screen)
	}
	if m = press(m, "esc"); m.screen != screenHome {
		t.Errorf("esc from Activity returns to %v", m.screen)
	}
}

func TestActivityNarrowsToAKind(t *testing.T) {
	m := sampleActivity(sampleApp())
	m.activity.decisions = append(m.activity.decisions, api.ApprovalView{
		Approval: store.Approval{ID: "g9", Kind: "git.push", Status: "denied", UpdatedAt: m.now.Add(-time.Minute)},
		Git:      &gitreview.GitDetail{Push: store.GitPush{Ref: "refs/heads/fix"}, Review: &gitreview.PushReview{Sandbox: "blub", Branch: "fix"}},
	})
	all := len(m.activityEntries())
	m = press(m, "right", "right")
	list := m.activityEntries()
	if len(list) != 1 || list[0].kind != "git" || all <= 1 {
		t.Fatalf("→ → narrows to git: %d of %d", len(list), all)
	}
	if out := ansi.Strip(strings.Join(m.activityBody(120, 20), "\n")); !strings.Contains(out, "[git]") {
		t.Errorf("the chips:\n%s", out)
	}
	m = press(m, "left", "left", "left")
	if activityKinds[m.activity.kind] != "memory" {
		t.Errorf("← wraps around: %s", activityKinds[m.activity.kind])
	}
	if len(m.activityEntries()) != 0 {
		t.Error("memory lists entries of other kinds")
	}
}

// visualTeamApp is the visual look's sample with its teammates, on Home's row.
func visualTeamApp(w, h int) *app {
	m := withSessions(visualApp(w, h))
	m.team = []store.Teammate{{ID: "t1", Name: "Iris", Role: "frontend"}, {ID: "t2", Name: "Finn"}}
	m.at, m.mate = atHome, ""
	sampleApprovals(m)
	m.home.loaded = true
	return m
}

func TestGoldenVisualHomeTeam(t *testing.T) {
	golden(t, "visual_home_team", visualTeamApp(140, 36))
}

func TestGoldenVisualHomeNarrow(t *testing.T) {
	golden(t, "visual_home_narrow", visualTeamApp(80, 24))
}

func TestGoldenVisualTeammate(t *testing.T) {
	m := press(visualTeamApp(140, 36), "j")
	m.mates = mateState{mate: "t1", loaded: true}
	golden(t, "visual_teammate", m)
}

// y on Home's card allows the request with a rule, and u takes the rule back.
func TestHomeDecidesAndUndoesThroughTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "api-refactor", runtime.StatusRunning)
	payload, _ := json.Marshal(map[string]any{"host": "pypi.org", "port": 443, "sandboxName": sb.Name, "patterns": []string{"pypi.org"}})
	if _, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: s.Env.ID, SandboxID: sb.ID, Kind: "network", Subject: "pypi.org:443", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	m := newApp(ctx, connect(t, s), s.Addr)
	m = run(m, m.loadEnvs())
	m = run(m, m.loadApprovals())
	if m.at != atHome || len(m.cards()) != 1 {
		t.Fatalf("Home's row with one card: at %v, %d cards", m.at, len(m.cards()))
	}
	m, cmd := pressCmd(m, "y")
	m = run(m, cmd)
	if rules, _ := s.Store.Rules(ctx, s.Env.ID); len(rules) != 1 || !strings.Contains(m.notice.text, "u undoes it") {
		t.Fatalf("y made %d rules; notice %q", len(rules), m.notice.text)
	}
	m, cmd = pressCmd(m, "u")
	m = run(m, cmd)
	if rules, _ := s.Store.Rules(ctx, s.Env.ID); len(rules) != 0 || !strings.HasPrefix(m.notice.text, "Undone") {
		t.Fatalf("u left %d rules; notice %q", len(rules), m.notice.text)
	}
	if m.undoable() != nil {
		t.Error("the undo can be pressed twice")
	}
}
