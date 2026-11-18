package tui

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// paneApp is the visual sample with its sessions at 140 columns: the sidebar, and a thread beside
// its computer.
func paneApp() *app {
	m := withSessions(visualApp(140, 40))
	m.client = &Client{addr: "127.0.0.1:1"} // the tests run no command that would reach it
	return m
}

// shown is the frame on show, without its styles.
func shown(m *app) string {
	return ansi.Strip(m.View().Content)
}

// termRecorder is a fake terminal that writes its greeting, and records the keys it is sent and the
// paths it is attached at.
type termRecorder struct {
	mu     sync.Mutex
	keys   strings.Builder
	paths  []string
	closed chan struct{}
}

func (r *termRecorder) typed() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keys.String()
}

func (r *termRecorder) attached() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

// recordingTerm is a client whose terminals greet with hello and are recorded.
func recordingTerm(t *testing.T, hello string) (*Client, *termRecorder) {
	rec := &termRecorder{closed: make(chan struct{}, 8)}
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, r *http.Request) {
		defer func() { rec.closed <- struct{}{} }()
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		_ = c.Write(ctx, websocket.MessageBinary, []byte(hello))
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				rec.mu.Lock()
				rec.keys.Write(data)
				rec.mu.Unlock()
			}
		}
	})
	return client, rec
}

// connected connects the terminal on show, as the wait after paneDelay would, and waits for want
// on its screen.
func connected(t *testing.T, m *app, want string) *app {
	t.Helper()
	m = run(m, m.openPane())
	if m.pane.term == nil {
		t.Fatalf("the terminal is not connected: %v", m.pane.err)
	}
	waitFor(t, want, func() bool { return strings.Contains(shown(m), want) })
	return m
}

// A thread that is an agent in a terminal shows the terminal under its computer's chips. enter
// gives it the keys, ctrl+c too; alt+z puts it in full screen with a status line; ctrl+] gives
// the keys back; leaving the thread lets the terminal go.
func TestThreadTerminalTakesTheKeys(t *testing.T) {
	client, rec := recordingTerm(t, "claude is ready\r\n> ")
	m := paneApp()
	m.client = client
	m = openSession(t, m, "sb1", "claude")
	if x, ok := m.paneTarget(); !ok || x.term.Name != "claude" || !m.threadTermShows() {
		t.Fatalf("the thread's terminal: %+v", x)
	}
	m = connected(t, m, "claude is ready")
	out := shown(m)
	for _, want := range []string{"Iris › claude", "main  claude  + new shell", "● blub"} {
		if !strings.Contains(out, want) {
			t.Errorf("the thread lacks %q:\n%s", want, out)
		}
	}

	m = press(m, "enter")
	if !m.paneFocused() || m.View().Cursor == nil {
		t.Fatalf("enter did not give the terminal the keys: focus %v", m.pane.focus)
	}
	if got := ansi.Strip(m.keyBar(140, 0)); !strings.Contains(got, "ctrl+] keys back") || !strings.Contains(got, "alt+z full screen") {
		t.Errorf("key bar while typing: %q", got)
	}
	if !strings.Contains(shown(m), "━━━") {
		t.Error("the rule is not the accent's heavy one while the terminal has the keys")
	}
	for _, k := range []string{"l", "s", "enter", "q"} {
		m = press(m, k)
	}
	if _, cmd := m.key(keyMsg("ctrl+c")); cmd != nil {
		t.Error("ctrl+c in the terminal must reach the agent, not quit")
	}
	waitFor(t, "the keys", func() bool { return rec.typed() == "ls\rq\x03" })
	if m.screen != screenMain || m.here.kind != placeThread {
		t.Fatalf("q in the terminal left the thread: %v %+v", m.screen, m.here)
	}

	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt} })
	out = shown(m)
	if !m.zoom || !m.shellZoomed() || strings.Contains(out, "Iris › claude") || !strings.Contains(out, "blub › claude") || !strings.Contains(out, "ctrl+] keys back · alt+z") {
		t.Errorf("alt+z: zoom %v\n%s", m.zoom, out)
	}
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl} })
	if m.paneFocused() || m.View().Cursor != nil {
		t.Fatal("ctrl+] did not give the keys back")
	}
	if !strings.Contains(shown(m), "Iris › claude") {
		t.Error("with the keys back, the zoomed thread shows the crumb row again")
	}
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt} }) // back from full screen
	if m.zoom {
		t.Error("alt+z with Studio's keys leaves full screen")
	}
	m = openComputer(m, "sb4", tabTerminal) // demo, which has no terminal yet
	select {
	case <-rec.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal was not closed when the thread was left")
	}
	if m.pane.term != nil || m.zoom {
		t.Errorf("the pane kept the terminal or the full screen: %+v, zoom %v", m.pane, m.zoom)
	}
}

// The Terminal tab is the computer's terminals as chips over the one on show: alt+2 shows the
// second and keeps the keys in it, and + attaches a new shell by the next free name.
func TestTerminalTabSwitchesAndOpensShells(t *testing.T) {
	client, rec := recordingTerm(t, "$ ")
	m := paneApp()
	m.client = client
	m = openComputer(m, "sb1", tabTerminal)
	out := shown(m)
	for _, want := range []string{"Terminal", "Details", "main  claude  + new shell", "Iris › desk"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Terminal tab lacks %q:\n%s", want, out)
		}
	}
	m = run(m, m.focusPane())
	if !m.paneFocused() || m.pane.key != "term/sb1/main" {
		t.Fatalf("enter types into main: focus %v, key %q", m.pane.focus, m.pane.key)
	}
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt} })
	if x, ok := m.paneTarget(); !ok || x.term.Name != "claude" || !m.pane.focus || m.comp.term != "claude" {
		t.Fatalf("alt+2 goes to claude and keeps the keys: %+v focus %v", x, m.pane.focus)
	}
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl} })
	m, cmd := pressCmd(m, "+")
	if m.comp.term != "2" || !m.pane.focus {
		t.Fatalf("+ opens shell 2 with the keys: term %q, focus %v", m.comp.term, m.pane.focus)
	}
	run(m, cmd)
	waitFor(t, "the new shell's attach", func() bool {
		for _, p := range rec.attached() {
			if strings.Contains(p, "/terminals/2/") {
				return true
			}
		}
		return false
	})
}

// At 80 columns the computer fills the column: its tab row, the rule, the chips and the
// terminal.
func TestTerminalTabAtEightyColumns(t *testing.T) {
	client, _ := recordingTerm(t, "codex is ready\r\n> ")
	m := withSessions(visualApp(80, 24))
	m.client = client
	m.approvals = nil // no banner over the terminal's first rows
	m = openComputer(m, "sb2", tabTerminal)
	m.comp.term = "codex"
	m = connected(t, settle(m), "codex is ready")
	lines := strings.Split(shown(m), "\n")
	if len(lines) != 24 || !strings.HasPrefix(lines[0], " ≡  Finn › desk") {
		t.Fatalf("the frame:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "Terminal") || !strings.Contains(lines[4], "main  codex") {
		t.Errorf("the tab row and the chips:\n%s", strings.Join(lines[:6], "\n"))
	}
	if got := m.termRect(); got.w != 80 || got.h != 16 {
		t.Errorf("the terminal is %d×%d", got.w, got.h)
	}
}

// The banner over an agent's terminal decides the request its computer waits on.
func TestBannerDecidesTheAgentsApproval(t *testing.T) {
	m := paneApp()
	m.approvals[0].Network = &policy.NetworkRequest{Host: "proxy.golang.org", Port: 443, SandboxName: "api-refactor"}
	m = openSession(t, m, "sb2", "codex")
	if x, _ := m.paneTarget(); x.term.Name != "codex" || m.stateOf(x) != stateBlocked {
		t.Fatalf("on %+v", x)
	}
	out := shown(m)
	if !strings.Contains(out, "api-refactor wants proxy.golang.org:443") || !strings.Contains(out, "Allow once") || !strings.Contains(out, "More…") {
		t.Fatalf("no banner over the blocked agent:\n%s", out)
	}
	for _, k := range []string{"y", "o", "n"} {
		if cmd, ok := m.bannerKey(keyMsg(k)); !ok || cmd == nil {
			t.Errorf("%s does not decide", k)
		}
	}
	if m = press(m, "n"); m.screen != screenMain || m.here.kind != placeThread {
		t.Errorf("n opened %v instead of denying", m.screen)
	}
}

// Home's reading pane shows the thread under the cursor as it streams; enter opens the thread,
// which keeps the chat open.
func TestReadingPaneShowsTheChat(t *testing.T) {
	m := paneApp()
	m.goHome()
	for _, r := range m.inboxRows() {
		if r.thread != nil && r.thread.kind == sessionChat && r.thread.chat.ID == "c1" {
			m.inbox.cursor = r.id
		}
	}
	m = settle(m)
	m.openPane()
	if !m.chats.reading || m.chats.id != "c1" || m.here.kind != placeHome {
		t.Fatalf("the reading pane opens the chat: %+v, here %+v", m.chats, m.here)
	}
	answer := item("i1", 1, chat.KindAgent)
	answer.Text = &chat.TextBody{Text: "The form submits on enter now."}
	log := chat.Log{
		Chat:  store.Chat{ID: "c1", SandboxID: "sb1", Title: "Fix the login form", State: store.ChatIdle},
		Turns: []chat.Turn{{ChatTurn: store.ChatTurn{ID: "t1", ChatID: "c1", Seq: 1}}},
		Items: []chat.Item{answer},
	}
	next, _ := m.Update(chatLoaded{gen: m.chats.gen, log: log})
	m = next.(*app)
	if !strings.Contains(shown(m), "The form submits on enter now.") {
		t.Fatalf("the transcript is not in the reading pane:\n%s", shown(m))
	}
	m = press(m, "enter")
	if m.here.kind != placeThread || m.here.chat != "c1" || m.chats.reading || m.chats.id != "c1" || !m.chats.loaded {
		t.Fatalf("enter opens the thread and keeps its chat: here %+v, %+v", m.here, m.chats)
	}
	if !strings.Contains(shown(m), "The form submits on enter now.") || !strings.Contains(shown(m), "Iris › Fix the login form") {
		t.Errorf("the thread:\n%s", shown(m))
	}
}

// The Details tab charts the computer from 20 rows in the visual look.
func TestDashboardChartsTheSandbox(t *testing.T) {
	m := openComputer(paneApp(), "sb2", tabDetails)
	if !m.dashShows() {
		t.Fatal("the dashboard does not show on the Details tab")
	}
	out := shown(m)
	for _, want := range []string{"CPU  38 % of 2 CPUs", "avg 42 % · peak 85 %", "100┤", "2 min ago", "MEMORY  1.4 / 2 GiB", "70 %", "HISTORY"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dashboard lacks %q:\n%s", want, out)
		}
	}
}

func TestAreaChartFillsInEighths(t *testing.T) {
	style := styleDim
	got := areaChart([]float64{0, 50, 100}, 4, 1, 100, 2, style, style)
	want := []string{"   █", "·▁██"}
	for i := range want {
		if ansi.Strip(got[i]) != want[i] {
			t.Errorf("row %d: %q, want %q", i, ansi.Strip(got[i]), want[i])
		}
	}
	if got := ansi.Strip(areaChart([]float64{25}, 1, 3, 100, 1, style, style)[0]); got != "▂▂▂" {
		t.Errorf("a slot three columns wide: %q", got)
	}
	if slots, per := chartSize(130); slots != usageKeep || per != 2 {
		t.Errorf("chartSize(130) = %d, %d", slots, per)
	}
}

func TestPaneLeavesAFinishedSessionUnmarkedWhileItShows(t *testing.T) {
	m := openSession(t, paneApp(), "sb2", "codex") // which works
	m.sess.terms["sb2"][1].State = sandboxes.TerminalIdle
	m.sessionsRead(sessionsLoaded{at: m.now, terms: m.sess.terms, chats: m.sess.chats})
	if m.sess.done["term/sb2/codex"] || strings.Contains(m.notice.text, "codex") {
		t.Errorf("the session on show was marked done: %v, %q", m.sess.done, m.notice.text)
	}
}

func must(x session, _ bool) session { return x }

func TestCursorFollowsItsSessionAcrossReads(t *testing.T) {
	m := onSession(t, paneApp(), "sb1", "Fix the login form")
	terms := map[string][]sandboxes.Terminal{
		"sb1": {termOf(agentproto.Session{Name: "main"}, ""), termOf(agentproto.Session{Name: "gemini", Harness: "gemini"}, ""), termOf(agentproto.Session{Name: "claude", Harness: "claude"}, "")},
	}
	m.sessionsRead(sessionsLoaded{at: m.now, terms: terms, chats: m.sess.chats})
	if x, ok := m.selectedSession(); !ok || x.title() != "Fix the login form" {
		t.Fatalf("a new agent above moved the cursor to %+v", x)
	}
}
