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
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// paneApp is the visual sample with its sessions at 140 columns, Home split into the sidebar and
// the pane.
func paneApp() *app {
	m := withSessions(visualApp(140, 40))
	m.client = &Client{addr: "127.0.0.1:1"} // the tests run no command that would reach it
	return m
}

// shown is the frame on show, without its styles.
func shown(m *app) string {
	return ansi.Strip(m.View().Content)
}

func TestPaneShowsTheAgentAndTakesTheKeys(t *testing.T) {
	var mu sync.Mutex
	var keys strings.Builder
	closed := make(chan struct{})
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, r *http.Request) {
		defer close(closed)
		_ = c.Write(ctx, websocket.MessageBinary, []byte("claude is ready\r\n> "))
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				mu.Lock()
				keys.Write(data)
				mu.Unlock()
			}
		}
	})
	m := paneApp()
	m.client = client
	m = press(m, "j") // blub's claude
	if x, ok := m.paneTarget(); !ok || x.term.Name != "claude" {
		t.Fatalf("the pane's session: %+v", x)
	}
	m = run(m, m.openPane()) // what the wait would do
	if m.pane.term == nil {
		t.Fatalf("the terminal is not connected: %v", m.pane.err)
	}
	waitFor(t, "the agent's screen", func() bool { return strings.Contains(shown(m), "claude is ready") })
	if !strings.Contains(shown(m), "claude · blub") {
		t.Errorf("the pane's title is missing:\n%s", shown(m))
	}

	// Enter gives the pane the keys; they go to the agent, ctrl+c too, and the TUI stays.
	m = press(m, "enter")
	if !m.paneFocused() || m.View().Cursor == nil {
		t.Fatalf("enter did not give the pane the keys: focus %v", m.pane.focus)
	}
	if got := ansi.Strip(m.keyBar(140, 0)); !strings.Contains(got, "ctrl+] sidebar") || !strings.Contains(got, "alt+z full screen") {
		t.Errorf("key bar while typing: %q", got)
	}
	for _, k := range []string{"l", "s", "enter", "q"} {
		m = press(m, k)
	}
	if _, cmd := m.key(keyMsg("ctrl+c")); cmd != nil {
		t.Error("ctrl+c in the pane must reach the agent, not quit")
	}
	waitFor(t, "the keys", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return keys.String() == "ls\rq\x03"
	})
	if m.screen != screenHome {
		t.Fatalf("q in the pane left Home: %v", m.screen)
	}

	// alt+z puts the pane in full screen; Ctrl-] gives the keys back to the sidebar.
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt} })
	if !m.pane.zoom || strings.Contains(shown(m), "blub · Iris") {
		t.Errorf("alt+z: zoom %v", m.pane.zoom)
	}
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl} })
	if m.paneFocused() || m.View().Cursor != nil {
		t.Fatal("Ctrl-] did not give the keys back")
	}
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt} }) // back from full screen
	m = press(m, "j") // the chat: the terminal is let go
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal was not closed when the cursor moved on")
	}
	if m.pane.term != nil || m.pane.zoom {
		t.Errorf("the pane kept the terminal or the full screen: %+v", m.pane)
	}
}

func TestPaneSwitchesWithAltNumbers(t *testing.T) {
	m := paneApp()
	m = press(m, "j")
	m.pane.focus = true
	m = run(m, func() tea.Msg { return tea.KeyPressMsg{Code: '4', Mod: tea.ModAlt} })
	x, ok := m.paneTarget()
	if !ok || x.term.Name != "codex" || !m.paneFocused() {
		t.Fatalf("alt+4 goes to codex and keeps the keys in the pane: %+v focus %v", x, m.pane.focus)
	}
}

func TestNumbersFocusThePane(t *testing.T) {
	m := press(paneApp(), "2")
	x, ok := m.paneTarget()
	if !ok || x.kind != sessionChat || !m.paneFocused() || m.screen != screenHome {
		t.Fatalf("2 shows the chat in the pane with the keys: %+v focus %v screen %v", x, m.pane.focus, m.screen)
	}
	if !m.chats.inPane || !m.chats.composing {
		t.Errorf("an idle chat opens its composer: inPane %v composing %v", m.chats.inPane, m.chats.composing)
	}
}

func TestPaneShowsTheChat(t *testing.T) {
	m := press(paneApp(), "j", "j") // blub's chat
	m.openPane()
	if !m.chats.inPane || m.chats.id != "c1" || m.screen != screenHome {
		t.Fatalf("the chat opens in the pane: %+v, screen %v", m.chats, m.screen)
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
	if !strings.Contains(shown(m), "The form submits on enter now.") || !strings.Contains(shown(m), "Fix the login form · blub") {
		t.Fatalf("the transcript is not in the pane:\n%s", shown(m))
	}

	// Enter writes; esc puts the prompt aside, a second esc gives the keys back.
	m = press(m, "enter", "h", "i")
	if !m.paneFocused() || !m.chats.composing || !strings.Contains(m.noticeLine(140), "hi") {
		t.Fatalf("enter writes a prompt in the pane: focus %v composing %v", m.pane.focus, m.chats.composing)
	}
	m = press(m, "esc")
	if !m.paneFocused() || m.chats.composing {
		t.Fatal("esc puts the prompt aside and keeps the pane")
	}
	m = press(m, "esc")
	if m.paneFocused() || m.screen != screenHome {
		t.Fatalf("esc gives the keys back: focus %v screen %v", m.pane.focus, m.screen)
	}
	m = press(m, "k") // up to claude: the chat lets go
	if m.chats.inPane || m.chats.cancel != nil {
		t.Errorf("the chat stayed after the cursor left: %+v", m.chats)
	}
}

func TestBannerDecidesTheAgentsApprovalFromTheSidebar(t *testing.T) {
	m := paneApp()
	m.approvals[0].Network = &policy.NetworkRequest{Host: "proxy.golang.org", Port: 443, SandboxName: "api-refactor"}
	m = press(m, "j", "j", "j", "j", "j") // api-refactor's codex
	if x, _ := m.paneTarget(); x.term.Name != "codex" || m.stateOf(x) != stateBlocked {
		t.Fatalf("on %+v", x)
	}
	out := shown(m)
	if !strings.Contains(out, "api-refactor wants proxy.golang.org:443") || !strings.Contains(out, "y allow · o once · n deny") {
		t.Fatalf("no banner over the blocked agent:\n%s", out)
	}
	for _, k := range []string{"y", "o", "n"} {
		if cmd, ok := m.bannerKey(keyMsg(k)); !ok || cmd == nil {
			t.Errorf("%s does not decide from the sidebar", k)
		}
	}
	if m = press(m, "n"); m.screen != screenHome {
		t.Errorf("n opened %v instead of denying", m.screen)
	}
	if m = press(m, "k"); m.paneBanner(must(m.paneTarget()), 90) != nil {
		t.Error("the main terminal shows the agent's banner")
	}
}

func TestDashboardChartsTheSandbox(t *testing.T) {
	m := press(paneApp(), "j", "j", "j", "j") // api-refactor's header
	if !m.dashShows() {
		t.Fatal("the dashboard does not show on a sandbox's row")
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
	m := press(paneApp(), "j", "j", "j", "j", "j") // codex, which works
	m.sess.terms["sb2"][1].Activity = 0
	m.sessionsRead(sessionsLoaded{at: m.now, terms: m.sess.terms, chats: m.sess.chats})
	if m.sess.done["term/sb2/codex"] || strings.Contains(m.notice.text, "codex") {
		t.Errorf("the session on show was marked done: %v, %q", m.sess.done, m.notice.text)
	}
}

func must(x session, _ bool) session { return x }

func TestCursorFollowsItsSessionAcrossReads(t *testing.T) {
	m := press(paneApp(), "j", "j", "j") // blub's main terminal, its third session
	terms := map[string][]agentproto.Session{
		"sb1": {{Name: "main"}, {Name: "claude", Harness: "claude"}, {Name: "gemini", Harness: "gemini"}},
	}
	m.sessionsRead(sessionsLoaded{at: m.now, terms: terms, chats: m.sess.chats})
	if x, ok := m.selectedSession(); !ok || x.term.Name != "main" {
		t.Fatalf("a new agent above moved the cursor to %+v", x)
	}
}
