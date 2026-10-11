package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

func TestCallWriteCancellationClosesConnection(t *testing.T) {
	rwc := newBlockedWriteRWC()
	c := acp.NewConn(rwc, &recorder{})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Call(ctx, "initialize", struct{}{}, nil) }()
	waitWriteStarted(t, rwc)

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("call stayed in Write past its context deadline")
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("canceled in-flight write did not close the connection")
	}
}

func TestSuccessfulWriteCancellationCallbackDoesNotCloseNextCall(t *testing.T) {
	rwc := newCountingWriteRWC()
	c := acp.NewConn(rwc, &recorder{})
	defer c.Close()

	for wantWrites := int32(1); wantWrites <= 2; wantWrites++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := c.Call(ctx, "initialize", struct{}{}, nil)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call %d error = %v, want response-wait deadline", wantWrites, err)
		}
		if got := rwc.writes.Load(); got != wantWrites {
			t.Fatalf("after call %d, writes = %d", wantWrites, got)
		}
		select {
		case <-rwc.closed:
			t.Fatalf("call %d's finished write callback closed the connection", wantWrites)
		default:
		}
	}
}

func TestCallWriteCancellationWaitingForSerialization(t *testing.T) {
	rwc := newBlockedWriteRWC()
	c := acp.NewConn(rwc, &recorder{})
	defer c.Close()
	firstDone := make(chan error, 1)
	go func() { firstDone <- c.Notify("blocked", nil) }()
	waitWriteStarted(t, rwc)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	callDone := make(chan error, 1)
	go func() { callDone <- c.Call(ctx, "initialize", struct{}{}, nil) }()
	select {
	case err := <-callDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued call error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("call stayed queued behind the writer past its context deadline")
	}
	select {
	case <-c.Done():
		t.Fatal("canceling a call before its write began closed the connection")
	default:
	}

	c.Close()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("closing the stream did not unblock the active writer")
	}
}

func TestContextlessNotifyWriteHasTimeout(t *testing.T) {
	rwc := newBlockedWriteRWC()
	c := acp.NewConn(rwc, &recorder{})
	defer c.Close()
	done := make(chan error, 1)
	go func() { done <- c.Notify(acp.MethodSessionCancel, acp.SessionRequest{SessionID: "s1"}) }()
	waitWriteStarted(t, rwc)

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("notify error = %v, want write timeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("contextless notify stayed blocked past the write timeout")
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("timed-out notify did not close the connection")
	}
}

func TestAgentReplyWriteHasTimeout(t *testing.T) {
	client, server := pipe()
	c := acp.NewConn(client, &recorder{})
	defer c.Close()

	if _, err := server.conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"unsupported"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("agent reply stayed blocked past the write timeout")
	}
}

func TestInboundRequestsAreBoundedAndCanceledOnOverload(t *testing.T) {
	client, server := pipe()
	h := &blockedRequestHandler{
		entered: make(chan struct{}, 32),
		stopped: make(chan struct{}, 32),
	}
	c := acp.NewConn(client, h)
	defer c.Close()

	for id := 1; id <= 32; id++ {
		if _, err := server.conn.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"session/request_permission","params":{}}` + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 32; i++ {
		select {
		case <-h.entered:
		case <-time.After(time.Second):
			t.Fatal("an in-flight handler did not start")
		}
	}
	go func() {
		_, _ = server.conn.Write([]byte(`{"jsonrpc":"2.0","id":33,"method":"session/request_permission","params":{}}` + "\n"))
	}()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("the connection did not close after the request limit was exceeded")
	}
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "too many concurrent requests") {
		t.Fatalf("connection error = %v, want concurrent request limit", err)
	}
	for i := 0; i < 32; i++ {
		select {
		case <-h.stopped:
		case <-time.After(time.Second):
			t.Fatal("an in-flight request handler did not observe connection cancellation")
		}
	}
	if got := h.active.Load(); got != 0 {
		t.Fatalf("active request handlers = %d, want 0", got)
	}
	if got := h.maxActive.Load(); got != 32 {
		t.Fatalf("maximum active request handlers = %d, want 32", got)
	}
}

type blockedWriteRWC struct {
	writeStarted chan struct{}
	closed       chan struct{}
	writeOnce    sync.Once
	closeOnce    sync.Once
}

type countingWriteRWC struct {
	writes atomic.Int32
	closed chan struct{}
	once   sync.Once
}

func newCountingWriteRWC() *countingWriteRWC {
	return &countingWriteRWC{closed: make(chan struct{})}
}

func (c *countingWriteRWC) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *countingWriteRWC) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return len(p), nil
}

func (c *countingWriteRWC) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func newBlockedWriteRWC() *blockedWriteRWC {
	return &blockedWriteRWC{writeStarted: make(chan struct{}), closed: make(chan struct{})}
}

func waitWriteStarted(t *testing.T, rwc *blockedWriteRWC) {
	t.Helper()
	select {
	case <-rwc.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("the fake writer was not entered")
	}
}

func (c *blockedWriteRWC) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *blockedWriteRWC) Write([]byte) (int, error) {
	c.writeOnce.Do(func() { close(c.writeStarted) })
	<-c.closed
	return 0, io.ErrClosedPipe
}

func (c *blockedWriteRWC) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

type blockedRequestHandler struct {
	active    atomic.Int32
	maxActive atomic.Int32
	entered   chan struct{}
	stopped   chan struct{}
}

func (h *blockedRequestHandler) SessionUpdate(acp.SessionNotification) {}

func (h *blockedRequestHandler) RequestPermission(ctx context.Context, _ acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	current := h.active.Add(1)
	for max := h.maxActive.Load(); current > max && !h.maxActive.CompareAndSwap(max, current); max = h.maxActive.Load() {
	}
	h.entered <- struct{}{}
	<-ctx.Done()
	h.active.Add(-1)
	h.stopped <- struct{}{}
	return acp.Cancelled(), nil
}

func (h *blockedRequestHandler) CreateElicitation(ctx context.Context, _ acp.ElicitationRequest) (acp.ElicitationResponse, error) {
	<-ctx.Done()
	return acp.ElicitationResponse{}, ctx.Err()
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
