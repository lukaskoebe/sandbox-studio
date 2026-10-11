package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
)

type recorder struct {
	mu      sync.Mutex
	updates []acp.SessionUpdate
	perm    func(acp.RequestPermissionRequest) acp.RequestPermissionResponse
	elicit  func(acp.ElicitationRequest) acp.ElicitationResponse
}

func (r *recorder) SessionUpdate(n acp.SessionNotification) {
	r.mu.Lock()
	r.updates = append(r.updates, n.Update)
	r.mu.Unlock()
}

func (r *recorder) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if r.perm == nil {
		<-ctx.Done()
		return acp.Cancelled(), nil
	}
	return r.perm(req), nil
}

func (r *recorder) CreateElicitation(_ context.Context, req acp.ElicitationRequest) (acp.ElicitationResponse, error) {
	return r.elicit(req), nil
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, u := range r.updates {
		out = append(out, u.Kind)
	}
	return out
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestRoundTrip(t *testing.T) {
	agent := &acptest.Agent{SessionID: "s1", Turns: []acptest.Turn{{
		Steps: []acptest.Step{
			{Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}`)},
			{Update: json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Edit a.go","kind":"edit","status":"pending"}`)},
			{Permission: json.RawMessage(`{"toolCall":{"toolCallId":"t1"},"options":[{"optionId":"yes","name":"Allow","kind":"allow_once"},{"optionId":"no","name":"Reject","kind":"reject_once"}]}`)},
			{Elicit: json.RawMessage(`{"mode":"form","message":"Which branch?","requestedSchema":{"type":"object"}}`)},
			{Update: json.RawMessage(`{"sessionUpdate":"brand_new_kind","whatever":1}`)},
		},
	}}}
	rec := &recorder{
		perm: func(req acp.RequestPermissionRequest) acp.RequestPermissionResponse {
			if req.ToolCall.ToolCallID != "t1" || len(req.Options) != 2 {
				t.Errorf("permission request = %+v", req)
			}
			return acp.Selected("yes")
		},
		elicit: func(req acp.ElicitationRequest) acp.ElicitationResponse {
			return acp.ElicitationResponse{Action: acp.ElicitAccept, Content: json.RawMessage(`{"branch":"main"}`)}
		},
	}
	c := acp.NewConn(agent.Pipe(), rec)
	defer c.Close()

	init, err := c.Initialize(ctx(t), acp.Implementation{Name: "studio"})
	if err != nil {
		t.Fatal(err)
	}
	if !init.AgentCapabilities.CanResume() || !init.AgentCapabilities.LoadSession || !init.AgentCapabilities.PromptCapabilities.Image {
		t.Errorf("capabilities = %+v", init.AgentCapabilities)
	}
	// Studio offers form elicitation and nothing else: no fs, no terminal, no auth.
	var offered map[string]any
	json.Unmarshal(agent.Initial, &offered)
	caps, _ := json.Marshal(offered["clientCapabilities"])
	if string(caps) != `{"elicitation":{"form":{}}}` {
		t.Errorf("client capabilities = %s", caps)
	}

	id, err := c.NewSession(ctx(t), "/workspace", nil)
	if err != nil || id != "s1" {
		t.Fatalf("new session = %q, %v", id, err)
	}
	if !strings.Contains(string(agent.NewReq), `"mcpServers":[]`) {
		t.Errorf("session/new params = %s", agent.NewReq)
	}
	res, err := c.Prompt(ctx(t), id, []acp.ContentBlock{{Type: "text", Text: "hi"}})
	if err != nil || res.StopReason != acp.StopEndTurn {
		t.Fatalf("prompt = %+v, %v", res, err)
	}
	if got := strings.Join(rec.kinds(), ","); got != "agent_message_chunk,tool_call,brand_new_kind" {
		t.Errorf("updates = %s", got)
	}
	_, outcomes, answers := agent.Snapshot()
	if len(outcomes) != 1 || string(outcomes[0]) != `{"outcome":{"outcome":"selected","optionId":"yes"}}` {
		t.Errorf("outcomes = %s", outcomes)
	}
	if len(answers) != 1 || string(answers[0]) != `{"action":"accept","content":{"branch":"main"}}` {
		t.Errorf("answers = %s", answers)
	}
}

func TestCancel(t *testing.T) {
	agent := &acptest.Agent{SessionID: "s1", Turns: []acptest.Turn{{Steps: []acptest.Step{{WaitCancel: true}}}}}
	c := acp.NewConn(agent.Pipe(), &recorder{})
	defer c.Close()
	done := make(chan acp.PromptResponse)
	go func() {
		res, err := c.Prompt(ctx(t), "s1", []acp.ContentBlock{{Type: "text", Text: "go"}})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	for !contains(agent.Called(), "session/prompt") {
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.Cancel("s1"); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.StopReason != acp.StopCancelled {
		t.Errorf("stop = %q", res.StopReason)
	}
}

func TestAuthRequired(t *testing.T) {
	agent := &acptest.Agent{SessionID: "s1", AuthRequired: true}
	c := acp.NewConn(agent.Pipe(), &recorder{})
	defer c.Close()
	if _, err := c.NewSession(ctx(t), "/workspace", nil); !errors.Is(err, acp.ErrAuthRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingSession(t *testing.T) {
	agent := &acptest.Agent{SessionID: "s1"}
	c := acp.NewConn(agent.Pipe(), &recorder{})
	defer c.Close()
	var rpcErr *acp.Error
	if err := c.ResumeSession(ctx(t), "gone", "/workspace", nil); !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadReplays(t *testing.T) {
	agent := &acptest.Agent{SessionID: "s1", History: []json.RawMessage{
		json.RawMessage(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}`),
		json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}`),
	}}
	rec := &recorder{}
	c := acp.NewConn(agent.Pipe(), rec)
	defer c.Close()
	if err := c.LoadSession(ctx(t), "s1", "/workspace", nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.kinds(), ","); got != "user_message_chunk,agent_message_chunk" {
		t.Errorf("updates = %s", got)
	}
}

// The agent's requests for files or a terminal are refused: Studio advertises neither.
func TestRefusesFSAndTerminal(t *testing.T) {
	client, server := pipe()
	c := acp.NewConn(client, &recorder{})
	defer c.Close()
	for _, method := range []string{"fs/read_text_file", "fs/write_text_file", "terminal/create"} {
		server.send(t, `{"jsonrpc":"2.0","id":7,"method":"`+method+`","params":{"path":"/etc/passwd"}}`)
		line := server.recv(t)
		if !strings.Contains(line, `"code":-32601`) || !strings.Contains(line, `"id":7`) {
			t.Errorf("%s: reply = %s", method, line)
		}
	}
}

func TestAgentExitEndsCalls(t *testing.T) {
	client, server := pipe()
	c := acp.NewConn(client, &recorder{})
	errc := make(chan error, 1)
	go func() { _, err := c.NewSession(context.Background(), "/workspace", nil); errc <- err }()
	server.recv(t)
	server.conn.Close()
	if err := <-errc; err == nil {
		t.Fatal("call survived the agent's exit")
	}
	<-c.Done()
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
