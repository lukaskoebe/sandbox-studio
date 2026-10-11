package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// chatGuest serves chats' ACP streams with a scripted agent.
type chatGuest struct {
	fakeGuest
	agent *acptest.Agent
	acp   []agentproto.StartACP
}

func (g *chatGuest) OpenACP(_ context.Context, _ string, req agentproto.StartACP) (io.ReadWriteCloser, error) {
	g.mu.Lock()
	g.acp = append(g.acp, req)
	g.mu.Unlock()
	return g.agent.Pipe(), nil
}

func newChatServer(t *testing.T, agent *acptest.Agent) (http.Handler, *Server, *chatGuest, store.Environment) {
	t.Helper()
	h, s := newTestServer(t)
	g := &chatGuest{agent: agent}
	s.Chats = &chat.Service{Store: s.Store, Guest: g, Bus: s.Bus}
	t.Cleanup(s.Chats.Close)
	return h, s, g, newEnvironment(t, s, "work")
}

func newChatSandbox(t *testing.T, h http.Handler, s *Server, env store.Environment, providerID, harnessName string) store.Sandbox {
	t.Helper()
	rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/teammates",
		`{"name":"Ada `+harnessName+`","soul":"Careful.","harness":"`+harnessName+`","providerId":"`+providerID+`","model":"claude-sonnet-4-5"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("teammate: %d %s", rec.Code, rec.Body)
	}
	tm := decode[store.Teammate](t, rec)
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "dev-" + harnessName, CPUs: 1, TeammateID: tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

// waitIdle waits until the chat's last turn ended.
func waitIdle(t *testing.T, h http.Handler, base string) chat.Log {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		log := decode[chat.Log](t, do(h, "GET", base, ""))
		if log.Chat.State == store.ChatIdle && len(log.Turns) > 0 && log.Turns[len(log.Turns)-1].EndedAt != nil {
			return log
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the turn never ended")
	return chat.Log{}
}

func TestChats(t *testing.T) {
	agent := &acptest.Agent{SessionID: "ses_1", Turns: []acptest.Turn{
		{Steps: []acptest.Step{{Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}`)}}},
	}}
	h, s, g, env := newChatServer(t, agent)
	prov := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	sb := newChatSandbox(t, h, s, env, prov.ID, "claude")
	envBase := testOrigin + "/api/environments/" + env.ID

	rec := do(h, "POST", envBase+"/sandboxes/"+sb.ID+"/chats", `{"text":"Say hello","attachments":[{"name":"notes.txt","data":"aGk="}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	c := decode[store.Chat](t, rec)
	base := envBase + "/chats/" + c.ID
	log := waitIdle(t, h, base)
	if log.Chat.Harness != "claude" || log.Chat.Title != "Say hello" || len(log.Items) != 2 ||
		log.Items[0].User.Attachments[0].Name != "notes.txt" || log.Items[1].Text.Text != "Hello" {
		t.Fatalf("log: %+v", log)
	}

	// The agent got the placeholder, never the key, and its ACP command.
	sent, _ := json.Marshal(struct {
		F []agentproto.HomeFile
		A []agentproto.StartACP
	}{g.files, g.acp})
	if strings.Contains(string(sent), providerKey) {
		t.Fatalf("the key reached the guest: %s", sent)
	}
	if len(g.acp) != 1 || g.acp[0].Env["ANTHROPIC_API_KEY"] != prov.Placeholder || g.acp[0].Chat != c.ID ||
		strings.Join(g.acp[0].Command, " ") != strings.Join(harness.Claude{}.ACPCommand(), " ") {
		t.Fatalf("acp: %+v", g.acp)
	}

	if list := decode[[]store.Chat](t, do(h, "GET", envBase+"/chats", "")); len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("list: %+v", list)
	}
	if list := decode[[]store.Chat](t, do(h, "GET", envBase+"/sandboxes/"+sb.ID+"/chats", "")); len(list) != 1 {
		t.Fatalf("sandbox list: %+v", list)
	}
	if rec := do(h, "POST", base+"/prompt", `{"text":"  "}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty prompt: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", base+"/prompt", `{"text":"Again"}`)
	if rec.Code != http.StatusAccepted || decode[chat.Turn](t, rec).Seq != 2 {
		t.Fatalf("prompt: %d %s", rec.Code, rec.Body)
	}
	waitIdle(t, h, base)
	if rec := do(h, "POST", base+"/cancel", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", base, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", base, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted: %d", rec.Code)
	}
}

// Claude subscriptions can't drive the Agent SDK: chats refuse them with a reason.
func TestChatsRefuseClaudeSubscription(t *testing.T) {
	h, s, _, env := newChatServer(t, &acptest.Agent{})
	prov, err := s.Store.CreateProvider(context.Background(), store.Provider{ID: store.NewID(), EnvironmentID: env.ID, Name: "Max", Kind: "claude_subscription"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sb := newChatSandbox(t, h, s, env, prov.ID, "claude")
	rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes/"+sb.ID+"/chats", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Claude subscription") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
}

type sseEvent struct {
	event string
	id    int64
	data  string
}

// readEvents reads SSE events from r into a channel until it closes.
func readEvents(r io.Reader) <-chan sseEvent {
	ch := make(chan sseEvent, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(nil, 1<<20)
		var e sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if e.event != "" {
					ch <- e
				}
				e = sseEvent{}
			case strings.HasPrefix(line, "event: "):
				e.event = line[7:]
			case strings.HasPrefix(line, "id: "):
				e.id, _ = strconv.ParseInt(line[4:], 10, 64)
			case strings.HasPrefix(line, "data: "):
				e.data = line[6:]
			}
		}
	}()
	return ch
}

func next(t *testing.T, ch <-chan sseEvent, event string, match func(string) bool) sseEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatalf("the stream ended waiting for %s", event)
			}
			if e.event == event && match(e.data) {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event", event)
		}
	}
}

func TestChatStream(t *testing.T) {
	agent := &acptest.Agent{SessionID: "ses_1", Turns: []acptest.Turn{
		{Steps: []acptest.Step{{Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"One"}}`)}}},
		{Steps: []acptest.Step{{Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Two"}}`)}}},
	}}
	h, s, _, env := newChatServer(t, agent)
	prov := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	sb := newChatSandbox(t, h, s, env, prov.ID, "claude")
	srv := httptest.NewServer(h)
	defer srv.Close()
	envBase := testOrigin + "/api/environments/" + env.ID
	c := decode[store.Chat](t, do(h, "POST", envBase+"/sandboxes/"+sb.ID+"/chats", `{"text":"First"}`))
	base := envBase + "/chats/" + c.ID
	waitIdle(t, h, base)

	open := func(header ...string) (<-chan sseEvent, func()) {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/environments/"+env.ID+"/chats/"+c.ID+"/stream", nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("stream: %v %v", resp, err)
		}
		return readEvents(resp.Body), func() { cancel(); resp.Body.Close() }
	}

	// From the start: the first turn's items, then the chat.
	ch, stop := open()
	last := next(t, ch, "item", func(d string) bool { return strings.Contains(d, `"One"`) })
	chatEvent := next(t, ch, "chat", func(d string) bool { return strings.Contains(d, `"state":"idle"`) })
	if chatEvent.id < last.id {
		t.Fatalf("chat rev %d before item rev %d", chatEvent.id, last.id)
	}
	stop()

	// Resuming after the last ID sends only what changed since: the second turn.
	ch, stop = open("Last-Event-ID", strconv.FormatInt(chatEvent.id, 10))
	defer stop()
	next(t, ch, "chat", func(string) bool { return true })
	if rec := do(h, "POST", base+"/prompt", `{"text":"Second"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("prompt: %d %s", rec.Code, rec.Body)
	}
	var seen []string
	for {
		e := next(t, ch, "item", func(string) bool { return true })
		if e.id <= chatEvent.id {
			t.Fatalf("resent %s %d", e.data, e.id)
		}
		seen = append(seen, e.data)
		if strings.Contains(e.data, `"Two"`) {
			break
		}
	}
	if !strings.Contains(seen[0], `"Second"`) {
		t.Fatalf("items: %v", seen)
	}

	// Deleting the chat ends its stream.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()
	if rec := do(h, "DELETE", base, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	wg.Wait()
}
