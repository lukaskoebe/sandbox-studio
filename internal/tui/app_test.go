package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// sampleApp is the terminal UI with a fixed catalog: four sandboxes in one environment, one of
// them with a pending approval. Its clock is fixed, so the uptimes are too.
func sampleApp() *app {
	m := newApp(context.Background(), nil, "127.0.0.1:7878")
	m.width, m.height = defaultWidth, defaultHeight
	m.now = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	m.env = store.Environment{ID: "env1", Name: "Default"}
	m.envs = []store.Environment{m.env, {ID: "env2", Name: "work"}}
	m.loaded = true
	m.teammates = map[string]string{"t1": "Iris", "t2": "Finn"}
	started := m.now.Add(-2*time.Hour - 14*time.Minute)
	recent := m.now.Add(-12 * time.Minute)
	m.sandboxes = []sandboxes.View{
		{Sandbox: store.Sandbox{ID: "sb1", Name: "blub", TeammateID: "t1", StartedAt: &started, CPUs: 2, MemoryMiB: 2048}, Status: "running", Agent: &sandboxes.AgentInfo{}, TemplateName: "node-22+python-3.13@2"},
		{Sandbox: store.Sandbox{ID: "sb2", Name: "api-refactor", TeammateID: "t2", StartedAt: &recent, CPUs: 2, MemoryMiB: 2048}, Status: "running", Agent: &sandboxes.AgentInfo{}, TemplateName: "go@2"},
		{Sandbox: store.Sandbox{ID: "sb3", Name: "docs-site", TeammateID: "t1"}, Status: "stopped", TemplateName: "node@3"},
		{Sandbox: store.Sandbox{ID: "sb4", Name: "demo"}, Status: "starting", TemplateName: "base"},
	}
	m.approvals = []api.ApprovalView{{Approval: store.Approval{ID: "a1", SandboxID: "sb2", Kind: "network", Status: "pending"}}}
	return m
}

func press(m *app, keys ...string) *app {
	for _, k := range keys {
		next, _ := m.Update(keyMsg(k))
		m = next.(*app)
	}
	return m
}

// keyMsg is the key press of a key's name, as press takes it.
func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
	}
}

// run executes a command and applies its message, the way the program does.
func run(m *app, cmd tea.Cmd) *app {
	if cmd == nil {
		return m
	}
	next, _ := m.Update(cmd())
	return next.(*app)
}

func TestCursorStaysInTheList(t *testing.T) {
	m := sampleApp()
	press(m, "k")
	if m.cursor != 0 {
		t.Fatalf("cursor above the first row: %d", m.cursor)
	}
	press(m, "j", "j", "j", "j", "j", "j")
	if m.cursor != 3 {
		t.Fatalf("cursor below the last row: %d", m.cursor)
	}
}

func TestEnterOpensTheSandboxAndEscGoesBack(t *testing.T) {
	m := press(sampleApp(), "j", "enter")
	if m.screen != screenDetail || m.selected().Name != "api-refactor" {
		t.Fatalf("detail of %v on screen %v", m.selected(), m.screen)
	}
	if got := press(m, "esc").screen; got != screenHome {
		t.Fatalf("esc from the detail: screen %v", got)
	}
}

func TestFilterNarrowsTheListAndEscClearsIt(t *testing.T) {
	m := press(sampleApp(), "j", "/", "a", "p", "i")
	if got := names(m.visible()); got != "api-refactor" {
		t.Fatalf("filtered to %q", got)
	}
	if m.selectedID() != "sb2" {
		t.Fatalf("selection lost while filtering: %q", m.selectedID())
	}
	m = press(m, "enter")
	if m.filtering || m.filter != "api" {
		t.Fatalf("enter keeps the filter: filtering %v, filter %q", m.filtering, m.filter)
	}
	if got := len(press(m, "esc").visible()); got != 4 {
		t.Fatalf("esc clears the filter: %d sandboxes shown", got)
	}
}

func TestTeammateNamesFilterToo(t *testing.T) {
	m := press(sampleApp(), "/", "f", "i", "n", "n")
	if got := names(m.visible()); got != "api-refactor" {
		t.Fatalf("filter by teammate: %q", got)
	}
}

func TestDeleteAsksAndDeletesOnYes(t *testing.T) {
	m := press(sampleApp(), "d")
	if m.confirm == nil || m.confirm.Name != "blub" {
		t.Fatalf("delete does not ask about blub: %+v", m.confirm)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = next.(*app)
	if m.confirm != nil || cmd == nil || !strings.HasPrefix(m.notice.text, "Deleting blub") {
		t.Fatalf("yes: confirm %v, cmd %v, notice %q", m.confirm, cmd != nil, m.notice.text)
	}
}

func TestDeleteKeepsTheSandboxOnAnyOtherKey(t *testing.T) {
	m := press(sampleApp(), "d", "n")
	if m.confirm != nil || m.notice.text != "Kept blub" {
		t.Fatalf("no: confirm %v, notice %q", m.confirm, m.notice.text)
	}
}

func TestToggleFollowsThePhase(t *testing.T) {
	cases := []struct {
		name   string
		sb     int // the sandbox of sampleApp it is about
		status runtime.Status
		agent  bool
		notice string
		cmd    bool // whether the toggle starts a command
	}{
		{"running sandbox stops", 0, "running", true, "Stopping blub…", true},
		{"stopped sandbox starts", 2, "stopped", false, "Starting docs-site…", true},
		{"suspended sandbox resumes", 2, "suspended", false, "Resuming docs-site…", true},
		{"starting sandbox waits", 0, "running", false, "blub is still starting.", false},
		{"failed sandbox says so", 0, "failed", false, "blub could not be created. Delete it with d.", false},
	}
	for _, c := range cases {
		m := sampleApp()
		m.sandboxes[c.sb].Status = c.status
		if c.agent {
			m.sandboxes[c.sb].Agent = &sandboxes.AgentInfo{}
		} else {
			m.sandboxes[c.sb].Agent = nil
		}
		m.cursor = c.sb
		cmd := m.toggle(m.selected())
		if m.notice.text != c.notice || m.notice.failed != strings.Contains(c.notice, "could not") {
			t.Errorf("%s: notice %+v, want %q", c.name, m.notice, c.notice)
		}
		if (cmd != nil) != c.cmd {
			t.Errorf("%s: command %v, want %v", c.name, cmd != nil, c.cmd)
		}
	}
}

func TestBrowserHintsPrintTheURL(t *testing.T) {
	m := press(sampleApp(), "l", "r")
	if m.screen != screenActivity {
		t.Fatalf("l opens %v", m.screen)
	}
	if want := "open in browser: http://127.0.0.1:7878/e/env1/network/rules"; m.notice.text != want {
		t.Fatalf("rules hint %q", m.notice.text)
	}
}

func TestEnvironmentPickerSwitchesAndLoads(t *testing.T) {
	m := press(sampleApp(), "e", "j")
	if m.screen != screenEnvs || m.picker != 1 {
		t.Fatalf("picker: screen %v, cursor %d", m.screen, m.picker)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*app)
	if m.env.ID != "env2" || m.screen != screenHome || m.loaded || cmd == nil {
		t.Fatalf("switch: env %q, screen %v, loaded %v, cmd %v", m.env.ID, m.screen, m.loaded, cmd != nil)
	}
	if m.sandboxes != nil || len(m.visible()) != 0 {
		t.Fatal("the previous environment's sandboxes are still on show")
	}
}

func TestNewApprovalRingsTheBellOnce(t *testing.T) {
	m := newApp(context.Background(), nil, "127.0.0.1:7878")
	first := []api.ApprovalView{{Approval: store.Approval{ID: "a1"}}}
	if cmd := m.setApprovals(first); cmd != nil {
		t.Fatal("the first load rang the bell")
	}
	if cmd := m.setApprovals(first); cmd != nil {
		t.Fatal("a load without new approvals rang the bell")
	}
	cmd := m.setApprovals(append(first, api.ApprovalView{Approval: store.Approval{ID: "a2"}}))
	if cmd == nil {
		t.Fatal("a new approval did not ring the bell")
	}
	if msg, ok := cmd().(tea.RawMsg); !ok || msg.Msg != "\a" {
		t.Fatalf("bell message: %#v", cmd())
	}
}

func TestStreamLossReconnectsAndRefreshes(t *testing.T) {
	m := sampleApp()
	next, cmd := m.Update(streamEnded{})
	m = next.(*app)
	if !m.offline || cmd == nil {
		t.Fatalf("lost stream: offline %v, cmd %v", m.offline, cmd != nil)
	}
	if !strings.Contains(m.noticeLine(80), "Reconnecting") {
		t.Fatalf("notice while offline: %q", m.noticeLine(80))
	}
}

func TestSelectionFollowsTheSandboxAcrossRefreshes(t *testing.T) {
	m := press(sampleApp(), "j", "j")
	list := []sandboxes.View{m.sandboxes[3], m.sandboxes[2], m.sandboxes[1], m.sandboxes[0]}
	next, _ := m.Update(sandboxesLoaded{env: m.env.ID, list: list})
	m = next.(*app)
	if m.selectedID() != "sb3" {
		t.Fatalf("selection moved to %q", m.selectedID())
	}
}

func TestDetailGoesHomeWhenItsSandboxIsGone(t *testing.T) {
	m := press(sampleApp(), "enter")
	list := m.sandboxes[1:]
	next, _ := m.Update(sandboxesLoaded{env: m.env.ID, list: list})
	m = next.(*app)
	if m.screen != screenHome || !m.notice.failed {
		t.Fatalf("deleted sandbox: screen %v, notice %+v", m.screen, m.notice)
	}
}

func TestStaleLoadsOfAnotherEnvironmentAreIgnored(t *testing.T) {
	m := sampleApp()
	next, _ := m.Update(sandboxesLoaded{env: "other", list: nil})
	if got := len(next.(*app).sandboxes); got != 4 {
		t.Fatalf("a load for another environment replaced the list: %d", got)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, msg := range []tea.KeyPressMsg{{Code: 'q', Text: "q"}, {Code: 'c', Mod: tea.ModCtrl}} {
		_, cmd := sampleApp().Update(msg)
		if cmd == nil {
			t.Fatalf("%v does not quit", msg)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%v: command %T is not a quit", msg, cmd())
		}
	}
}

func TestPhaseOf(t *testing.T) {
	cases := map[string]phase{
		"creating": phaseCreating, "running": phaseBooting, "starting": phaseBooting, "created": phaseBooting,
		"suspended": phaseSuspended, "stopped": phaseStopped, "crashed": phaseFailed, "failed": phaseFailed,
	}
	for status, want := range cases {
		if got := phaseOf(sandboxes.View{Status: runtime.Status(status)}); got != want {
			t.Errorf("%s: phase %v, want %v", status, got, want)
		}
	}
	if got := phaseOf(sandboxes.View{Status: runtime.Status("running"), Agent: &sandboxes.AgentInfo{}}); got != phaseReady {
		t.Errorf("running with an agent: %v", got)
	}
}

func TestUptimeReadsLikeTheConcept(t *testing.T) {
	cases := map[time.Duration]string{
		0:                             "up <1m",
		12 * time.Minute:              "up 12m",
		2*time.Hour + 14*time.Minute:  "up 2h 14m",
		47 * time.Hour:                "up 47h 0m",
		3*24*time.Hour + 4*time.Hour:  "up 3d 4h",
		2*24*time.Hour + 23*time.Hour: "up 2d 23h",
	}
	for d, want := range cases {
		if got := uptime(d); got != want {
			t.Errorf("uptime(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFitPadsAndCuts(t *testing.T) {
	if got := fit("blub", 6); got != "blub  " {
		t.Errorf("pad: %q", got)
	}
	if got := fit("node-22+python-3.13", 8); got != "node-22…" {
		t.Errorf("cut: %q", got)
	}
	if got := fit("anything", 0); got != "" {
		t.Errorf("zero width: %q", got)
	}
}

// names joins the names of sandboxes, for comparisons.
func names(list []sandboxes.View) string {
	var out []string
	for _, sb := range list {
		out = append(out, sb.Name)
	}
	return strings.Join(out, ",")
}

// z suspends a running sandbox, and says why it doesn't for one that isn't.
func TestSuspendFollowsThePhase(t *testing.T) {
	m := sampleApp()
	if _, cmd := pressCmd(m, "z"); cmd == nil || m.notice.text != "Suspending blub…" {
		t.Fatalf("z on a running sandbox: %+v", m.notice)
	}
	m.cursor = 2 // docs-site, stopped
	if _, cmd := pressCmd(m, "z"); cmd != nil || m.notice.text != "Only a running sandbox can be suspended." {
		t.Fatalf("z on a stopped sandbox: %+v", m.notice)
	}
	m.sandboxes[2].Status = "suspended"
	if _, cmd := pressCmd(m, "z"); cmd != nil || m.notice.text != "docs-site is suspended already. s resumes it." {
		t.Fatalf("z on a suspended sandbox: %+v", m.notice)
	}
}
