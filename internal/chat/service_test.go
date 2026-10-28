package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeGuest serves every ACP stream with one scripted agent and keeps tmux sessions as a
// list.
type fakeGuest struct {
	agent    *acptest.Agent
	mu       sync.Mutex
	opens    []agentproto.StartACP
	files    int
	conns    []io.ReadWriteCloser
	sessions []agentproto.Session
	started  []agentproto.StartSession
	startErr error // returned by StartSession when set
	killed   []string
	// The workspace as path → content, and its snapshots by ID.
	ws    map[string]string
	snaps map[string]map[string]string
	// turnEdits are what the next turns change, applied as each turn ends.
	turnEdits []map[string]string
	snapErr   error
	noRoom    bool // snapshots are taken but not kept, as on a full disk
	// slots, when set, is how many agents run at once, as the guest's MaxACPProcesses;
	// running holds the chats whose stream is open.
	slots    int
	running  map[string]bool
	openErr  error         // returned by OpenACP when set
	openHold chan struct{} // OpenACP waits for it to close, when set
	// newAgent, when set, serves each stream with an agent of its own, for chats that run
	// side by side; an acptest.Agent serves one stream at a time.
	newAgent func() *acptest.Agent
}

func (g *fakeGuest) Snapshot(_ context.Context, _ string, req agentproto.SnapshotRequest) (agentproto.SnapshotReply, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.snapErr != nil {
		return agentproto.SnapshotReply{}, g.snapErr
	}
	if g.snaps == nil {
		g.snaps = map[string]map[string]string{}
	}
	diff := func(a, b map[string]string, diffs bool) []agentproto.FileChange {
		var out []agentproto.FileChange
		for p, x := range a {
			if y, ok := b[p]; !ok {
				out = append(out, agentproto.FileChange{Path: p, Status: "deleted", Removed: 1})
			} else if x != y {
				c := agentproto.FileChange{Path: p, Status: "modified", Added: 1, Removed: 1}
				if diffs {
					c.Diff = "-" + x + "\n+" + y + "\n"
				}
				out = append(out, c)
			}
		}
		for p := range b {
			if _, ok := a[p]; !ok {
				out = append(out, agentproto.FileChange{Path: p, Status: "added", Added: 1})
			}
		}
		slices.SortFunc(out, func(x, y agentproto.FileChange) int { return strings.Compare(x.Path, y.Path) })
		return out
	}
	switch req.Op {
	case agentproto.SnapshotTake:
		if strings.HasSuffix(req.ID, "-after") && len(g.turnEdits) > 0 {
			g.apply(g.turnEdits[0])
			g.turnEdits = g.turnEdits[1:]
		}
		g.snaps[req.ID] = maps.Clone(g.ws)
		var r agentproto.SnapshotReply
		if base, ok := g.snaps[req.Base]; ok {
			r.Changes = diff(base, g.ws, false)
		}
		if g.noRoom {
			delete(g.snaps, req.ID)
			r.NoRoom = true
		}
		return r, nil
	case agentproto.SnapshotChanges:
		a, ok1 := g.snaps[req.Base]
		b, ok2 := g.snaps[req.ID]
		if !ok1 || !ok2 {
			return agentproto.SnapshotReply{}, errors.New("no such snapshot")
		}
		return agentproto.SnapshotReply{Changes: diff(a, b, true)}, nil
	case agentproto.SnapshotRestore:
		snap, ok := g.snaps[req.ID]
		if !ok {
			return agentproto.SnapshotReply{}, errors.New("no such snapshot")
		}
		g.ws = maps.Clone(snap)
		return agentproto.SnapshotReply{Restored: len(snap)}, nil
	case agentproto.SnapshotGC:
		for id := range g.snaps {
			if !slices.Contains(req.Keep, id) {
				delete(g.snaps, id)
			}
		}
		return agentproto.SnapshotReply{}, nil
	}
	return agentproto.SnapshotReply{}, errors.New("bad op")
}

func (g *fakeGuest) Upload(_ context.Context, _ string, _ agentproto.UploadRequest, _ io.Reader) (agentproto.UploadReply, error) {
	return agentproto.UploadReply{}, errors.New("uploads are not faked")
}

func (g *fakeGuest) Files(_ context.Context, _ string, req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if req.Op == agentproto.FilesRead {
		if c, ok := g.ws[req.Path]; ok {
			return agentproto.FilesReply{Size: int64(len(c)), MimeType: "text/plain; charset=utf-8", Data: []byte(c)}, nil
		}
		return agentproto.FilesReply{}, errors.New("no such file or directory")
	}
	var r agentproto.FilesReply
	for p, c := range g.ws {
		if !strings.Contains(p, "/") {
			r.Entries = append(r.Entries, agentproto.FileEntry{Name: p, Type: "file", Size: int64(len(c))})
		}
	}
	return r, nil
}

// apply changes the fake workspace; an empty content deletes.
func (g *fakeGuest) apply(files map[string]string) {
	if g.ws == nil {
		g.ws = map[string]string{}
	}
	for p, c := range files {
		if c == "" {
			delete(g.ws, p)
		} else {
			g.ws[p] = c
		}
	}
}

func (g *fakeGuest) Sessions(context.Context, string) ([]agentproto.Session, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]agentproto.Session(nil), g.sessions...), nil
}

func (g *fakeGuest) StartSession(_ context.Context, _ string, req agentproto.StartSession) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.startErr != nil {
		return g.startErr
	}
	g.started = append(g.started, req)
	g.sessions = append(g.sessions, agentproto.Session{Name: req.Name, Harness: req.Harness, Chat: req.Chat, Windows: 1})
	return nil
}

func (g *fakeGuest) KillSession(_ context.Context, _, name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.killed = append(g.killed, name)
	g.sessions = slices.DeleteFunc(g.sessions, func(x agentproto.Session) bool { return x.Name == name })
	return nil
}

func (g *fakeGuest) OpenACP(_ context.Context, _ string, req agentproto.StartACP) (io.ReadWriteCloser, error) {
	g.mu.Lock()
	hold := g.openHold
	g.mu.Unlock()
	if hold != nil {
		<-hold
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.opens = append(g.opens, req)
	if g.openErr != nil {
		return nil, g.openErr
	}
	if g.slots > 0 && len(g.running) >= g.slots {
		return nil, agentchan.ErrACPFull
	}
	if g.running == nil {
		g.running = map[string]bool{}
	}
	g.running[req.Chat] = true
	agent := g.agent
	if g.newAgent != nil {
		agent = g.newAgent()
	}
	c := &slotConn{Conn: agent.Pipe(), g: g, chat: req.Chat}
	g.conns = append(g.conns, c)
	return c, nil
}

// slotConn frees its chat's slot when it closes.
type slotConn struct {
	net.Conn
	g    *fakeGuest
	chat string
	once sync.Once
}

func (c *slotConn) Close() error {
	c.once.Do(func() {
		c.g.mu.Lock()
		delete(c.g.running, c.chat)
		c.g.mu.Unlock()
	})
	return c.Conn.Close()
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
	conns := slices.Clone(g.conns)
	g.mu.Unlock()
	for _, c := range conns {
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
	return Launch{Harness: harness.OpenCode{}, Files: []agentproto.HomeFile{{Path: ".config/opencode/opencode.json"}},
		Command: harness.OpenCode{}.ACPCommand(harness.Provider{}), Env: map[string]string{"OPENAI_API_KEY": "studio-x"}}
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
	if err := json.Unmarshal(f.agent.Initial, &init); err != nil {
		t.Fatal(err)
	}
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

	// A page of the log has every turn's header with its message, and the items of the last.
	page, err := f.s.Page(context.Background(), f.chat.EnvironmentID, f.chat.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Turns) != 3 || page.From != 3 || page.Turns[0].Prompt != "List the files\nplease" || page.Turns[2].Prompt != "third" {
		t.Fatalf("page: from %d, turns %+v", page.From, page.Turns)
	}
	for _, it := range page.Items {
		if it.TurnID != page.Turns[2].ID {
			t.Fatalf("an item of another turn: %+v", it)
		}
	}
	if full, err := f.s.Page(context.Background(), f.chat.EnvironmentID, f.chat.ID, 0, 0); err != nil || full.From != 0 || len(full.Items) <= len(page.Items) || full.Turns[1].Prompt != "again" {
		t.Fatalf("every item: from %d, %d items (%v)", full.From, len(full.Items), err)
	}
}

func TestChatBusyAndEmpty(t *testing.T) {
	promptStarted := make(chan struct{})
	releasePrompt := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releasePrompt) }) }
	defer release()
	f := newFixture(t, &acptest.Agent{
		Turns:        []acptest.Turn{{Steps: []acptest.Step{{WaitCancel: true}}}},
		BeforePrompt: func() { close(promptStarted); <-releasePrompt },
	})
	ctx := context.Background()
	if _, err := f.s.Send(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "  "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty prompt: %v", err)
	}
	turn := f.send(t, "work")
	if err := f.s.Cancel(ctx, f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Send(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "more"}); !errors.Is(err, ErrBusy) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second prompt: %v", err)
	}
	select {
	case <-promptStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent did not receive the prompt")
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 5*time.Second)
	defer cancelWait()
	if err := f.agent.WaitForCall(waitCtx, "session/cancel"); err != nil {
		t.Fatal(err)
	}
	release()
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != "cancelled" || kinds(log.Items) != "user,marker:stopped" {
		t.Fatalf("stop %q items %s", turn.StopReason, kinds(log.Items))
	}
	promptAt, cancelAt := -1, -1
	for i, method := range f.agent.Called() {
		if method == "session/prompt" && promptAt < 0 {
			promptAt = i
		}
		if method == "session/cancel" && cancelAt < 0 {
			cancelAt = i
		}
	}
	if promptAt < 0 || cancelAt <= promptAt {
		t.Fatalf("ACP calls did not put prompt before cancel: %v", f.agent.Called())
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
	f := newFixture(t, &acptest.Agent{MissingSessions: true, Turns: []acptest.Turn{{Steps: steps(chunk(agentChunk, "Hi"))}}})
	ctx := context.Background()
	f.wait(t, f.send(t, "hello").ID)
	f.g.killAgent()
	if err := f.s.Store.SetChatSession(ctx, f.chat.ID, "ses_gone"); err != nil {
		t.Fatal(err)
	}
	waitGone(t, f)
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
	// A tool call that was running.
	if _, err := f.s.Store.AddChatItem(ctx, store.ChatItem{ChatID: f.chat.ID, TurnID: turn.ID, Kind: KindTool, Status: "in_progress",
		Body: encodeBody(&ToolBody{Title: "make test", Status: "in_progress"})}); err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe := f.s.Bus.Subscribe()
	defer unsubscribe()
	if err := f.s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != StopInterrupted || kinds(log.Items) != "tool,marker:interrupted" || log.Chat.State != store.ChatIdle {
		t.Fatalf("turn %+v items %s chat %s", turn, kinds(log.Items), log.Chat.State)
	}
	if tool := log.Items[0]; tool.Status != ToolStopped || tool.Tool.Status != ToolStopped {
		t.Fatalf("tool %+v %+v; want it stopped", tool.ChatItem, tool.Tool)
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

// An integration's note starts the next prompt only, and is marked given once that prompt went out.
func TestPromptNoteGoesWithTheNextPromptOnce(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	pending, told := 1, 0
	f.s.PromptNote = func(_ context.Context, envID, sandboxID string) (string, func(), error) {
		if envID != f.sb.EnvironmentID || sandboxID != f.sb.ID {
			t.Errorf("the note was asked for %s/%s", envID, sandboxID)
		}
		if pending == 0 {
			return "", nil, nil
		}
		return "[Studio] your push was rejected", func() { pending--; told++ }, nil
	}
	f.wait(t, f.send(t, "one").ID)
	f.wait(t, f.send(t, "two").ID)
	prompts, _, _ := f.agent.Snapshot()
	if !strings.Contains(string(prompts[0]), "[Studio] your push was rejected") || strings.Contains(string(prompts[1]), "[Studio]") || told != 1 {
		t.Fatalf("prompts %s / %s, told %d", prompts[0], prompts[1], told)
	}
}

// A prompt queued while a turn runs goes out once the chat is idle again.
func TestQueueWaitsForTheRunningTurn(t *testing.T) {
	release := make(chan struct{})
	var prompts atomic.Int32
	f := newFixture(t, &acptest.Agent{
		Turns: []acptest.Turn{{}, {}},
		BeforePrompt: func() {
			if prompts.Add(1) == 1 {
				<-release
			}
		},
	})
	ctx := context.Background()
	first := f.send(t, "work")
	var sent atomic.Bool
	queued, err := f.s.Queue(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "review feedback"}, func() { sent.Store(true) })
	if err != nil || !queued {
		t.Fatalf("Queue = %v, %v; want queued", queued, err)
	}
	if sent.Load() {
		t.Fatal("the queued prompt went out while the turn ran")
	}
	close(release)
	f.wait(t, first.ID)
	deadline := time.Now().Add(5 * time.Second)
	for !sent.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !sent.Load() {
		t.Fatal("the queued prompt was never sent")
	}
	log, err := f.s.Get(ctx, f.sb.EnvironmentID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	var prompts2 []string
	for _, it := range log.Items {
		if it.User != nil {
			prompts2 = append(prompts2, it.User.Text)
		}
	}
	if len(log.Turns) != 2 || strings.Join(prompts2, "|") != "work|review feedback" {
		t.Fatalf("prompts %q in %d turns; want the queued one second", prompts2, len(log.Turns))
	}

	// An idle chat takes it at once.
	f.wait(t, log.Turns[1].ID)
	if queued, err := f.s.Queue(ctx, f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "more"}, nil); err != nil || queued {
		t.Fatalf("Queue on an idle chat = %v, %v; want sent", queued, err)
	}
}

// An image for an agent that can't take images is refused as ErrNoImages, so the user can
// send the text alone: at once on an idle chat, and while a turn runs too, rather than once
// the queued prompt's turn comes.
func TestImageForAnAgentWithoutImagesIsRefused(t *testing.T) {
	release := make(chan struct{})
	var prompts atomic.Int32
	f := newFixture(t, &acptest.Agent{
		Init: json.RawMessage(`{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"promptCapabilities":{"image":false,"embeddedContext":true},` +
			`"sessionCapabilities":{"resume":{}}},"authMethods":[],"agentInfo":{"name":"fake","version":"1"}}`),
		BeforePrompt: func() {
			if prompts.Add(1) == 1 {
				<-release
			}
		},
	})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	shot := Prompt{Text: "1. button \"Sign in\" (#sign-in) at [120, 306]: does nothing", Attachments: []File{{Name: "notes.jpg", MimeType: "image/jpeg", Data: []byte("\xff\xd8jpeg")}}}
	first := f.send(t, "work")
	if _, queued, err := f.s.Enqueue(ctx, env, f.chat.ID, f.launch(), shot, nil); !errors.Is(err, ErrNoImages) || !errors.Is(err, ErrInvalid) || queued {
		t.Fatalf("Enqueue with an image while a turn runs = %v, %v; want ErrNoImages", queued, err)
	}
	if q := f.s.Queued(f.chat.ID); len(q) != 0 {
		t.Fatalf("queued %+v", q)
	}
	text := Prompt{Text: shot.Text}
	if _, queued, err := f.s.Enqueue(ctx, env, f.chat.ID, f.launch(), text, nil); err != nil || !queued {
		t.Fatalf("Enqueue of the text alone = %v, %v; want queued", queued, err)
	}
	close(release)
	f.wait(t, first.ID)
	deadline := time.Now().Add(5 * time.Second)
	for len(f.s.Queued(f.chat.ID)) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	log, err := f.s.Get(ctx, env, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, log.Turns[len(log.Turns)-1].ID)
	if _, err := f.s.Send(ctx, env, f.chat.ID, f.launch(), shot); !errors.Is(err, ErrNoImages) {
		t.Fatalf("Send with an image to an idle chat = %v; want ErrNoImages", err)
	}
}

// A model that reads text only is sent no images, even by an agent that takes them: the image
// is refused as ErrModelNoImages, which says why, and the text alone goes.
func TestImageForATextOnlyModelIsRefused(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{}, {}}})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	launch := f.launch()
	launch.NoImages = true
	shot := Prompt{Text: "1. button \"Sign in\" (#sign-in) at [120, 306]: does nothing", Attachments: []File{{Name: "notes.jpg", MimeType: "image/jpeg", Data: []byte("\xff\xd8jpeg")}}}
	_, err := f.s.Send(ctx, env, f.chat.ID, launch, shot)
	if !errors.Is(err, ErrModelNoImages) || !errors.Is(err, ErrNoImages) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("Send with an image for a text-only model = %v; want ErrModelNoImages", err)
	}
	turn, err := f.s.Send(ctx, env, f.chat.ID, launch, Prompt{Text: shot.Text})
	if err != nil {
		t.Fatalf("Send of the text alone = %v", err)
	}
	f.wait(t, turn.ID)
	prompts, _, _ := f.agent.Snapshot()
	if len(prompts) != 1 || strings.Contains(string(prompts[0]), `"image"`) {
		t.Fatalf("the agent got %s; want the text alone", prompts)
	}
}

// Prompts queued while a turn runs show in the chat in order, one taken off never goes, and the
// rest go one turn after another.
func TestQueuedPromptsShowAndCanBeTakenOff(t *testing.T) {
	release := make(chan struct{})
	var prompts atomic.Int32
	f := newFixture(t, &acptest.Agent{
		Turns: []acptest.Turn{{}, {}, {}},
		BeforePrompt: func() {
			if prompts.Add(1) == 1 {
				<-release
			}
		},
	})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	first := f.send(t, "work")
	wake, stop := f.s.Watch(f.chat.ID)
	defer stop()
	var views []QueuedPrompt
	for _, text := range []string{"steer one", "steer two", "steer three"} {
		q, queued, err := f.s.Enqueue(ctx, env, f.chat.ID, f.launch(), Prompt{Text: text}, nil)
		if err != nil || !queued || q.ID == "" || q.Text != text {
			t.Fatalf("Enqueue(%s) = %+v, %v, %v", text, q, queued, err)
		}
		views = append(views, q)
	}
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("queueing didn't wake the chat's streams")
	}
	if log, _ := f.s.Get(ctx, env, f.chat.ID); len(log.Queued) != 3 || log.Queued[0].Text != "steer one" || log.Queued[2].Text != "steer three" {
		t.Fatalf("queued: %+v", log.Queued)
	}
	if err := f.s.Unqueue(env, f.chat.ID, views[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Unqueue(env, f.chat.ID, views[1].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("taking off twice: %v", err)
	}
	if err := f.s.Unqueue("other", f.chat.ID, views[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another environment took a prompt off: %v", err)
	}
	close(release)
	f.wait(t, first.ID)
	deadline := time.Now().Add(5 * time.Second)
	var log Log
	for time.Now().Before(deadline) {
		log, _ = f.s.Get(ctx, env, f.chat.ID)
		if len(log.Turns) == 3 && log.Turns[2].EndedAt != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var texts []string
	for _, it := range log.Items {
		if it.User != nil {
			texts = append(texts, it.User.Text)
		}
	}
	if got := strings.Join(texts, "|"); got != "work|steer one|steer three" || len(log.Queued) != 0 {
		t.Fatalf("prompts %q, still queued %+v", got, log.Queued)
	}
}

// A sandbox with no room for another chat's agent retires the agent that sat idle longest; its
// chat resumes its session with the next message. With every agent busy, a start is refused.
func TestFullSandboxRetiresTheIdlestAgent(t *testing.T) {
	release := make(chan struct{})
	var hold atomic.Bool
	f := newFixture(t, &acptest.Agent{BeforePrompt: func() {
		if hold.Load() {
			<-release
		}
	}})
	f.g.slots = 2
	var agents []*acptest.Agent
	f.g.newAgent = func() *acptest.Agent {
		a := &acptest.Agent{SessionID: "ses_1", BeforePrompt: f.agent.BeforePrompt}
		agents = append(agents, a)
		return a
	}
	ctx := context.Background()
	env := f.sb.EnvironmentID
	chats := []store.Chat{f.chat}
	for range 2 {
		c, err := f.s.Create(ctx, f.sb, "opencode")
		if err != nil {
			t.Fatal(err)
		}
		chats = append(chats, c)
	}
	sendTo := func(c store.Chat) error {
		turn, err := f.s.Send(ctx, env, c.ID, f.launch(), Prompt{Text: "hi"})
		if err == nil {
			f.chat = c
			f.wait(t, turn.ID)
		}
		return err
	}
	for _, c := range chats {
		if err := sendTo(c); err != nil {
			t.Fatalf("send to %s: %v", c.ID, err)
		}
	}
	if f.s.get(chats[0].ID) != nil || f.s.get(chats[1].ID) == nil || f.s.get(chats[2].ID) == nil {
		t.Fatal("the first chat's agent, idle longest, should have made room")
	}
	if c, _ := f.s.Store.Chat(ctx, env, chats[0].ID); c.State != store.ChatIdle {
		t.Fatalf("the retired agent's chat is %s", c.State)
	}
	// It comes back, in place of the second.
	if err := sendTo(chats[0]); err != nil {
		t.Fatal(err)
	}
	if f.s.get(chats[1].ID) != nil || f.s.get(chats[0].ID) == nil {
		t.Fatal("the second chat's agent should have made room")
	}
	f.g.mu.Lock()
	back := agents[len(agents)-1]
	f.g.mu.Unlock()
	if calls := back.Calls; !slices.Contains(calls, "session/resume") {
		t.Fatalf("calls %v; want the session resumed", calls)
	}

	// Both agents busy: the third chat is told so, and no agent stops.
	hold.Store(true)
	defer close(release)
	for _, c := range []store.Chat{chats[0], chats[2]} {
		if _, err := f.s.Send(ctx, env, c.ID, f.launch(), Prompt{Text: "work"}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.s.Send(ctx, env, chats[1].ID, f.launch(), Prompt{Text: "hi"})
	if !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("Send with every agent busy = %v; want a conflict", err)
	}
	if f.s.get(chats[0].ID) == nil || f.s.get(chats[2].ID) == nil {
		t.Fatal("a busy agent was stopped")
	}
}

// A queued prompt that can't go stays queued with the reason, and nothing retries it in a
// loop; the user's next message tries again, and the queue goes on after it.
func TestQueuedPromptThatFailsStaysWithItsReason(t *testing.T) {
	release := make(chan struct{})
	var prompts atomic.Int32
	f := newFixture(t, &acptest.Agent{BeforePrompt: func() {
		if prompts.Add(1) == 1 {
			<-release
		}
	}})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	first := f.send(t, "work")
	if _, queued, err := f.s.Enqueue(ctx, env, f.chat.ID, f.launch(), Prompt{Text: "next"}, nil); err != nil || !queued {
		t.Fatalf("Enqueue = %v, %v", queued, err)
	}
	// The sandbox stops: the turn ends, and the agent can't start again.
	f.g.mu.Lock()
	f.g.openErr = errors.New("the sandbox is stopped")
	f.g.mu.Unlock()
	f.g.killAgent()
	close(release)
	f.wait(t, first.ID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		q := f.s.Queued(f.chat.ID)
		if len(q) == 1 && strings.Contains(q[0].Error, "the sandbox is stopped") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued %+v; want the prompt kept with the reason", q)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	f.g.mu.Lock()
	opens := len(f.g.opens)
	f.g.openErr = nil
	f.g.mu.Unlock()
	if opens != 2 {
		t.Fatalf("%d agent starts; want the failed prompt tried once", opens)
	}

	queued, err := f.s.Queue(ctx, env, f.chat.ID, f.launch(), Prompt{Text: "again"}, nil)
	if err != nil || queued {
		t.Fatalf("Queue on an idle chat = %v, %v; want sent", queued, err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for len(f.s.Queued(f.chat.ID)) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	log, err := f.s.Get(ctx, env, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, log.Turns[len(log.Turns)-1].ID)
	log, _ = f.s.Get(ctx, env, f.chat.ID)
	var texts []string
	for _, it := range log.Items {
		if it.User != nil {
			texts = append(texts, it.User.Text)
		}
	}
	if got := strings.Join(texts, "|"); got != "work|again|next" {
		t.Fatalf("prompts %q; want the queued one after the retry", got)
	}
}

// A chat deleted while its agent starts doesn't keep the agent.
func TestDeleteWhileTheAgentStarts(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	c, err := f.s.Store.Chat(ctx, f.sb.EnvironmentID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Store.DeleteChat(ctx, f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.startAgent(ctx, c, f.launch(), false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("startAgent for a deleted chat = %v", err)
	}
	if f.s.get(c.ID) != nil {
		t.Fatal("the deleted chat's agent kept running")
	}
	f.g.mu.Lock()
	defer f.g.mu.Unlock()
	if len(f.g.running) != 0 {
		t.Fatalf("running %v", f.g.running)
	}
}

// Stop while the agent starts stops the message before it goes: no turn, the chat idle again.
func TestStopWhileTheAgentStarts(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	hold := make(chan struct{})
	f.g.openHold = hold
	sent := make(chan error, 1)
	go func() {
		_, err := f.s.Send(ctx, env, f.chat.ID, f.launch(), Prompt{Text: "hi"})
		sent <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.s.mu.Lock()
		_, starting := f.s.stops[f.chat.ID]
		f.s.mu.Unlock()
		if starting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Send never started the agent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := f.s.Cancel(ctx, env, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	close(hold)
	if err := <-sent; !errors.Is(err, ErrStopped) {
		t.Fatalf("Send = %v; want ErrStopped", err)
	}
	log, err := f.s.Get(ctx, env, f.chat.ID)
	if err != nil || len(log.Turns) != 0 || log.Chat.State != store.ChatIdle {
		t.Fatalf("turns %d, state %s, err %v", len(log.Turns), log.Chat.State, err)
	}
	// A stop with nothing starting doesn't stop the next message.
	if err := f.s.Cancel(ctx, env, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, f.send(t, "again").ID)
}

// waitGone waits until the chat's agent is gone.
func waitGone(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.s.get(f.chat.ID) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the agent kept running")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A chat whose agent never answered starts a new session when the agent lost the old one, as
// Claude Code does when a first message is stopped before it wrote its transcript.
func TestUnansweredChatStartsOverWhenTheSessionIsGone(t *testing.T) {
	f := newFixture(t, &acptest.Agent{MissingSessions: true})
	ctx := context.Background()
	if err := f.s.Store.SetChatSession(ctx, f.chat.ID, "ses_gone"); err != nil {
		t.Fatal(err)
	}
	f.wait(t, f.send(t, "hi").ID)
	c, _ := f.s.Store.Chat(ctx, f.sb.EnvironmentID, f.chat.ID)
	if c.State != store.ChatIdle || c.ACPSessionID != "ses_1" {
		t.Fatalf("chat %s with session %q; want idle in a new session", c.State, c.ACPSessionID)
	}
}
