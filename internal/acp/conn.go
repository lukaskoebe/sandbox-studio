// Package acp is Studio's client of the Agent Client Protocol (v1): JSON-RPC 2.0 over
// newline-delimited JSON, here over a stream to the agent's stdin and stdout in a
// sandbox (docs/design/chat-mode.md). It is hand-rolled like the MCP server in
// internal/agentcall: only the methods and updates Studio uses are typed, the rest is
// ignored.
//
// The client advertises no fs or terminal capability, so the agent never asks Studio to
// read or write files or run commands. Agent → client requests other than permission
// requests and form elicitations are answered with "method not found".
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// MaxLine bounds one message from the agent. Longer lines are dropped.
const MaxLine = 8 << 20

const (
	// Close an overloaded stream instead of queueing unbounded agent work.
	maxAgentRequests = 32
	// Agents run over a local guest stream. Even notifications and replies must not
	// hold the chat indefinitely if its peer stops reading.
	writeTimeout = 2 * time.Second
)

// JSON-RPC error codes.
const (
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	// CodeAuthRequired is the agent's "authentication required" error.
	CodeAuthRequired = -32000
)

// ErrAuthRequired means the agent wants a login. Studio never runs ACP authentication:
// providers authenticate through the gateway's placeholders.
var ErrAuthRequired = errors.New("provider needs a login")

// ErrClosed is returned by calls on a closed connection.
var ErrClosed = errors.New("the agent connection is closed")

// Error is a JSON-RPC error the agent answered with.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("agent error %d: %s", e.Code, e.Message)
	if len(e.Data) > 0 && len(e.Data) <= 512 {
		msg += " " + string(e.Data)
	}
	return msg
}

// Handler serves what the agent sends on its own. SessionUpdate runs on the read loop, in
// order, and must not block for long; the requests run in their own goroutines and may
// wait for the user. Their context ends when the connection closes.
type Handler interface {
	SessionUpdate(SessionNotification)
	RequestPermission(context.Context, RequestPermissionRequest) (RequestPermissionResponse, error)
	CreateElicitation(context.Context, ElicitationRequest) (ElicitationResponse, error)
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Conn is a client connection to one agent process.
type Conn struct {
	rwc     io.ReadWriteCloser
	handler Handler
	ctx     context.Context
	cancel  context.CancelFunc

	writeGate chan struct{}
	closeOnce sync.Once
	closeErr  error
	requests  chan struct{}

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan message
	err     error
	done    chan struct{}
}

// NewConn starts reading from rwc. Close it with Close; the connection also ends when the
// agent closes its output.
func NewConn(rwc io.ReadWriteCloser, h Handler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	writeGate := make(chan struct{}, 1)
	writeGate <- struct{}{}
	c := &Conn{
		rwc: rwc, handler: h, ctx: ctx, cancel: cancel,
		writeGate: writeGate, requests: make(chan struct{}, maxAgentRequests),
		pending: map[int64]chan message{}, done: make(chan struct{}),
	}
	go c.read()
	return c
}

// Done is closed when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, once Done is closed.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection; the guest then stops the agent process.
func (c *Conn) Close() error {
	err := c.closeRWC()
	c.shutdown(ErrClosed)
	return err
}

func (c *Conn) closeRWC() error {
	c.closeOnce.Do(func() { c.closeErr = c.rwc.Close() })
	return c.closeErr
}

func (c *Conn) shutdown(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	c.cancel()
	for _, ch := range pending {
		close(ch)
	}
	close(c.done)
}

func (c *Conn) read() {
	br := bufio.NewReaderSize(c.rwc, 64<<10)
	for {
		line, err := readLine(br, MaxLine)
		if errors.Is(err, errLineTooLong) {
			continue
		}
		if err != nil {
			_ = c.closeRWC()
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			c.shutdown(fmt.Errorf("the agent exited: %w", err))
			return
		}
		var m message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			select {
			case c.requests <- struct{}{}:
				go func() {
					defer func() { <-c.requests }()
					c.serve(m)
				}()
			default:
				c.shutdown(errors.New("the agent has too many concurrent requests"))
				_ = c.closeRWC()
				return
			}
		case m.Method != "":
			c.notification(m)
		case len(m.ID) > 0:
			c.response(m)
		}
	}
}

var errLineTooLong = errors.New("line too long")

// readLine reads one line of at most max bytes; a longer one is read to its end and
// dropped.
func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(line)+len(chunk) > max {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			if err != nil {
				return nil, err
			}
			return nil, errLineTooLong
		}
		line = append(line, chunk...)
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

func (c *Conn) notification(m message) {
	if m.Method != MethodSessionUpdate {
		return
	}
	var n SessionNotification
	if json.Unmarshal(m.Params, &n) == nil && n.Update.Kind != "" {
		c.handler.SessionUpdate(n)
	}
}

func (c *Conn) response(m message) {
	id, err := strconv.ParseInt(string(m.ID), 10, 64)
	if err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		ch <- m
	}
}

// serve answers a request from the agent.
func (c *Conn) serve(m message) {
	var result any
	var rerr *Error
	switch m.Method {
	case MethodRequestPermission:
		var req RequestPermissionRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			rerr = &Error{Code: CodeInvalidParams, Message: err.Error()}
			break
		}
		res, err := c.handler.RequestPermission(c.ctx, req)
		if err != nil {
			rerr = &Error{Code: CodeInternal, Message: err.Error()}
			break
		}
		result = res
	case MethodElicitationCreate:
		var req ElicitationRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			rerr = &Error{Code: CodeInvalidParams, Message: err.Error()}
			break
		}
		res, err := c.handler.CreateElicitation(c.ctx, req)
		if err != nil {
			rerr = &Error{Code: CodeInternal, Message: err.Error()}
			break
		}
		result = res
	default:
		// fs/*, terminal/* and anything else: Studio offers none of them.
		rerr = &Error{Code: CodeMethodNotFound, Message: "method not found: " + m.Method}
	}
	out := message{JSONRPC: "2.0", ID: m.ID, Error: rerr}
	if rerr == nil {
		b, err := json.Marshal(result)
		if err != nil {
			out.Error = &Error{Code: CodeInternal, Message: err.Error()}
		} else {
			out.Result = b
		}
	}
	_ = c.write(out)
}

func (c *Conn) write(m message) error {
	err := c.writeContext(c.ctx, m)
	if err != nil {
		// Notify and reply writes have no caller context. If one cannot be sent within
		// the bound, close the stream rather than leave the agent waiting for it.
		c.Close()
	}
	return err
}

func (c *Conn) writeContext(ctx context.Context, m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := c.acquireWrite(writeCtx); err != nil {
		return err
	}
	defer c.releaseWrite()
	if err := writeCtx.Err(); err != nil {
		return err
	}

	line := append(b, '\n')
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(writeCtx, func() {
		defer close(callbackDone)
		c.Close()
	})
	n, writeErr := c.rwc.Write(line)
	if writeErr == nil && n != len(line) {
		writeErr = io.ErrShortWrite
	}
	ctxErr := writeCtx.Err()
	if ctxErr != nil || writeErr != nil {
		// A canceled in-flight write may have emitted a partial JSON line. Any
		// underlying write error has the same framing risk, so retire the stream.
		c.Close()
	}
	stopped := stop()
	if !stopped {
		// Do not release the serialized writer while its cancellation callback can
		// still close the connection under the next call.
		<-callbackDone
	}
	if ctxErr != nil {
		return ctxErr
	}
	if writeErr != nil {
		return writeErr
	}
	if err := writeCtx.Err(); err != nil {
		// Cancellation raced with the end of Write. The bytes may have reached the
		// peer, but the caller must not reuse a connection whose outcome is unclear.
		c.Close()
		return err
	}
	return nil
}

func (c *Conn) acquireWrite(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	case <-c.writeGate:
	}
	if err := ctx.Err(); err != nil {
		c.releaseWrite()
		return err
	}
	select {
	case <-c.done:
		c.releaseWrite()
		return c.Err()
	default:
		return nil
	}
}

func (c *Conn) releaseWrite() { c.writeGate <- struct{}{} }

type pendingCall struct {
	id int64
	ch chan message
}

func (c *Conn) beginCall(ctx context.Context, method string, params any) (pendingCall, error) {
	p, err := json.Marshal(params)
	if err != nil {
		return pendingCall{}, err
	}
	ch := make(chan message, 1)
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return pendingCall{}, ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.writeContext(ctx, message{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: p}); err != nil {
		c.forgetCall(id)
		return pendingCall{}, err
	}
	return pendingCall{id: id, ch: ch}, nil
}

func (c *Conn) forgetCall(id int64) {
	c.mu.Lock()
	if c.pending != nil {
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *Conn) waitCall(ctx context.Context, call pendingCall) (message, error) {
	select {
	case m, ok := <-call.ch:
		if !ok {
			return message{}, c.Err()
		}
		if m.Error != nil {
			if m.Error.Code == CodeAuthRequired {
				return message{}, ErrAuthRequired
			}
			return message{}, m.Error
		}
		return m, nil
	case <-ctx.Done():
		c.forgetCall(call.id)
		return message{}, ctx.Err()
	}
}

// Call sends a request and decodes its result into result (unless nil). A JSON-RPC error
// is returned as *Error, an authentication error as ErrAuthRequired.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	call, err := c.beginCall(ctx, method, params)
	if err != nil {
		return err
	}
	m, err := c.waitCall(ctx, call)
	if err != nil || result == nil || len(m.Result) == 0 {
		return err
	}
	return json.Unmarshal(m.Result, result)
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: p})
}

// --- typed calls ----------------------------------------------------------------------

// Initialize negotiates protocol version 1 and offers form elicitation only.
func (c *Conn) Initialize(ctx context.Context, client Implementation) (InitializeResponse, error) {
	var out InitializeResponse
	err := c.Call(ctx, MethodInitialize, InitializeRequest{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: ClientCapabilities{Elicitation: &ElicitationCapabilities{Form: &struct{}{}}},
		ClientInfo:         &client,
	}, &out)
	if err == nil && out.ProtocolVersion != ProtocolVersion {
		err = fmt.Errorf("the agent speaks ACP version %d, Studio speaks %d", out.ProtocolVersion, ProtocolVersion)
	}
	return out, err
}

// NewSession creates a session in cwd.
func (c *Conn) NewSession(ctx context.Context, cwd string, servers []MCPServer) (string, error) {
	var out NewSessionResponse
	err := c.Call(ctx, MethodSessionNew, NewSessionRequest{Cwd: cwd, MCPServers: nonNil(servers)}, &out)
	if err == nil && out.SessionID == "" {
		err = errors.New("the agent returned no session ID")
	}
	return out.SessionID, err
}

// ResumeSession reattaches to a session without replaying it.
func (c *Conn) ResumeSession(ctx context.Context, id, cwd string, servers []MCPServer) error {
	return c.Call(ctx, MethodSessionResume, LoadSessionRequest{SessionID: id, Cwd: cwd, MCPServers: nonNil(servers)}, nil)
}

// LoadSession reattaches to a session; the agent replays it as session updates before
// it answers.
func (c *Conn) LoadSession(ctx context.Context, id, cwd string, servers []MCPServer) error {
	return c.Call(ctx, MethodSessionLoad, LoadSessionRequest{SessionID: id, Cwd: cwd, MCPServers: nonNil(servers)}, nil)
}

// PromptResult is the completed response to a started prompt.
type PromptResult struct {
	Response PromptResponse
	Err      error
}

// StartPrompt sends a prompt and returns after its request has been written. The result
// channel receives the agent's response when the turn ends.
func (c *Conn) StartPrompt(ctx context.Context, id string, prompt []ContentBlock) (<-chan PromptResult, error) {
	call, err := c.beginCall(ctx, MethodSessionPrompt, PromptRequest{SessionID: id, Prompt: prompt})
	if err != nil {
		return nil, err
	}
	done := make(chan PromptResult, 1)
	go func() {
		m, err := c.waitCall(ctx, call)
		var response PromptResponse
		if err == nil && len(m.Result) > 0 {
			err = json.Unmarshal(m.Result, &response)
		}
		done <- PromptResult{Response: response, Err: err}
		close(done)
	}()
	return done, nil
}

// Prompt runs one turn and returns its stop reason.
func (c *Conn) Prompt(ctx context.Context, id string, prompt []ContentBlock) (PromptResponse, error) {
	done, err := c.StartPrompt(ctx, id, prompt)
	if err != nil {
		return PromptResponse{}, err
	}
	result := <-done
	return result.Response, result.Err
}

// Cancel asks the agent to end the running turn.
func (c *Conn) Cancel(id string) error {
	return c.Notify(MethodSessionCancel, SessionRequest{SessionID: id})
}

// CloseSession ends a session in the agent, so another process may write it.
func (c *Conn) CloseSession(ctx context.Context, id string) error {
	return c.Call(ctx, MethodSessionClose, SessionRequest{SessionID: id}, nil)
}

func nonNil(s []MCPServer) []MCPServer {
	if s == nil {
		return []MCPServer{}
	}
	return s
}
