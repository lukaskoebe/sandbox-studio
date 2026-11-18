package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// withSessions gives the sample app's sandboxes their sessions: blub an agent that finished, a
// chat and main; api-refactor a working agent, a chat that waits on a permission and main;
// docs-site, which is stopped, five chats, the newest a scheduled task's run.
func withSessions(m *app) *app {
	ago := func(d time.Duration) time.Time { return m.now.Add(-d) }
	m.sess = sessionsState{
		env: m.env.ID,
		terms: map[string][]sandboxes.Terminal{
			"sb1": {termOf(agentproto.Session{Name: "main", Attached: 1}, ""), termOf(agentproto.Session{Name: "claude", Harness: "claude", Activity: ago(2 * time.Minute).Unix()}, sandboxes.TerminalIdle)},
			"sb2": {termOf(agentproto.Session{Name: "main"}, ""), termOf(agentproto.Session{Name: "codex", Harness: "codex", Activity: m.now.Unix()}, sandboxes.TerminalWorking)},
		},
		chats: map[string][]store.Chat{
			"sb1": {{ID: "c1", SandboxID: "sb1", TeammateID: "t1", Title: "Fix the login form", State: store.ChatIdle, UpdatedAt: ago(3 * time.Minute)}},
			"sb2": {{ID: "c2", SandboxID: "sb2", TeammateID: "t2", Title: "Port the parser to go", State: store.ChatRunning, UpdatedAt: ago(time.Minute)}},
			"sb3": {
				{ID: "c3", SandboxID: "sb3", TeammateID: "t1", Title: "Update the deploy notes", State: store.ChatIdle, TaskID: "t9", UpdatedAt: ago(5 * time.Hour)},
				{ID: "c4", SandboxID: "sb3", TeammateID: "t1", Title: "Check-in", State: store.ChatIdle, Heartbeat: true, UpdatedAt: ago(30 * time.Hour)},
				{ID: "c5", SandboxID: "sb3", TeammateID: "t1", Title: "Rename the guides", State: store.ChatOrphaned, UpdatedAt: ago(50 * time.Hour)},
				{ID: "c6", SandboxID: "sb3", TeammateID: "t1", Title: "Old one", State: store.ChatIdle, UpdatedAt: ago(70 * time.Hour)},
				{ID: "c7", SandboxID: "sb3", TeammateID: "t1", Title: "Oldest", State: store.ChatIdle, UpdatedAt: ago(90 * time.Hour)},
			},
		},
		working: map[string]bool{"term/sb2/codex": true, "chat/c2": true},
		done:    map[string]bool{"term/sb1/claude": true},
	}
	m.approvals = append(m.approvals, api.ApprovalView{
		Approval:       store.Approval{ID: "p1", SandboxID: "sb2", Kind: "chat.permission", Status: "pending"},
		ChatPermission: &chat.PermissionPayload{ChatID: "c2"},
	})
	return m
}

// termOf is a terminal of a session, as the server lists it.
func termOf(s agentproto.Session, state sandboxes.TerminalState) sandboxes.Terminal {
	if state == "" {
		state = sandboxes.TerminalIdle
	}
	return sandboxes.Terminal{Session: s, Label: firstNonEmpty(s.Harness, s.Name), Agent: s.Harness != "", State: state}
}

func TestGoldenHomeSessions(t *testing.T) {
	golden(t, "home_sessions", withSessions(sampleApp()))
}

func TestGoldenHomeSessionsOnASession(t *testing.T) {
	golden(t, "home_sessions_cursor", press(withSessions(sampleApp()), "j", "j"))
}

func TestGoldenWideHomeSessions(t *testing.T) {
	m := withDetailData(withSessions(visualApp(140, 40)))
	goldenVisual(t, "wide_home_sessions_140x40", press(m, "j"), false)
}

// titles joins the titles of sessions, for comparisons.
func titles(list []session) string {
	var out []string
	for _, x := range list {
		out = append(out, x.title())
	}
	return strings.Join(out, ",")
}

func TestSessionsAreListedInOrder(t *testing.T) {
	m := withSessions(sampleApp())
	if got := titles(m.sessionsOf(m.sandboxes[0])); got != "claude,main" {
		t.Fatalf("blub's terminals, the agent's first: %v", got)
	}
	// Iris's rows: her agent, her threads of the last day, a row for the older ones, her terminals.
	if got := titles(m.threadsOf("t1")); got != "claude,Fix the login form,Update the deploy notes,+ 4 older,main" {
		t.Fatalf("Iris: %v", got)
	}
	// A chat continued in a terminal is listed once, as its terminal.
	m.sess.terms["sb1"] = append(m.sess.terms["sb1"], termOf(agentproto.Session{Name: "chat-c1", Harness: "claude", Chat: "c1"}, ""))
	for _, x := range m.threadsOf("t1") {
		if x.kind == sessionChat && x.chat.ID == "c1" {
			t.Fatalf("the chat in the terminal is listed as a thread too: %+v", x)
		}
	}
}

// threadsOf lists a teammate's threads as the web's sidebar does: every live one, the two latest
// others of the last day, and the one on show; and a teammate's thread on another's computer.
func TestThreadsFollowTheWebsSidebar(t *testing.T) {
	m := sampleApp()
	ago := func(d time.Duration) time.Time { return m.now.Add(-d) }
	thread := func(id string, state string, updated time.Duration) store.Chat {
		return store.Chat{ID: id, SandboxID: "sb1", TeammateID: "t1", Title: id, State: state, UpdatedAt: ago(updated)}
	}
	m.sess = sessionsState{env: m.env.ID, chats: map[string][]store.Chat{
		"sb1": {
			thread("new", store.ChatIdle, time.Minute),
			thread("newer-than-a-day", store.ChatIdle, time.Hour),
			thread("third-of-the-day", store.ChatIdle, 2*time.Hour),
			thread("running", store.ChatRunning, 3*24*time.Hour),
			thread("in-the-terminal", store.ChatTerminal, 4*24*time.Hour),
			thread("asks", store.ChatIdle, 5*24*time.Hour),
			thread("on-show", store.ChatIdle, 6*24*time.Hour),
			thread("old", store.ChatIdle, 7*24*time.Hour),
		},
		// Iris's thread on demo, which isn't hers, is hers to see; Finn's isn't.
		"sb4": {{ID: "elsewhere", SandboxID: "sb4", TeammateID: "t1", Title: "elsewhere", State: store.ChatIdle, UpdatedAt: ago(30 * time.Second)}},
		"sb2": {{ID: "finns", SandboxID: "sb2", TeammateID: "t2", Title: "finns", State: store.ChatIdle, UpdatedAt: ago(time.Second)}},
	}}
	m.approvals = []api.ApprovalView{{
		Approval:       store.Approval{ID: "p", SandboxID: "sb1", Kind: "chat.permission", Status: "pending"},
		ChatPermission: &chat.PermissionPayload{ChatID: "asks"},
	}}
	m.screen, m.chats.id = screenChat, "on-show"
	want := "elsewhere,new,running,in-the-terminal,asks,on-show,+ 3 older" // the day's third and fourth, and old
	if got := titles(m.threadsOf("t1")); got != want {
		t.Fatalf("Iris's threads:\n%v\nwant\n%v", got, want)
	}
	m.screen = screenHome
	m.showOlder("t1", true)
	if got := titles(m.threadsOf("t1")); !strings.HasSuffix(got, "on-show,old") || strings.Contains(got, "older") {
		t.Fatalf("with the older ones: %v", got)
	}
}

func TestSessionStates(t *testing.T) {
	m := withSessions(sampleApp())
	want := map[string]agentState{
		"claude": stateDone, "main": stateNone, "Fix the login form": stateIdle,
		"codex":                 stateBlocked, // a network approval waits in api-refactor
		"Port the parser to go": stateBlocked,
	}
	for _, sb := range m.sandboxes[:2] {
		for _, x := range m.sessionsOf(sb) {
			if got := m.stateOf(x); got != want[x.title()] {
				t.Errorf("%s in %s: state %d, want %d", x.title(), sb.Name, got, want[x.title()])
			}
		}
	}
	m.approvals = nil
	codex := m.sessionsOf(m.sandboxes[1])[0]
	if got := m.stateOf(codex); got != stateWorking {
		t.Fatalf("codex without approvals: %d", got)
	}
	codex.term.Activity = 0
	delete(m.sess.working, codex.id())
	if got := m.stateOf(codex); got != stateUnknown {
		t.Fatalf("an agent whose guest says nothing of its output: %d", got)
	}
	if w, b := withSessions(sampleApp()).sessionCounts(); w != 0 || b != 2 {
		t.Fatalf("counts: %d working, %d blocked", w, b)
	}
}

func TestAReadMarksFinishedSessionsDone(t *testing.T) {
	m := withSessions(sampleApp())
	terms := map[string][]sandboxes.Terminal{
		"sb2": {termOf(agentproto.Session{Name: "codex", Harness: "codex", Activity: m.now.Add(-time.Minute).Unix()}, sandboxes.TerminalIdle)},
	}
	m.sessionsRead(sessionsLoaded{at: m.now, terms: terms, chats: m.sess.chats})
	if !m.sess.done["term/sb2/codex"] || m.sess.working["term/sb2/codex"] {
		t.Fatalf("codex stopped printing: done %v, working %v", m.sess.done, m.sess.working)
	}
	if !m.sess.working["chat/c2"] {
		t.Fatal("the running chat no longer counts as working")
	}
	if !strings.Contains(m.notice.text, "codex in api-refactor is done.") {
		t.Fatalf("notice: %q", m.notice.text)
	}
	terms["sb2"][0].State = sandboxes.TerminalWorking
	m.sessionsRead(sessionsLoaded{at: m.now, terms: terms, chats: m.sess.chats})
	if m.sess.done["term/sb2/codex"] || !m.sess.working["term/sb2/codex"] {
		t.Fatal("codex printing again is not working again")
	}
}

func TestCursorWalksTheRows(t *testing.T) {
	m := press(withSessions(sampleApp()), "j")
	if x, ok := m.selectedSession(); !ok || x.title() != "claude" || m.selected().ID != "sb1" {
		t.Fatalf("j from Iris: %+v", x)
	}
	m = press(m, "j", "j")
	if x, ok := m.selectedSession(); !ok || x.title() != "Update the deploy notes" || m.selected().ID != "sb3" {
		t.Fatalf("a thread selects its own computer: %+v, %s", x, m.selectedID())
	}
	m = press(m, "j", "j", "j")
	if _, ok := m.selectedSession(); ok || m.at != atMate || m.selected().ID != "sb2" {
		t.Fatalf("j past Iris's rows lands on Finn: %v %q", m.at, m.mate)
	}
	m = press(m, "k")
	if x, _ := m.selectedSession(); x.title() != "main" || m.selected().ID != "sb1" {
		t.Fatalf("k returns to Iris's last row: %+v", x)
	}
	// A computer that goes away puts the cursor on a computer's own row.
	m.reselect("gone")
	if m.sub != -1 {
		t.Fatalf("sub %d", m.sub)
	}
}

func TestNumbersEnterSessions(t *testing.T) {
	m := withSessions(sampleApp())
	m, cmd := pressCmd(m, "2")
	if m.screen != screenChat || m.chats.id != "c1" || !m.chats.composing || m.chats.back != screenHome {
		t.Fatalf("2 opens the chat with the composer: screen %v, chat %q, composing %v", m.screen, m.chats.id, m.chats.composing)
	}
	if cmd == nil {
		t.Fatal("2 did not load the chat")
	}
	m = press(m, "esc", "esc")
	if m.screen != screenHome {
		t.Fatalf("esc esc returns to Home: %v", m.screen)
	}
	m, cmd = pressCmd(m, "1")
	if cmd == nil || m.sess.done["term/sb1/claude"] {
		t.Fatalf("1 attaches claude and clears its done: cmd %v, done %v", cmd != nil, m.sess.done)
	}
	if x, _ := m.selectedSession(); x.title() != "claude" {
		t.Fatalf("the cursor is not on claude: %+v", x)
	}
	// A blocked chat opens without the composer, so its permission takes the keys.
	m, _ = pressCmd(withSessions(sampleApp()), "6")
	if m.screen != screenChat || m.chats.id != "c2" || m.chats.composing {
		t.Fatalf("6: screen %v, chat %q, composing %v", m.screen, m.chats.id, m.chats.composing)
	}
}

func TestOlderRowShowsTheOlderThreads(t *testing.T) {
	m := onSession(t, withSessions(sampleApp()), "sb1", "+ 4 older")
	if x, ok := m.selectedSession(); !ok || x.kind != sessionOlder {
		t.Fatalf("the cursor is not on the older row: %+v", x)
	}
	m = press(m, "enter")
	if got := len(m.threadsOf("t1")); got != 8 {
		t.Fatalf("Iris lists %d rows after enter on the older row", got)
	}
}

func TestTabFindsTheSessionsThatWait(t *testing.T) {
	m := press(withSessions(sampleApp()), "tab")
	if x, _ := m.selectedSession(); x.title() != "codex" {
		t.Fatalf("tab goes to the first blocked session: %+v", x)
	}
	m = press(m, "tab")
	if x, _ := m.selectedSession(); x.title() != "Port the parser to go" {
		t.Fatalf("then to the next: %+v", x)
	}
	m.approvals = nil
	m = press(m, "tab")
	if x, _ := m.selectedSession(); x.title() != "claude" {
		t.Fatalf("with nothing blocked, tab goes to the done one: %+v", x)
	}
	m.sess.done = nil
	m = press(m, "tab")
	if m.notice.text != "Nothing waits for you." {
		t.Fatalf("notice %q", m.notice.text)
	}
}

func TestStartAgentNeedsARunningSandboxWithATeammate(t *testing.T) {
	m := sampleApp()
	m.cursor = 3
	if cmd := m.startAgent(m.selected()); cmd != nil || !strings.Contains(m.notice.text, "no teammate") {
		t.Fatalf("demo: %q", m.notice.text)
	}
	m.cursor = 2
	if cmd := m.startAgent(m.selected()); cmd != nil || m.notice.text != "Start docs-site first, with s." {
		t.Fatalf("docs-site: %q", m.notice.text)
	}
	m.cursor = 0
	if cmd := m.startAgent(m.selected()); cmd == nil {
		t.Fatal("blub started nothing")
	}
}

func TestSessionsLoadFromTheServer(t *testing.T) {
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	c := s.addChat(t, sb, "Greeting")
	m := newApp(context.Background(), connect(t, s), s.Addr)
	m.env = s.Env
	m = run(m, m.loadSandboxes())
	if !m.sess.busy {
		t.Fatal("the sandboxes' arrival did not start a read of their sessions")
	}
	m.team = []store.Teammate{{ID: "t1", Name: "Iris"}} // the chat is with this teammate
	m.sess.busy = false
	cmd := m.watchSessions()
	if cmd == nil || m.watchSessions() != nil {
		t.Fatal("Home reads the sessions once at a time")
	}
	next, tick := m.Update(cmd())
	m = next.(*app)
	if list := m.threadsOf("t1"); len(list) != 1 || list[0].chat.ID != c.ID {
		t.Fatalf("Iris's threads: %+v", list)
	}
	chats, err := m.client.EnvironmentChats(context.Background(), s.Env.ID)
	if err != nil || len(chats) != 1 || chats[0].ID != c.ID || chats[0].TeammateID != "t1" {
		t.Fatalf("the environment's chats: %+v, %v", chats, err)
	}
	terms, err := m.client.EnvironmentTerminals(context.Background(), s.Env.ID)
	if err != nil || len(terms) != 0 {
		t.Fatalf("no guest is connected, so no terminals: %+v, %v", terms, err)
	}
	if tick == nil {
		t.Fatal("no next read is scheduled")
	}
}
