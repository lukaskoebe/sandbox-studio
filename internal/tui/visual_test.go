package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// goldenVisual checks a screen in the visual look against testdata/<name>.golden. With escapes
// the file keeps the 256-color ANSI the screen draws; without, it is the text, for the layouts
// of wide terminals. The look, the clock and the spinner are fixed, so the files are stable.
func goldenVisual(t *testing.T, name string, m *app, escapes bool) {
	t.Helper()
	view := m.View().Content
	if !escapes {
		view = ansi.Strip(view)
	}
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(view), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if view != string(want) {
		t.Errorf("%s does not match %s; got:\n%s", name, path, ansi.Strip(view))
	}
}

// visualApp is the sample app in the visual look on a 256-color terminal of w×h with a dark
// background, with a few samples of each running sandbox's usage.
func visualApp(w, h int) *app {
	m := sampleApp()
	m.setLook(lookFor(colorprofile.ANSI256, false, false))
	m.width, m.height = w, h
	for i, cpu := range []float64{4, 8, 30, 62, 85, 60, 35, 20, 38, 55, 71, 66, 40, 22, 38} {
		m.usage.record(map[string]sandboxes.ResourceUse{
			"sb1": {CPUPercent: float64(i % 3), CPUs: 2, MemoryBytes: 600 << 20, MemoryTotalBytes: 2 << 30},
			"sb2": {CPUPercent: cpu, CPUs: 2, MemoryBytes: 1503238553, MemoryTotalBytes: 2 << 30},
		})
	}
	return m
}

// withNetwork gives api-refactor's network column a few connections.
func withNetwork(m *app) {
	at := func(d time.Duration) time.Time { return m.now.Add(-d) }
	m.net = netState{sandbox: "sb2", loaded: true, busy: true, conns: []gateway.Conn{
		{ID: 1, Started: at(40 * time.Minute), Host: "github.com", Port: 443, Verdict: gateway.VerdictAllowed},
		{ID: 2, Started: at(12 * time.Minute), Host: "registry.npmjs.org", Port: 443, Verdict: gateway.VerdictAllowed},
		{ID: 3, Started: at(9 * time.Minute), Host: "telemetry.example.com", Port: 443, Verdict: gateway.VerdictDenied},
		{ID: 4, Started: at(2 * time.Minute), Host: "pypi.org", Port: 443, Verdict: gateway.VerdictPending},
		{ID: 5, Started: at(20 * time.Second), Host: "files.pythonhosted.org", Port: 443, Verdict: gateway.VerdictFailed},
	}}
}

// withDetailData gives the detail pane of api-refactor its ports, terminals and history.
func withDetailData(m *app) *app {
	m.ports = portsState{sandbox: "sb2", loaded: true, previews: true,
		list:      []api.PreviewPort{{Port: 3000, URL: "http://3000-sb2.localhost:7878/"}, {Port: 8080, URL: "http://8080-sb2.localhost:7878/"}},
		terminals: []agentproto.Session{{Name: "main"}, {Name: "claude"}},
	}
	at := func(d time.Duration) time.Time { return m.now.Add(-d) }
	m.lineage = lineageState{sandbox: "sb2", loaded: true, data: sandboxes.Lineage{
		SandboxID: "sb2",
		Events: []sandboxes.LineageEvent{
			{SandboxEvent: store.SandboxEvent{Kind: store.EventCreate, CreatedAt: at(72 * time.Hour)}},
			{SandboxEvent: store.SandboxEvent{Kind: store.EventCheckpoint, CheckpointID: "k1", CreatedAt: at(48 * time.Hour)}},
			{SandboxEvent: store.SandboxEvent{Kind: store.EventCheckpoint, CheckpointID: "k2", CreatedAt: at(24 * time.Hour)}},
			{SandboxEvent: store.SandboxEvent{Kind: store.EventRestore, CreatedAt: at(3 * time.Hour)}},
			{SandboxEvent: store.SandboxEvent{Kind: store.EventFork, CreatedAt: at(time.Hour)}},
		},
		Checkpoints: []sandboxes.LineageCheckpoint{{ID: "k1", Auto: true}, {ID: "k2"}},
	}}
	return m
}

func TestGoldenVisualHome(t *testing.T) {
	goldenVisual(t, "visual_home", visualApp(80, 24), true)
}

func TestGoldenVisualInbox(t *testing.T) {
	m := sampleMorning(visualApp(80, 24))
	goldenVisual(t, "visual_inbox", onCard(m, 2), true)
}

func TestGoldenVisualInboxKinds(t *testing.T) {
	m := visualApp(80, 24)
	m.approvals = kindApprovals(m.now)
	goldenVisual(t, "visual_inbox_kinds", onCard(m, 0), true)
}

func TestGoldenVisualChat(t *testing.T) {
	goldenVisual(t, "visual_chat", sampleChat(visualApp(80, 24)), true)
}

func TestGoldenVisualActivity(t *testing.T) {
	goldenVisual(t, "visual_activity", sampleActivity(visualApp(80, 24)), true)
}

func TestGoldenVisualComputer(t *testing.T) {
	goldenVisual(t, "visual_computer", withDetailData(openComputer(visualApp(80, 24), "sb2", tabDetails)), true)
}

func TestGoldenVisualThread(t *testing.T) {
	goldenVisual(t, "visual_thread", threadWithCard(visualApp(80, 24)), true)
}

// A thread with its computer's Details beside it, at 140 and at 120 columns (A4).
func TestGoldenWideThread(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {120, 30}} {
		m := threadWithCard(visualApp(size[0], size[1]))
		m.comp.tab = tabDetails // A4 shows Changes, which phase 3b adds
		detailDataOf(m, "sb1")
		goldenVisual(t, fmt.Sprintf("wide_thread_%dx%d", size[0], size[1]), m, false)
	}
}

func TestGoldenVisualPager(t *testing.T) {
	m := visualApp(80, 24)
	samplePush(m)
	goldenVisual(t, "visual_pager", press(onCard(m, 0), "enter"), true)
}

func TestGoldenWideHome(t *testing.T) {
	m := withDetailData(press(visualApp(140, 40), "j"))
	sampleApprovals(m)
	goldenVisual(t, "wide_home_140x40", m, false)
}

func TestGoldenWideHome180(t *testing.T) {
	m := withDetailData(press(visualApp(180, 50), "j"))
	withNetwork(m)
	goldenVisual(t, "wide_home_180x50", m, false)
}

func TestGoldenWidePager(t *testing.T) {
	m := visualApp(140, 40)
	samplePush(m)
	goldenVisual(t, "wide_pager_140x40", press(onCard(m, 0), "enter"), false)
}

func TestGoldenWidePagerSideBySide(t *testing.T) {
	m := visualApp(180, 50)
	samplePush(m)
	goldenVisual(t, "wide_pager_180x50", press(onCard(m, 0), "enter"), false)
}

func TestGoldenWideInbox(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {180, 50}, {100, 30}} {
		m := visualApp(size[0], size[1])
		sampleApprovals(m)
		m.approvals = append(m.approvals, kindApprovals(m.now)...)
		goldenVisual(t, fmt.Sprintf("wide_inbox_%dx%d", size[0], size[1]), onCard(m, 0), false)
	}
}

func TestGoldenWideChatRunning(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.chat.State = store.ChatRunning
	m.chats.turns["t2"] = chat.Turn{ChatTurn: store.ChatTurn{ID: "t2", ChatID: "c1", Seq: 2}}
	prompt := item("i6", 6, chat.KindUser)
	prompt.TurnID, prompt.User = "t2", &chat.UserBody{Text: "Add a test for it."}
	plan := item("i7", 7, chat.KindPlan)
	plan.TurnID, plan.Plan = "t2", &chat.PlanBody{Entries: []acp.PlanEntry{
		{Content: "Write the test", Status: "completed"},
		{Content: "Run it", Status: "in_progress"},
		{Content: "Push the branch", Status: "pending"},
	}}
	run := item("i8", 8, chat.KindTool)
	run.TurnID, run.Tool = "t2", &chat.ToolBody{Title: "npm test -- login", Kind: "execute", Status: "in_progress"}
	m.chats.items["i6"], m.chats.items["i7"], m.chats.items["i8"] = prompt, plan, run
	m.chats.sorted = nil // sorted again, as the stream would
	goldenVisual(t, "wide_chat_140x40", m, false)
}

func TestGoldenWideActivity(t *testing.T) {
	goldenVisual(t, "wide_activity_140x40", sampleActivity(visualApp(140, 40)), false)
}

// TestPlainLookIsToday keeps STUDIO_TUI=plain and a terminal without colors on today's look.
func TestPlainLookIsToday(t *testing.T) {
	if l := lookFor(colorprofile.TrueColor, true, false); !l.plain {
		t.Error("STUDIO_TUI=plain on a truecolor terminal: not plain")
	}
	if l := lookFor(colorprofile.ASCII, false, false); !l.plain {
		t.Error("NO_COLOR: not plain")
	}
	if l := lookFor(colorprofile.ANSI, false, false); l.plain {
		t.Error("16 colors: plain")
	}
	if !plainSetting(" Plain ") || plainSetting("") {
		t.Error("plainSetting")
	}
	m := sampleApp()
	if v := m.View(); v.MouseMode != tea.MouseModeNone {
		t.Error("the plain look takes the mouse")
	}
	if v := visualApp(80, 24).View(); v.MouseMode != tea.MouseModeCellMotion {
		t.Error("the visual look does not take the mouse by cell motion")
	}
}

// TestColorsFollowTheProfile resolves an area's hue on each profile: the ANSI color on 16
// colors, the xterm color on 256, and the web's dark or light value on truecolor.
func TestColorsFollowTheProfile(t *testing.T) {
	for _, tc := range []struct {
		profile colorprofile.Profile
		light   bool
		want    string
	}{
		{colorprofile.ANSI, false, "\x1b[34m"},
		{colorprofile.ANSI256, false, "\x1b[38;5;74m"},
		{colorprofile.TrueColor, false, "\x1b[38;2;110;185;220m"},
		{colorprofile.TrueColor, true, "\x1b[38;2;58;137;179m"},
	} {
		p := newPalette(lookFor(tc.profile, false, tc.light))
		if got := p.hue(areaNetwork).Render("x"); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%v light %v: network is %q, want %q", tc.profile, tc.light, got, tc.want)
		}
	}
}

func TestSparklineAndBar(t *testing.T) {
	if got := sparkline([]float64{0, 50, 100}, 100, 5); got != "  ▁▅█" {
		t.Errorf("sparkline: %q", got)
	}
	if got := sparkline([]float64{1, 2, 3, 4}, 4, 2); got != "▆█" {
		t.Errorf("sparkline keeps the last values: %q", got)
	}
	if got := ansi.Strip(bar(0.5, 4, styleDim)); got != "██░░" {
		t.Errorf("bar: %q", got)
	}
}

// TestGroupedKeyBar puts a dim │ between the key groups and calls help "more".
func TestGroupedKeyBar(t *testing.T) {
	m := visualApp(100, 24)
	m.approvals = nil // Home with nothing waiting
	got := ansi.Strip(m.keyBar(100, 0))
	if got != "enter open  tab next  + new computer │ l activity  , settings  e env  \\ sidebar │ ? more  q quit" {
		t.Errorf("key bar: %q", got)
	}
	if got := ansi.Strip(m.keyBar(40, 0)); !strings.HasSuffix(got, " …") || len([]rune(got)) > 40 {
		t.Errorf("a narrow key bar is cut: %q", got)
	}
}

// A click on a row of the sidebar selects it and gives the sidebar the cursor; a double click
// opens its place.
func TestMouseSelectsAndOpens(t *testing.T) {
	m := visualApp(120, 30)
	m.View() // records where the rows lead
	var demo rowHit
	for _, r := range m.mouse.rows {
		if rows := m.homeRows(); r.target == rowTree && rows[r.index].kind == atSandbox && m.visible()[rows[r.index].sb].ID == "sb4" {
			demo = r
		}
	}
	if demo.target == 0 {
		t.Fatal("demo's row of the sidebar records no click")
	}
	click := tea.MouseClickMsg{X: demo.x0 + 2, Y: demo.y, Button: tea.MouseLeft}
	next, _ := m.Update(click)
	m = next.(*app)
	if m.selectedID() != "sb4" || m.focus != focusSidebar || m.here.kind != placeHome {
		t.Fatalf("a click selects the row: %s, focus %v, here %+v", m.selectedID(), m.focus, m.here)
	}
	m.View()
	next, _ = m.Update(click)
	m = next.(*app)
	if m.here.kind != placeComputer || m.here.sandbox != "sb4" {
		t.Errorf("a double click opens it: here %+v", m.here)
	}
}

// The wheel over Home's list moves its cursor; the plain look leaves the mouse alone.
func TestMouseWheelMovesTheList(t *testing.T) {
	m := visualApp(80, 24)
	rows := m.inboxRows()
	next, _ := m.Update(tea.MouseWheelMsg{X: 5, Y: bodyTop + 1, Button: tea.MouseWheelDown})
	m = next.(*app)
	if m.inbox.cursor != rows[1].id {
		t.Errorf("the wheel moves the cursor: %q, want %q", m.inbox.cursor, rows[1].id)
	}
	m = sampleApp()
	next, _ = m.Update(tea.MouseWheelMsg{X: 5, Y: bodyTop + 1, Button: tea.MouseWheelDown})
	if next.(*app).inbox.cursor != "" {
		t.Error("the plain look took the wheel")
	}
}

func TestMouseWheelScrollsThePager(t *testing.T) {
	m := visualApp(80, 24)
	samplePush(m)
	m = press(onCard(m, 0), "enter")
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if got := next.(*app).pager.scroll; got != wheelLines {
		t.Errorf("the wheel scrolls the review: %d", got)
	}
}

// The crumb row's parts act on a click: ≡ opens the drawer, the count of what waits goes Home,
// and the sidebar's environment opens the picker.
func TestClickingTheCrumbRow(t *testing.T) {
	at := func(m *app, part crumbPart) crumbSpan {
		t.Helper()
		m.View()
		for _, s := range m.mouse.crumb {
			if s.part == part {
				return s
			}
		}
		t.Fatalf("the crumb row has no part %v: %+v", part, m.mouse.crumb)
		return crumbSpan{}
	}
	m := visualApp(80, 24)
	next, _ := m.Update(tea.MouseClickMsg{X: at(m, crumbMenu).x0, Y: 0, Button: tea.MouseLeft})
	if m = next.(*app); !m.drawer || !m.sidebarKeys() {
		t.Fatalf("≡ opens the drawer: drawer %v", m.drawer)
	}

	m = visualApp(120, 30)
	sampleApprovals(m)
	m = sampleChat(m)
	next, _ = m.Update(tea.MouseClickMsg{X: at(m, crumbWaiting).x0, Y: 0, Button: tea.MouseLeft})
	if m = next.(*app); m.here.kind != placeHome {
		t.Fatalf("what waits goes Home: here %+v", m.here)
	}
	next, _ = m.Update(tea.MouseClickMsg{X: at(m, crumbEnv).x0, Y: 0, Button: tea.MouseLeft})
	if m = next.(*app); m.screen != screenEnvs {
		t.Errorf("the environment opens the picker: screen %v", m.screen)
	}
}

func TestBracketsJumpBetweenFiles(t *testing.T) {
	m := visualApp(140, 40)
	samplePush(m)
	m = press(onCard(m, 0), "enter")
	files := m.pagerFiles()
	if len(files) != 2 || files[0].path != "src/login.tsx" || files[0].adds != 1 || files[0].dels != 1 || files[1].adds != 6 {
		t.Fatalf("files: %+v", files)
	}
	m = press(m, "]")
	if m.pager.scroll != files[0].line {
		t.Errorf("] goes to the first file: %d, want %d", m.pager.scroll, files[0].line)
	}
	m = press(m, "]")
	if m.pager.scroll != files[1].line {
		t.Errorf("] goes to the next file: %d, want %d", m.pager.scroll, files[1].line)
	}
	m = press(m, "[")
	if m.pager.scroll != files[0].line {
		t.Errorf("[ goes back: %d", m.pager.scroll)
	}
}

func TestVTogglesSideBySide(t *testing.T) {
	m := visualApp(180, 50)
	samplePush(m)
	m = press(onCard(m, 0), "enter")
	if !m.sideBySide() {
		t.Fatal("180 columns: not side by side")
	}
	if m = press(m, "v"); m.sideBySide() {
		t.Error("v did not turn side by side off")
	}
}

// TestUsageIsReadOnlyWhileHomeShows reads the running sandboxes' usage on Home and the detail,
// and stops elsewhere.
func TestUsageIsReadOnlyWhileHomeShows(t *testing.T) {
	m := visualApp(120, 30)
	if got := m.usageWanted(); len(got) != 2 || got[0] != "sb1" || got[1] != "sb2" {
		t.Errorf("Home reads the running sandboxes: %v", got)
	}
	m = press(m, "l")
	if got := m.usageWanted(); len(got) != 0 {
		t.Errorf("Activity reads usage: %v", got)
	}
	m = sampleApp()
	if got := m.usageWanted(); len(got) != 0 {
		t.Errorf("the plain look reads usage: %v", got)
	}
	_, ok := m.liveUpdate(usageLoaded{unsupported: true})
	if !ok || !m.usage.unsupported {
		t.Error("a runtime without usage stops the reads")
	}
}

// TestClickingACardSelectsIt selects the request whose row a click lands on.
func TestClickingACardSelectsIt(t *testing.T) {
	m := visualApp(80, 24)
	sampleApprovals(m)
	m = onCard(m, 0)
	view := ansi.Strip(m.View().Content)
	y := -1
	for i, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "registry.npmjs.org") {
			y = i
			break
		}
	}
	if y < 0 {
		t.Fatalf("no card of registry.npmjs.org:\n%s", view)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: 20, Y: y, Button: tea.MouseLeft})
	if got, want := next.(*app).inbox.cursor, "card/"+m.cards()[2].first().ID; got != want {
		t.Errorf("the click selected %q, want %q", got, want)
	}
}

func TestGoldenVisualPagerWords(t *testing.T) {
	m := visualApp(80, 24)
	samplePush(m)
	goldenVisual(t, "visual_pager_words", press(onCard(m, 0), "enter", "G"), true)
}

func TestClickingAKeycapPressesIt(t *testing.T) {
	m := visualApp(120, 30)
	sampleApprovals(m)
	m = onCard(m, 0)
	// at finds the key's place on the screen, and checks the screen shows the key there.
	at := func(k, label string) keyHit {
		t.Helper()
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		for _, h := range m.mouse.keys {
			if h.key == k {
				if cells := ansi.Cut(lines[h.y], h.x0, h.x1); !strings.Contains(cells, label) {
					t.Fatalf("the key %s covers %q, not %q", k, cells, label)
				}
				return h
			}
		}
		t.Fatalf("no key %s among %+v", k, m.mouse.keys)
		return keyHit{}
	}
	h := at("enter", "review")
	next, _ := m.Update(tea.MouseClickMsg{X: h.x0 + 1, Y: h.y, Button: tea.MouseLeft})
	m = next.(*app)
	if m.screen != screenPager {
		t.Fatalf("the git card's keycap opens the review: screen %v", m.screen)
	}
	h = at("esc", "back")
	if h.y != 28 {
		t.Errorf("the key bar's esc is on row %d, want 28", h.y)
	}
	next, _ = m.Update(tea.MouseClickMsg{X: h.x1 - 1, Y: h.y, Button: tea.MouseLeft})
	if m = next.(*app); m.screen != screenMain {
		t.Errorf("the key bar's esc goes back: screen %v", m.screen)
	}
}

func TestNetworkColumnReadsTheSelectedSandbox(t *testing.T) {
	m := onComputersScreen(t, visualApp(180, 50), "sb2")
	if got := m.netWanted(); got != "sb2" {
		t.Fatalf("the Computers screen's dashboard reads the selected computer's connections: %q", got)
	}
	if visualApp(99, 50).netWanted() != "" || press(visualApp(180, 50), "l").netWanted() != "" {
		t.Error("the network is read without the pane, or off Home")
	}
	if cmd := m.watchNet(); cmd != nil || m.net.sandbox != "sb2" || !m.net.busy {
		// press already started the read through Update; a second watch starts nothing
		t.Fatalf("a second read of the same sandbox: %v, %+v", cmd != nil, m.net)
	}
	gen := m.net.gen
	m.netUpdate(netLoaded{gen: gen - 1, conns: []gateway.Conn{{Host: "old.example"}}})
	if m.net.loaded {
		t.Error("a read for the sandbox before took effect")
	}
	if cmd, _ := m.netUpdate(netLoaded{gen: gen, conns: []gateway.Conn{{Host: "pypi.org", Port: 443}}}); cmd == nil || !m.net.loaded {
		t.Fatal("a read shows no connections, or schedules no next read")
	}
	m = press(m, "j") // another sandbox: read at once, a new generation
	if m.net.sandbox == "sb2" || m.net.gen == gen || m.net.loaded {
		t.Errorf("moving the cursor keeps the old sandbox's column: %+v", m.net)
	}
	m.netUpdate(netTick{gen: gen})
	if !m.net.busy {
		t.Error("a tick for the sandbox before ended the new one's read")
	}
}
