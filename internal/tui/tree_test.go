package tui

import (
	"context"
	"encoding/json"
	"fmt"
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
	m.at, m.mate = atHome, ""
	return m
}

func TestTheTreeIsTeammateFirst(t *testing.T) {
	m := withSessions(teamApp())
	rows := m.homeRows()
	if rows[0].kind != atHome {
		t.Fatalf("the first row is %+v, not Home", rows[0])
	}
	visible := m.visible()
	var got []string
	for _, r := range rows {
		label := ""
		if r.section != "" {
			label = "[" + r.section + "] "
		}
		switch r.kind {
		case atMate:
			got = append(got, label+"mate "+r.mate)
		case atThread:
			x, _ := m.rowSession(r, visible)
			got = append(got, label+fmt.Sprintf("%d ", r.num)+x.title())
		case atSandbox:
			if r.sub < 0 {
				got = append(got, label+"computer "+visible[r.sb].Name)
			}
		case atAction:
			got = append(got, label+map[rowAction]string{actionNewTeammate: "+ teammate", actionNewComputer: "+ computer"}[r.action])
		case atPlace:
			got = append(got, label+placeName(r.place))
		}
	}
	// A teammate's threads are right under it, and its computers have no row of their own:
	// they are on the Computers screen.
	want := []string{
		"[TEAMMATES] mate t1", "1 claude", "2 Fix the login form", "3 Update the deploy notes", "0 + 4 older", "4 main",
		"mate t2", "5 codex", "6 Port the parser to go", "7 main", "+ teammate",
		"[YOUR COMPUTERS] computer demo", "+ computer",
		"[ENVIRONMENT] Computers", "Network", "Secrets", "Memory", "Activity", "Settings",
	}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("the tree:\n%v\nwant\n%v", got, want)
	}
	out := ansi.Strip(strings.Join(m.sidebar(80, 40), "\n"))
	for _, want := range []string{"Home", "waiting for you", "TEAMMATES", "Iris", "frontend", "YOUR COMPUTERS", "demo", "ENVIRONMENT", "+ New teammate", "+ New computer"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list lacks %q:\n%s", want, out)
		}
	}
}

func TestOlderThreadsShowWithRightAndHideWithLeft(t *testing.T) {
	m := press(withSessions(teamApp()), "j") // Iris
	before := len(m.threadsOf("t1"))
	m = press(m, "right")
	if got := len(m.threadsOf("t1")); got != before+3 { // four older threads instead of their row
		t.Fatalf("→ shows %d rows under Iris, had %d", got, before)
	}
	m = press(m, "left")
	if got := len(m.threadsOf("t1")); got != before {
		t.Fatalf("← leaves %d rows under Iris, want %d", got, before)
	}
	if m.at != atMate || m.mate != "t1" {
		t.Fatalf("the cursor left Iris: %v %q", m.at, m.mate)
	}
}

func TestThreadsWaitOnTheirOwnRequests(t *testing.T) {
	m := withSessions(teamApp())
	c2 := m.sess.chats["sb2"][0]
	if got := m.threadWaiting(c2); len(got) != 2 { // its permission, and api-refactor's network request
		t.Fatalf("Port the parser waits on %d requests: %+v", len(got), got)
	}
	// An older thread of the same teammate on the computer doesn't take the computer's requests.
	older := store.Chat{ID: "c0", SandboxID: "sb2", TeammateID: "t2", UpdatedAt: m.now.Add(-time.Hour)}
	m.sess.chats["sb2"] = append(m.sess.chats["sb2"], older)
	if got := m.threadWaiting(older); len(got) != 0 {
		t.Fatalf("the older thread waits on %+v", got)
	}
	// Another teammate's request on the computer is not this thread's.
	m.approvals[0].Teammate = &api.ApprovalTeammate{ID: "t1", Name: "Iris"}
	if got := m.threadWaiting(c2); len(got) != 1 {
		t.Fatalf("Port the parser takes Iris's request: %+v", got)
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
	m = press(m, "j")
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
	for _, want := range []string{"Waiting for you · ", "WORKING NOW", "EARLIER TODAY", "Pushed fix"} {
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
	for _, want := range []string{"Iris · frontend", "COMPUTERS", "blub", "docs-site", "THREADS", "Fix the login form", "TASKS", "◐ Ship the login", "KNOWS", "nothing yet", "NETWORK", "Default, the environment's"} {
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

func TestGoldenComputers(t *testing.T) {
	golden(t, "computers", onComputersScreen(t, sampleApp(), "sb2"))
}

func TestGoldenWideComputers(t *testing.T) {
	goldenVisual(t, "wide_computers_140x40", withDetailData(onComputersScreen(t, visualApp(140, 40), "sb2")), false)
}

func TestGoldenHomeThreads(t *testing.T) {
	m := sampleMorning(sampleApp())
	golden(t, "home_threads", onSession(t, m, "sb1", "Fix the login form"))
}

func TestGoldenWideHomeThreads(t *testing.T) {
	m := sampleMorning(visualApp(140, 40))
	goldenVisual(t, "wide_home_threads_140x40", onSession(t, m, "sb1", "Fix the login form"), false)
}

// enter on an ENVIRONMENT row opens its place; on Computers, esc puts the cursor back.
func TestPlacesOpenFromTheList(t *testing.T) {
	m := teamApp()
	on := func(p envPlace) *app {
		for _, r := range m.homeRows() {
			if r.kind == atPlace && r.place == p {
				m.setCursor(r)
				return m
			}
		}
		t.Fatalf("no row of %v", p)
		return m
	}
	m = press(on(placeComputers), "enter")
	if m.screen != screenComputers {
		t.Fatalf("enter on Computers opens %v", m.screen)
	}
	m = press(m, "j", "esc")
	if m.screen != screenHome || m.at != atPlace || m.rowPlace != placeComputers {
		t.Fatalf("esc from Computers: screen %v, at %v, place %v", m.screen, m.at, m.rowPlace)
	}
	m, _ = pressCmd(on(placeNetwork), "enter")
	if m.screen != screenActivity || activityKinds[m.activity.kind] != "network" {
		t.Fatalf("enter on Network opens %v, kind %d", m.screen, m.activity.kind)
	}
	m = press(m, "esc")
	m = press(on(placeSecrets), "enter")
	if !strings.Contains(m.notice.text, "/e/env1/secrets") {
		t.Fatalf("Secrets prints its address: %q", m.notice.text)
	}
	m, _ = pressCmd(on(placeSettings), "enter")
	if m.screen != screenSettings {
		t.Fatalf("enter on Settings opens %v", m.screen)
	}
}

// The Computers screen takes a computer's keys, and enter opens its detail, which goes back to
// the screen.
func TestComputersScreenTakesAComputersKeys(t *testing.T) {
	m := onComputersScreen(t, sampleApp(), "sb3") // docs-site, stopped
	if m.selectedID() != "sb3" {
		t.Fatalf("the screen selects %q", m.selectedID())
	}
	if _, cmd := pressCmd(m, "s"); cmd == nil {
		t.Error("s on a stopped computer starts nothing")
	}
	m = press(m, "enter")
	if m.screen != screenDetail || m.selectedID() != "sb3" {
		t.Fatalf("enter: screen %v, %q", m.screen, m.selectedID())
	}
	m = press(m, "esc")
	if m.screen != screenComputers {
		t.Fatalf("esc from the detail goes to %v", m.screen)
	}
	m = press(m, "k", "k", "k", "k")
	if m.selectedID() != "sb1" {
		t.Fatalf("k stops on the first computer: %q", m.selectedID())
	}
}

// The cursor stays on its thread when a read moves the thread to another row, and when the
// computers reload.
func TestTheCursorStaysOnItsThreadAcrossReads(t *testing.T) {
	m := onSession(t, sampleMorning(sampleApp()), "sb1", "Fix the login form")
	chats := map[string][]store.Chat{}
	for id, list := range m.sess.chats {
		chats[id] = append([]store.Chat(nil), list...)
	}
	chats["sb3"] = append(chats["sb3"], store.Chat{ID: "c9", SandboxID: "sb3", TeammateID: "t1", Title: "A newer one", State: store.ChatRunning, UpdatedAt: m.now})
	m.sessionsRead(sessionsLoaded{at: m.now, chats: chats})
	if x, ok := m.selectedSession(); !ok || x.title() != "Fix the login form" {
		t.Fatalf("after a read the cursor is on %+v", x)
	}
	next, _ := m.Update(sandboxesLoaded{env: m.env.ID, list: m.sandboxes, teammates: m.team})
	m = next.(*app)
	if x, ok := m.selectedSession(); !ok || x.title() != "Fix the login form" || m.selectedID() != "sb1" {
		t.Fatalf("after the computers reloaded the cursor is on %+v, %s", x, m.selectedID())
	}
}

// A teammate with no computer has a row, and its keys say + gives it one.
func TestATeammateWithoutAComputer(t *testing.T) {
	m := teamApp()
	m.team = append(m.team, store.Teammate{ID: "t3", Name: "Ada", Role: "docs"})
	var ada homeRow
	for _, r := range m.homeRows() {
		if r.kind == atMate && r.mate == "t3" {
			ada = r
		}
	}
	if ada.mate != "t3" {
		t.Fatal("Ada has no row")
	}
	m.setCursor(ada)
	m = settle(m)
	if m.selected() != nil {
		t.Fatalf("Ada's row selects %s", m.selectedID())
	}
	m, _ = pressCmd(m, "c")
	if !strings.Contains(m.notice.text, "Ada has no computer yet. + gives it one.") {
		t.Fatalf("c on Ada: %q", m.notice.text)
	}
	pane := ansi.Strip(strings.Join(m.matePane(80, 24), "\n"))
	if !strings.Contains(pane, "none yet; + gives Ada a computer of its own") || strings.Contains(pane, "c starts one") {
		t.Fatalf("Ada's pane:\n%s", pane)
	}
}
