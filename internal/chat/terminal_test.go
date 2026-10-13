package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func history(updates ...string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(updates))
	for _, u := range updates {
		out = append(out, json.RawMessage(u))
	}
	return out
}

const (
	userChunk  = `{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":%q}}`
	agentChunk = `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":%q}}`
)

func chunk(format, text string) string {
	b, _ := json.Marshal(text)
	return strings.Replace(format, "%q", string(b), 1)
}

func TestTerminalHandover(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{Steps: steps(chunk(agentChunk, "Hi"))}}})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	if _, err := f.s.OpenInTerminal(ctx, env, f.chat.ID, f.launch()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("opened a chat without a session: %v", err)
	}
	turn := f.send(t, "first")
	f.wait(t, turn.ID)

	launch := f.launch()
	launch.TUIEnv = map[string]string{"OPENAI_API_KEY": "studio-tui"}
	name, err := f.s.OpenInTerminal(ctx, env, f.chat.ID, launch)
	if err != nil || name != "chat-"+f.chat.ID {
		t.Fatalf("open: %q %v", name, err)
	}
	c, _ := f.s.Store.Chat(ctx, env, f.chat.ID)
	if c.State != store.ChatTerminal || f.s.get(f.chat.ID) != nil {
		t.Fatalf("state %s, agent still live: %v", c.State, f.s.get(f.chat.ID) != nil)
	}
	if !strings.Contains(strings.Join(f.agent.Called(), ","), "session/close") {
		t.Fatalf("calls %v", f.agent.Called())
	}
	st := f.g.started[0]
	if st.Name != name || st.Chat != f.chat.ID || st.Harness != "opencode" || strings.Join(st.Command, " ") != "opencode --session ses_1" || st.Env["OPENAI_API_KEY"] != "studio-tui" {
		t.Fatalf("started %+v", st)
	}
	// The chat is read-only meanwhile; opening it again finds the running TUI.
	if _, err := f.s.Send(ctx, env, f.chat.ID, f.launch(), Prompt{Text: "x"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("prompt in terminal: %v", err)
	}
	if again, err := f.s.OpenInTerminal(ctx, env, f.chat.ID, launch); err != nil || again != name || len(f.g.started) != 1 {
		t.Fatalf("open again: %v %d", err, len(f.g.started))
	}

	// Back to chat: the TUI is ended, the replay's extra turn is added.
	f.agent.History = history(chunk(userChunk, "first"), chunk(agentChunk, "Hi"),
		chunk(userChunk, "from the TUI"), chunk(agentChunk, "Done there"),
		`{"sessionUpdate":"tool_call","toolCallId":"t9","title":"edit a.go","kind":"edit","status":"completed"}`,
		`{"sessionUpdate":"session_info_update","title":"TUI work"}`)
	if err := f.s.ReturnToChat(ctx, env, f.chat.ID, f.launch()); err != nil {
		t.Fatal(err)
	}
	if len(f.g.killed) != 1 || f.g.killed[0] != name {
		t.Fatalf("killed %v", f.g.killed)
	}
	log, err := f.s.Get(ctx, env, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if log.Chat.State != store.ChatIdle || log.Chat.Title != "TUI work" || len(log.Turns) != 2 || log.Turns[1].Origin != store.OriginTerminal || log.Turns[1].EndedAt == nil {
		t.Fatalf("chat %+v turns %+v", log.Chat, log.Turns)
	}
	if got := kinds(log.Items); got != "user,agent,marker:terminal,user,agent,tool" {
		t.Fatalf("items %s", got)
	}
	if log.Items[3].User.Text != "from the TUI" || log.Items[3].TurnID != log.Turns[1].ID {
		t.Fatalf("terminal prompt %+v", log.Items[3])
	}
	if !strings.Contains(strings.Join(f.agent.Called(), ","), "session/load") {
		t.Fatalf("calls %v", f.agent.Called())
	}

	// Returning again changes nothing; the loaded agent takes the next prompt.
	if err := f.s.ReturnToChat(ctx, env, f.chat.ID, f.launch()); err != nil {
		t.Fatal(err)
	}
	opens := len(f.g.opens)
	turn = f.send(t, "third")
	if turn.Seq != 3 || len(f.g.opens) != opens {
		t.Fatalf("seq %d opens %d/%d", turn.Seq, len(f.g.opens), opens)
	}
	f.wait(t, turn.ID)
}

func TestTerminalRewrittenHistory(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	for _, p := range []string{"one", "two"} {
		f.wait(t, f.send(t, p).ID)
	}
	turns, _ := f.s.Store.ChatTurns(ctx, f.chat.ID)
	for _, tr := range turns {
		if _, err := f.s.Store.SetChatTurnSnapshot(ctx, f.chat.ID, tr.ID, tr.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.OpenInTerminal(ctx, env, f.chat.ID, f.launch()); err != nil {
		t.Fatal(err)
	}
	// The TUI rewound to before "two" and went on differently.
	f.agent.History = history(chunk(userChunk, "one"), chunk(agentChunk, "ok"), chunk(userChunk, "other"), chunk(agentChunk, "ok"))
	if err := f.s.ReturnToChat(ctx, env, f.chat.ID, f.launch()); err != nil {
		t.Fatal(err)
	}
	turns, _ = f.s.Store.ChatTurns(ctx, f.chat.ID)
	if len(turns) != 3 || turns[2].Origin != store.OriginTerminal {
		t.Fatalf("turns %+v", turns)
	}
	for _, tr := range turns {
		if tr.Snapshot != "" {
			t.Fatalf("turn %d kept its snapshot", tr.Seq)
		}
	}
}

func TestTerminalSessionGone(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	f.wait(t, f.send(t, "one").ID)
	if _, err := f.s.OpenInTerminal(ctx, env, f.chat.ID, f.launch()); err != nil {
		t.Fatal(err)
	}
	f.agent.MissingSessions = true
	if err := f.s.ReturnToChat(ctx, env, f.chat.ID, f.launch()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("return: %v", err)
	}
	c, _ := f.s.Store.Chat(ctx, env, f.chat.ID)
	if c.State != store.ChatOrphaned {
		t.Fatalf("state %s", c.State)
	}
	// Deleting a chat in the terminal ends its TUI.
	f.g.sessions = append(f.g.sessions, agentproto.Session{Name: "chat-" + f.chat.ID, Chat: f.chat.ID})
	if _, err := f.s.Store.SetChatState(ctx, env, f.chat.ID, store.ChatTerminal); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Delete(ctx, env, f.chat.ID); err != nil || len(f.g.sessions) != 0 {
		t.Fatalf("delete: %v %v", err, f.g.sessions)
	}
}

// A TUI that fails to start gives its error back and leaves the chat idle, so the chat can
// be used again.
func TestTerminalStartFailureUnlocksChat(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{Steps: steps(chunk(agentChunk, "Hi"))}}})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	f.wait(t, f.send(t, "first").ID)

	f.g.startErr = errors.New("tmux refused the session")
	if name, err := f.s.OpenInTerminal(ctx, env, f.chat.ID, f.launch()); err == nil || name != "" {
		t.Fatalf("open with a failing TUI: %q %v", name, err)
	}
	c, _ := f.s.Store.Chat(ctx, env, f.chat.ID)
	if c.State == store.ChatTerminal {
		t.Fatalf("state %s after the TUI failed to start", c.State)
	}
	f.g.startErr = nil
	if _, err := f.s.OpenInTerminal(ctx, env, f.chat.ID, f.launch()); err != nil {
		t.Fatalf("open after the failure: %v", err)
	}
}
