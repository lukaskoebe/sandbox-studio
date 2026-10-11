package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeGuest serves every ACP stream with one scripted agent.
type fakeGuest struct {
	agent *acptest.Agent
	mu    sync.Mutex
	opens []agentproto.StartACP
	files int
	conns []io.ReadWriteCloser
}

func (g *fakeGuest) OpenACP(_ context.Context, _ string, req agentproto.StartACP) (io.ReadWriteCloser, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.opens = append(g.opens, req)
	c := g.agent.Pipe()
	g.conns = append(g.conns, c)
	return c, nil
}

func (g *fakeGuest) WriteHomeFiles(_ context.Context, _ string, files []agentproto.HomeFile) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files += len(files)
	return nil
}

// killAgent ends the agent's stream as a stopping sandbox would.
func (g *fakeGuest) killAgent() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.conns {
		c.Close()
	}
}

type fixture struct {
	s     *Service
	g     *fakeGuest
	sb    store.Sandbox
	chat  store.Chat
	agent *acptest.Agent
}

func newFixture(t *testing.T, agent *acptest.Agent) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := st.CreateProvider(ctx, store.Provider{ID: store.NewID(), EnvironmentID: env.ID, Name: "p", Kind: "chatgpt_subscription"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Ada", Harness: "opencode", ProviderID: prov.ID})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, TeammateID: tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	if agent.SessionID == "" {
		agent.SessionID = "ses_1"
	}
	g := &fakeGuest{agent: agent}
	s := &Service{Store: st, Guest: g, Bus: &events.Bus{}}
	t.Cleanup(s.Close)
	c, err := s.Create(ctx, sb, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{s: s, g: g, sb: sb, chat: c, agent: agent}
}

func (f *fixture) launch() Launch {
	return Launch{Harness: harness.OpenCode{}, Files: []agentproto.HomeFile{{Path: ".config/opencode/opencode.json"}}, Env: map[string]string{"OPENAI_API_KEY": "studio-x"}}
}

func (f *fixture) send(t *testing.T, text string) store.ChatTurn {
	t.Helper()
	turn, err := f.s.Send(context.Background(), f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

// wait waits until the turn ended and the chat is idle again.
func (f *fixture) wait(t *testing.T, turnID string) (store.ChatTurn, Log) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := f.s.Store.ChatTurn(ctx, f.chat.ID, turnID)
		c, _ := f.s.Store.Chat(ctx, f.sb.EnvironmentID, f.chat.ID)
		if err == nil && turn.EndedAt != nil && c.State != store.ChatRunning {
			log, err := f.s.Get(ctx, f.sb.EnvironmentID, f.chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			return turn, log
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the turn never ended")
	return store.ChatTurn{}, Log{}
}

func kinds(items []Item) string {
	var k []string
	for _, it := range items {
		s := it.Kind
		if it.Marker != nil {
			s += ":" + it.Marker.Kind
		}
		k = append(k, s)
	}
	return strings.Join(k, ",")
}

func steps(updates ...string) []acptest.Step {
	var out []acptest.Step
	for _, u := range updates {
		out = append(out, acptest.Step{Update: json.RawMessage(u)})
	}
	return out
}

func TestChatTurn(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{
		Steps: steps(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}`,
			`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"ls","kind":"execute","status":"in_progress"}`,
			`{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"a.go"}}]}`,
			`{"sessionUpdate":"usage_update","used":10,"size":100}`),
		Usage: json.RawMessage(`{"inputTokens":5,"outputTokens":7}`),
	}}})
	turn := f.send(t, "List the files\nplease")
	if turn.Seq != 1 || turn.Origin != store.OriginChat {
		t.Fatalf("turn %+v", turn)
	}
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != "end_turn" {
		t.Fatalf("stop %q", turn.StopReason)
	}
	if got := kinds(log.Items); got != "user,agent,tool" {
		t.Fatalf("items %s", got)
	}
	if log.Items[0].User.Text != "List the files\nplease" || log.Items[1].Text.Text != "Hello" || log.Items[2].Tool.Status != "completed" {
		t.Fatalf("bodies %+v %+v %+v", log.Items[0].User, log.Items[1].Text, log.Items[2].Tool)
	}
	if u := log.Turns[0].Usage; u == nil || string(u.Tokens) != `{"inputTokens":5,"outputTokens":7}` || *u.ContextUsed != 10 {
		t.Fatalf("usage %+v", log.Turns[0].Usage)
	}
	if log.Chat.Title != "List the files" || log.Chat.ACPSessionID != "ses_1" || log.Chat.State != store.ChatIdle {
		t.Fatalf("chat %+v", log.Chat)
	}
	// The agent ran as planned, with Studio's client info and the workspace as cwd.
	if len(f.g.opens) != 1 || f.g.opens[0].Chat != f.chat.ID || f.g.opens[0].Command[0] != "opencode" || f.g.opens[0].Env["OPENAI_API_KEY"] != "studio-x" || f.g.files != 1 {
		t.Fatalf("opens %+v files %d", f.g.opens, f.g.files)
	}
	var init struct {
		ClientInfo struct{ Name, Version string }
	}
	json.Unmarshal(f.agent.Initial, &init)
	if init.ClientInfo.Name != "sandbox-studio" || init.ClientInfo.Version == "" {
		t.Fatalf("initialize %s", f.agent.Initial)
	}
	if !strings.Contains(string(f.agent.NewReq), `"cwd":"/workspace"`) {
		t.Fatalf("session/new %s", f.agent.NewReq)
	}

	// The second prompt reuses the agent; after the agent went away, the next one
	// resumes the session.
	turn = f.send(t, "again")
	f.wait(t, turn.ID)
	f.g.killAgent()
	deadline := time.Now().Add(5 * time.Second)
	for f.s.get(f.chat.ID) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	turn = f.send(t, "third")
	if turn.Seq != 3 {
		t.Fatalf("seq %d", turn.Seq)
	}
	f.wait(t, turn.ID)
	calls := strings.Join(f.agent.Called(), ",")
	if strings.Count(calls, "session/new") != 1 || strings.Count(calls, "session/resume") != 1 || len(f.g.opens) != 2 {
		t.Fatalf("calls %s", calls)
	}
}

func TestChatBusyAndEmpty(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{WaitCancel: true}}}}})
	ctx := context.Background()
	if _, err := f.s.Send(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "  "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty prompt: %v", err)
	}
	turn := f.send(t, "work")
	if _, err := f.s.Send(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "more"}); !errors.Is(err, ErrBusy) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second prompt: %v", err)
	}
	if err := f.s.Cancel(ctx, f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != "cancelled" || kinds(log.Items) != "user,marker:stopped" {
		t.Fatalf("stop %q items %s", turn.StopReason, kinds(log.Items))
	}
}

// An agent that ignores session/cancel is stopped after the grace period.
func TestChatCancelGrace(t *testing.T) {
	old := cancelGrace
	cancelGrace = 50 * time.Millisecond
	defer func() { cancelGrace = old }()
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Hang: true}}}}})
	turn := f.send(t, "work")
	time.Sleep(20 * time.Millisecond)
	if err := f.s.Cancel(context.Background(), f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != StopInterrupted || kinds(log.Items) != "user,marker:interrupted" {
		t.Fatalf("stop %q items %s", turn.StopReason, kinds(log.Items))
	}
}

func TestChatOrphaned(t *testing.T) {
	f := newFixture(t, &acptest.Agent{MissingSessions: true})
	ctx := context.Background()
	if err := f.s.Store.SetChatSession(ctx, f.chat.ID, "ses_gone"); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Send(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "hi"})
	if !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "session of this chat is gone") {
		t.Fatalf("send: %v", err)
	}
	c, _ := f.s.Store.Chat(ctx, f.sb.EnvironmentID, f.chat.ID)
	if c.State != store.ChatOrphaned {
		t.Fatalf("state %s", c.State)
	}
	if _, err := f.s.Send(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "hi"}); !strings.Contains(fmt_(err), "start a new chat") {
		t.Fatalf("again: %v", err)
	}
}

func fmt_(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestChatAuthRequired(t *testing.T) {
	f := newFixture(t, &acptest.Agent{AuthRequired: true})
	_, err := f.s.Send(context.Background(), f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "hi"})
	if !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "login") {
		t.Fatalf("send: %v", err)
	}
	c, _ := f.s.Store.Chat(context.Background(), f.sb.EnvironmentID, f.chat.ID)
	if c.State != store.ChatIdle {
		t.Fatalf("state %s", c.State)
	}
}

// A restart marks running turns interrupted and their chats idle.
func TestRecoverInterruptsRunningTurns(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	turn, err := f.s.Store.StartChatTurn(ctx, f.chat.ID, store.OriginChat, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Store.SetChatState(ctx, f.sb.EnvironmentID, f.chat.ID, store.ChatRunning); err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe := f.s.Bus.Subscribe()
	defer unsubscribe()
	if err := f.s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != StopInterrupted || kinds(log.Items) != "marker:interrupted" || log.Chat.State != store.ChatIdle {
		t.Fatalf("turn %+v items %s chat %s", turn, kinds(log.Items), log.Chat.State)
	}
	if e := <-ch; e.Topic != events.TopicChats || e.ID != f.chat.ID {
		t.Fatalf("event %+v", e)
	}
}

// Streaming text is written at most every flushEvery, not once per chunk.
func TestChatCoalescesWrites(t *testing.T) {
	var updates []string
	for i := 0; i < 200; i++ {
		updates = append(updates, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}`)
	}
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{Steps: steps(updates...)}}})
	turn := f.send(t, "write")
	_, log := f.wait(t, turn.ID)
	if got := log.Items[1].Text.Text; len(got) != 200 {
		t.Fatalf("text %d", len(got))
	}
	if log.Chat.Rev > 40 {
		t.Fatalf("%d writes for 200 chunks", log.Chat.Rev)
	}
}

func TestChatDelete(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{WaitCancel: true}}}}})
	f.send(t, "work")
	if err := f.s.Delete(context.Background(), f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Store.Chat(context.Background(), f.sb.EnvironmentID, f.chat.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("chat: %v", err)
	}
	if !strings.Contains(strings.Join(f.agent.Called(), ","), "session/close") {
		t.Fatalf("calls %v", f.agent.Called())
	}
}
