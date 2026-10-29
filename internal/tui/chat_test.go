package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// item is a chat item of turn t1 of chat c1.
func item(id string, seq int, kind string) chat.Item {
	return chat.Item{ChatItem: store.ChatItem{ID: id, ChatID: "c1", TurnID: "t1", Seq: seq, Kind: kind, Rev: int64(seq)}}
}

// sampleChat opens a chat of blub with Iris on the sample app: a prompt, a thought, two tool
// calls and an answer with a heading and a code block.
func sampleChat(m *app) *app {
	m.chats = chatState{sandbox: m.sandboxes[0], back: screenHome, fold: -1}
	m.openChat("c1")
	c := &m.chats
	c.loaded = true
	c.chat = store.Chat{ID: "c1", SandboxID: "sb1", TeammateID: "t1", Title: "Fix the login form", State: store.ChatIdle, UpdatedAt: m.now.Add(-3 * time.Minute)}
	ended := m.now.Add(-time.Minute)
	c.turns["t1"] = chat.Turn{ChatTurn: store.ChatTurn{ID: "t1", ChatID: "c1", Seq: 1, EndedAt: &ended}}

	user := item("i1", 1, chat.KindUser)
	user.User = &chat.UserBody{Text: "The login form doesn't submit on enter. Can you fix it?"}
	thought := item("i2", 2, chat.KindThought)
	thought.Text = &chat.TextBody{Text: "The form probably lacks a submit button."}
	read := item("i3", 3, chat.KindTool)
	line := 12
	read.Tool = &chat.ToolBody{Title: "Read src/login.tsx", Kind: "read", Status: "completed",
		Locations: []acp.ToolCallLocation{{Path: "src/login.tsx", Line: &line}}}
	edit := item("i4", 4, chat.KindTool)
	old := "<div onClick={submit}>Sign in</div>"
	edit.Tool = &chat.ToolBody{Title: "Edit src/login.tsx", Kind: "edit", Status: "completed",
		Content: []acp.ToolCallContent{{Type: "diff", Path: "src/login.tsx", OldText: &old, NewText: `<button type="submit">Sign in</button>`}}}
	answer := item("i5", 5, chat.KindAgent)
	answer.Text = &chat.TextBody{Text: "## Fixed\n\nThe form used a `div` as its button, so enter did nothing. It is a real submit button now:\n\n```tsx\n<button type=\"submit\">Sign in</button>\n```\n\n- Enter submits the form.\n- The button is focusable."}
	for _, it := range []chat.Item{user, thought, read, edit, answer} {
		c.items[it.ID] = it
	}
	c.rev = 5
	return m
}

func TestGoldenChat(t *testing.T) {
	golden(t, "chat", sampleChat(sampleApp()))
}

func TestGoldenChatToolExpanded(t *testing.T) {
	golden(t, "chat_tool", press(sampleChat(sampleApp()), "k", "enter"))
}

func TestGoldenChatComposing(t *testing.T) {
	golden(t, "chat_composing", press(sampleChat(sampleApp()), "p", "T", "h", "a", "n", "k", "s"))
}

func TestGoldenChatRunning(t *testing.T) {
	m := sampleChat(sampleApp())
	m.chats.chat.State = store.ChatRunning
	turn := chat.Turn{ChatTurn: store.ChatTurn{ID: "t2", ChatID: "c1", Seq: 2}}
	m.chats.turns["t2"] = turn
	prompt := item("i6", 6, chat.KindUser)
	prompt.TurnID, prompt.User = "t2", &chat.UserBody{Text: "Add a test for it."}
	run := item("i7", 7, chat.KindTool)
	run.TurnID, run.Tool = "t2", &chat.ToolBody{Title: "npm test -- login", Kind: "execute", Status: "in_progress"}
	m.chats.items["i6"], m.chats.items["i7"] = prompt, run
	m.chats.sorted = nil // sorted again, as the stream would
	golden(t, "chat_running", m)
}

func TestChatNeedsATeammate(t *testing.T) {
	m := sampleApp()
	m.cursor = 3 // demo has no teammate
	m = press(m, "c")
	if m.screen != screenHome || !m.notice.failed || !strings.Contains(m.notice.text, "no teammate") {
		t.Fatalf("chat of a sandbox without a teammate: screen %v, notice %+v", m.screen, m.notice)
	}
}

func TestChatOpensFromHomeAndDetail(t *testing.T) {
	next, cmd := sampleApp().Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m := next.(*app)
	if m.screen != screenHome || m.chats.sandbox.ID != "sb1" || !m.chats.picking || cmd == nil {
		t.Fatalf("c on Home: screen %v, sandbox %q, picking %v, cmd %v", m.screen, m.chats.sandbox.ID, m.chats.picking, cmd != nil)
	}
	newest := store.Chat{ID: "c1", SandboxID: "sb1", State: store.ChatIdle, UpdatedAt: time.Now()}
	next, _ = m.Update(chatsListed{sandbox: "sb1", list: []store.Chat{{ID: "c0"}, newest}})
	if m = next.(*app); m.screen != screenChat || m.chats.id != "c1" {
		t.Fatalf("c opens the newest chat: screen %v, chat %q", m.screen, m.chats.id)
	}
	if m = press(m, "esc"); m.screen != screenHome {
		t.Fatalf("esc from the chat returns to %v", m.screen)
	}

	m = press(sampleApp(), "j", "enter", "c")
	next, _ = m.Update(chatsListed{sandbox: "sb2"})
	if m = next.(*app); m.screen != screenChat || m.chats.id != "" || !m.chats.composing {
		t.Fatalf("c without chats opens a new one: screen %v, chat %q, composing %v", m.screen, m.chats.id, m.chats.composing)
	}
	if m = press(m, "esc"); m.screen != screenDetail {
		t.Fatalf("esc from the new chat returns to %v", m.screen)
	}
	if m = press(sampleApp(), "C"); m.screen != screenChat || m.chats.id != "" || !m.chats.composing {
		t.Fatalf("C: screen %v, chat %q, composing %v", m.screen, m.chats.id, m.chats.composing)
	}
}

func TestToolCallsFoldAndExpand(t *testing.T) {
	m := sampleChat(sampleApp())
	if got := m.chats.fold; got != -1 {
		t.Fatalf("a folded item is selected from the start: %d", got)
	}
	plain := strings.Join(m.transcript(80).lines, "\n")
	if strings.Contains(plain, "button type") && strings.Contains(plain, "+ ") {
		t.Fatal("the edit's diff shows while folded")
	}
	m = press(m, "k")
	if m.chats.fold != 2 {
		t.Fatalf("k selects the last folded item: %d", m.chats.fold)
	}
	m = press(m, "enter")
	if !m.chats.expanded["i4"] {
		t.Fatal("enter did not expand the edit")
	}
	if got := strings.Join(m.transcript(80).lines, "\n"); !strings.Contains(got, `+ <button type="submit">Sign in</button>`) {
		t.Fatalf("the expanded edit lacks its diff:\n%s", got)
	}
	m = press(m, "enter")
	if m.chats.expanded["i4"] {
		t.Fatal("enter again did not fold it")
	}
	m = press(m, "k", "k", "k")
	if m.chats.fold != 0 {
		t.Fatalf("k stops at the first folded item: %d", m.chats.fold)
	}
	m = press(m, "j", "j", "j")
	if m.chats.fold != -1 || m.chats.scroll != 0 {
		t.Fatalf("j past the last folded item follows the end again: fold %d, scroll %d", m.chats.fold, m.chats.scroll)
	}
}

func TestPromptSendsAndKeepsTheDraftOnFailure(t *testing.T) {
	m := press(sampleChat(sampleApp()), "p", "h", "i")
	if !m.chats.composing || m.chats.draft.value() != "hi" {
		t.Fatalf("composer: %v %q", m.chats.composing, m.chats.draft.value())
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*app)
	if cmd == nil || m.chats.composing || !m.chats.sending {
		t.Fatalf("enter: cmd %v, composing %v, sending %v", cmd != nil, m.chats.composing, m.chats.sending)
	}
	next, _ = m.Update(promptSent{gen: m.chats.gen, err: errors.New("a turn is running")})
	m = next.(*app)
	if !m.chats.composing || m.chats.draft.value() != "hi" || !m.notice.failed {
		t.Fatalf("a failed send: composing %v, draft %q, notice %+v", m.chats.composing, m.chats.draft.value(), m.notice)
	}
	m.chats.composing, m.chats.sending = false, true
	next, _ = m.Update(promptSent{gen: m.chats.gen})
	m = next.(*app)
	if m.chats.draft.value() != "" || m.chats.sending {
		t.Fatalf("a sent prompt: draft %q, sending %v", m.chats.draft.value(), m.chats.sending)
	}
}

func TestComposerTakesPastesAndNewLines(t *testing.T) {
	m := press(sampleChat(sampleApp()), "p")
	next, _ := m.Update(tea.PasteMsg{Content: "one\r\ntwo"})
	m = next.(*app)
	next, _ = m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = press(next.(*app), "x")
	if got := m.chats.draft.value(); got != "one\ntwo\nx" {
		t.Fatalf("draft %q", got)
	}
	m = press(m, "backspace", "backspace")
	if got := m.chats.draft.value(); got != "one\ntwo" {
		t.Fatalf("after backspaces: %q", got)
	}
}

func TestNewChatStartsWithTheComposer(t *testing.T) {
	m := press(sampleChat(sampleApp()), "n")
	if m.screen != screenChat || m.chats.id != "" || !m.chats.composing {
		t.Fatalf("n: screen %v, chat %q, composing %v", m.screen, m.chats.id, m.chats.composing)
	}
	m = press(m, "h", "i")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*app)
	if cmd == nil || !m.chats.sending {
		t.Fatal("enter did not start the chat")
	}
	next, _ = m.Update(chatCreated{chat: store.Chat{ID: "c9", State: store.ChatRunning}})
	m = next.(*app)
	if m.screen != screenChat || m.chats.id != "c9" || m.chats.composing {
		t.Fatalf("created: screen %v, chat %q, composing %v", m.screen, m.chats.id, m.chats.composing)
	}
	m = press(sampleChat(sampleApp()), "n", "esc")
	if m.screen != screenHome {
		t.Fatalf("esc from a new chat returns to %v", m.screen)
	}
}

func TestStreamKeepsTheNewestVersionOfEachRow(t *testing.T) {
	m := sampleChat(sampleApp())
	gen := m.chats.gen
	newer := item("i5", 5, chat.KindAgent)
	newer.Rev, newer.Text = 9, &chat.TextBody{Text: "Updated"}
	older := newer
	older.Rev, older.Text = 7, &chat.TextBody{Text: "Stale"}
	for _, ev := range []ChatEvent{{Rev: 9, Item: &newer}, {Rev: 7, Item: &older}} {
		next, cmd := m.Update(chatEventMsg{gen: gen, ev: ev})
		m = next.(*app)
		if cmd == nil {
			t.Fatal("the stream is not listened to after an event")
		}
	}
	if got := m.chats.items["i5"].Text.Text; got != "Updated" || m.chats.rev != 9 {
		t.Fatalf("item %q at rev %d", got, m.chats.rev)
	}
	stale := item("i6", 6, chat.KindAgent)
	next, _ := m.Update(chatEventMsg{gen: gen - 1, ev: ChatEvent{Rev: 10, Item: &stale}})
	if _, ok := next.(*app).chats.items["i6"]; ok {
		t.Fatal("an event of a chat left behind was applied")
	}
}

func TestLostChatStreamResumesFromTheLastRev(t *testing.T) {
	m := sampleChat(sampleApp())
	next, cmd := m.Update(chatStreamEnded{gen: m.chats.gen})
	m = next.(*app)
	if m.chats.live || cmd == nil {
		t.Fatalf("lost stream: live %v, retry %v", m.chats.live, cmd != nil)
	}
	next, cmd = m.Update(chatRetry{gen: m.chats.gen})
	m = next.(*app)
	if cmd == nil || m.chats.cancel == nil {
		t.Fatal("the retry does not reopen the stream")
	}
	m.stopChat()
}

func TestStopOnlyWhileATurnRuns(t *testing.T) {
	m := press(sampleChat(sampleApp()), "x")
	if m.notice.text != "No turn is running." {
		t.Fatalf("x on an idle chat: %q", m.notice.text)
	}
	m.chats.chat.State = store.ChatRunning
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd == nil || next.(*app).notice.text != "Stopping the turn…" {
		t.Fatalf("x on a running chat: cmd %v, notice %q", cmd != nil, next.(*app).notice.text)
	}
}

func TestEndedChatsTakeNoPrompt(t *testing.T) {
	m := sampleChat(sampleApp())
	m.chats.chat.State = store.ChatOrphaned
	m = press(m, "p")
	if m.chats.composing || !strings.Contains(m.notice.text, "ended") {
		t.Fatalf("p on an ended chat: composing %v, notice %q", m.chats.composing, m.notice.text)
	}
}

func TestMarkdownLinesStyleLightly(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := renderMarkdown("# Title\nSome text.\n```go\nfunc main() {}\n```\n#hashtag", 40, false)
	want := []string{"Title", "Some text.", "    func main() {}", "#hashtag"}
	if len(got) != len(want) {
		t.Fatalf("lines %q, want %q", got, want)
	}
	for i := range want {
		if stripped := stripANSI(got[i]); stripped != want[i] {
			t.Errorf("line %d: %q, want %q", i, stripped, want[i])
		}
	}
	if got[0] == "Title" {
		t.Error("the heading is not bold")
	}
}

func TestWrapKeepsListIndents(t *testing.T) {
	got := wrapText("- one two three four five six", 16)
	if len(got) < 2 || !strings.HasPrefix(got[1], "  ") {
		t.Fatalf("wrapped list item: %q", got)
	}
}

// stripANSI drops the escape sequences of styled text, and the links' too: an OSC sequence
// runs from ESC ] to a BEL or ESC \.
func stripANSI(s string) string {
	var b strings.Builder
	esc, osc := false, false
	for _, r := range s {
		switch {
		case osc && r == '\a':
			osc = false
		case osc && r == 0x1b:
			esc = true
		case osc && esc && r == '\\':
			osc, esc = false, false
		case osc:
			esc = false
		case r == 0x1b:
			esc = true
		case esc && r == ']':
			esc, osc = false, true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// addChat records a chat of sb with a finished turn: a prompt and an answer.
func (s *testStudio) addChat(t *testing.T, sb store.Sandbox, title string) store.Chat {
	t.Helper()
	ctx := context.Background()
	c, err := s.Store.CreateChat(ctx, store.Chat{EnvironmentID: s.Env.ID, SandboxID: sb.ID, TeammateID: "t1", Harness: "claude", Title: title})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Store.StartChatTurn(ctx, c.ID, "chat", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []struct{ kind, body string }{
		{chat.KindUser, `{"text":"Say hello"}`},
		{chat.KindTool, `{"title":"Read README.md","kind":"read","status":"completed"}`},
		{chat.KindAgent, `{"text":"Hello"}`},
	} {
		if _, err := s.Store.AddChatItem(ctx, store.ChatItem{ChatID: c.ID, TurnID: turn.ID, Kind: it.kind, Body: json.RawMessage(it.body)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Store.EndChatTurn(ctx, c.ID, turn.ID, "end_turn", nil); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestChatStreamFollowsAChat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	c := s.addChat(t, sb, "Greeting")
	client := connect(t, s)

	list, err := client.SandboxChats(ctx, s.Env.ID, sb.ID)
	if err != nil || len(list) != 1 || list[0].Title != "Greeting" {
		t.Fatalf("chats: %+v, %v", list, err)
	}
	log, err := client.Chat(ctx, s.Env.ID, c.ID, 0, 0)
	if err != nil || len(log.Items) != 3 || len(log.Turns) != 1 {
		t.Fatalf("chat: %d items, %d turns, %v", len(log.Items), len(log.Turns), err)
	}

	// From rev 0 the stream brings every row at its latest rev, in rev order: the items, then
	// the turn its end changed, then the chat.
	ch, err := client.ChatStream(ctx, s.Env.ID, c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var last int64
	for len(kinds) < 5 {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("the stream ended after %v", kinds)
			}
			if ev.Rev < last {
				t.Fatalf("rev went back from %d to %d", last, ev.Rev)
			}
			last = ev.Rev
			switch {
			case ev.Item != nil:
				kinds = append(kinds, ev.Item.Kind)
			case ev.Turn != nil:
				kinds = append(kinds, "turn")
			case ev.Chat != nil:
				kinds = append(kinds, "chat")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the stream stalled after %v", kinds)
		}
	}
	if got := strings.Join(kinds, ","); got != "user,tool,agent,turn,chat" {
		t.Fatalf("events: %s", got)
	}

	if _, err := client.ChatStream(ctx, s.Env.ID, "nope", 0); apiStatus(err) != 404 {
		t.Fatalf("the stream of a missing chat: %v", err)
	}
}

func TestChatScreensLoadFromTheServer(t *testing.T) {
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	c := s.addChat(t, sb, "Greeting")
	m := newApp(context.Background(), connect(t, s), s.Addr)
	m.at = atSandbox // on the sandbox's row, a row below Home's
	m.env = s.Env
	m = run(m, m.loadSandboxes())
	m.sandboxes[0].TeammateID = "t1" // the chat is with this teammate
	m.teammates["t1"] = "Iris"
	m = run(m, m.openChats(m.selected()))
	if m.screen != screenChat || m.chats.id != c.ID {
		t.Fatalf("c opens the newest chat: screen %v, chat %q", m.screen, m.chats.id)
	}
	next, cmd := m.Update(m.loadChat()())
	m = next.(*app)
	if !m.chats.loaded || len(m.chats.items) != 3 || cmd == nil {
		t.Fatalf("chat: loaded %v, %d items, follow %v", m.chats.loaded, len(m.chats.items), cmd != nil)
	}
	next, cmd = m.Update(cmd()) // the stream opens
	m = next.(*app)
	if !m.chats.live || cmd == nil {
		t.Fatalf("stream: live %v", m.chats.live)
	}
	m.stopChat()
}

// A long chat opens with its last turns, as the web's does; the earlier ones load once the
// transcript shows its top, and the view keeps its place. The stream's items for a turn not
// loaded yet wait for their page.
func TestChatLoadsEarlierTurnsAtItsTop(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	c, err := s.Store.CreateChat(ctx, store.Chat{EnvironmentID: s.Env.ID, SandboxID: sb.ID, TeammateID: "t1", Harness: "claude", Title: "Long"})
	if err != nil {
		t.Fatal(err)
	}
	var early store.ChatItem // an answer of the chat's third turn
	for i := range chatPageTurns + 10 {
		turn, err := s.Store.StartChatTurn(ctx, c.ID, "chat", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range []struct{ kind, body string }{
			{chat.KindUser, fmt.Sprintf(`{"text":"prompt %d"}`, i)},
			{chat.KindAgent, fmt.Sprintf(`{"text":"answer %d"}`, i)},
		} {
			added, err := s.Store.AddChatItem(ctx, store.ChatItem{ChatID: c.ID, TurnID: turn.ID, Kind: it.kind, Body: json.RawMessage(it.body)})
			if err != nil {
				t.Fatal(err)
			}
			if i == 2 && it.kind == chat.KindAgent {
				early = added
			}
		}
		if _, err := s.Store.EndChatTurn(ctx, c.ID, turn.ID, "end_turn", nil); err != nil {
			t.Fatal(err)
		}
	}
	m := newApp(ctx, connect(t, s), s.Addr)
	m.env, m.width, m.height = s.Env, 100, 30
	m.chats = chatState{sandbox: sandboxes.View{Sandbox: sb}, back: screenHome, fold: -1}
	m = run(m, m.openChat(c.ID))
	shown := func() string { return stripANSI(strings.Join(m.transcript(m.chatWidth()).lines, "\n")) }
	if got := shown(); !strings.Contains(got, "prompt 49") || strings.Contains(got, "prompt 9\n") ||
		!strings.Contains(got, "↑ 10 earlier turns · scroll up to load them") || m.chats.from != 11 {
		t.Fatalf("the first page: from %d\n%s", m.chats.from, got)
	}

	// The stream changes an answer of a turn not loaded: it waits for its page.
	early.Body, early.Rev = json.RawMessage(`{"text":"answer 2, edited"}`), m.chats.rev+1
	ev := chat.ItemView(early)
	m.chats.apply(ChatEvent{Rev: early.Rev, Item: &ev})
	if strings.Contains(shown(), "answer 2,") {
		t.Fatal("an item of a turn not loaded shows")
	}

	// g goes to the top, which loads the turns before; the rows from the end stay as they were.
	m, cmd := pressCmd(m, "g")
	if cmd == nil || !strings.Contains(shown(), "Loading 10 earlier turns…") {
		t.Fatalf("g at the top loads nothing:\n%s", shown())
	}
	scroll := m.chats.scroll
	m = run(m, cmd)
	got := shown()
	if !strings.Contains(got, "prompt 0\n") || strings.Contains(got, "earlier turn") || m.chats.earlier() != 0 || m.chats.scroll != scroll {
		t.Fatalf("the earlier turns: from %d, scroll %d (was %d)\n%s", m.chats.from, m.chats.scroll, scroll, got)
	}
	if !strings.Contains(got, "answer 2, edited") {
		t.Fatalf("the stream's newer answer lost to the page's older one:\n%s", got)
	}
}
