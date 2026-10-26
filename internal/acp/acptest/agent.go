// Package acptest is a scripted ACP agent for tests. It speaks the agent side of ACP v1
// on its own, independent of the client in package acp, so tests are real round trips.
package acptest

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// Step is one thing the agent does during a turn. Exactly one field is set.
type Step struct {
	// Update is sent as a session/update notification (the `update` object).
	Update json.RawMessage
	// Permission is sent as session/request_permission params (without sessionId); the
	// answer is recorded in Agent.Outcomes and the turn waits for it.
	Permission json.RawMessage
	// Elicit is sent as elicitation/create params; the answer goes to Agent.Answers.
	Elicit json.RawMessage
	// WaitCancel blocks the turn until session/cancel arrives; the turn then ends with
	// stopReason cancelled.
	WaitCancel bool
	// Hang blocks the turn forever, ignoring cancellation.
	Hang bool
	// Pause waits that long before the next step, as an agent thinking before it answers.
	Pause time.Duration
}

// Turn is the script of one prompt.
type Turn struct {
	Steps []Step
	Stop  string          // defaults to end_turn
	Usage json.RawMessage // optional PromptResponse usage
}

// Agent is the scripted agent. Configure it before Serve.
type Agent struct {
	// Init is the initialize result; DefaultInit if nil.
	Init json.RawMessage
	// AuthRequired answers session/new with the authentication error.
	AuthRequired bool
	// SessionID is what session/new returns.
	SessionID string
	// Turns are played in order, one per prompt; prompts beyond them end at once.
	Turns []Turn
	// History is replayed as updates by session/load.
	History []json.RawMessage
	// MissingSessions makes session/resume and session/load fail as unknown sessions.
	MissingSessions bool
	// BeforePrompt runs in the prompt handler after session/prompt is received. Tests can
	// hold it while Serve continues reading later messages from the wire.
	BeforePrompt func()

	mu           sync.Mutex
	Calls        []string          // methods called, in order, with notifications
	Prompts      []json.RawMessage // prompt arrays received
	Initial      json.RawMessage   // the initialize params received
	NewReq       json.RawMessage   // the session/new params received
	Outcomes     []json.RawMessage // permission answers
	Answers      []json.RawMessage // elicitation answers
	callsChanged chan struct{}

	cancel chan struct{}
	turn   int
	w      *bufio.Writer
	wmu    sync.Mutex
	nextID int
	waits  map[int]chan json.RawMessage
}

// DefaultInit advertises what the three pinned adapters advertise.
var DefaultInit = json.RawMessage(`{"protocolVersion":1,"agentCapabilities":{"loadSession":true,` +
	`"promptCapabilities":{"image":true,"embeddedContext":true},"mcpCapabilities":{"http":true},` +
	`"sessionCapabilities":{"resume":{},"close":{},"list":{},"fork":{}}},"authMethods":[],"agentInfo":{"name":"fake","version":"1"}}`)

// Pipe returns the client end of a connection served by a; the agent stops when it is
// closed.
func (a *Agent) Pipe() net.Conn {
	client, server := net.Pipe()
	go a.Serve(server)
	return client
}

// Serve speaks ACP on rw until it closes.
func (a *Agent) Serve(rw io.ReadWriteCloser) {
	defer rw.Close()
	a.mu.Lock()
	a.w = bufio.NewWriter(rw)
	a.cancel = make(chan struct{}, 1)
	a.waits = map[int]chan json.RawMessage{}
	a.callsChanged = make(chan struct{})
	a.mu.Unlock()
	br := bufio.NewReader(rw)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			a.mu.Lock()
			for _, ch := range a.waits {
				close(ch)
			}
			a.waits = map[int]chan json.RawMessage{}
			a.mu.Unlock()
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Method == "" {
			id, _ := strconv.Atoi(string(m.ID))
			a.mu.Lock()
			ch := a.waits[id]
			delete(a.waits, id)
			a.mu.Unlock()
			if ch != nil {
				if len(m.Error) > 0 {
					ch <- m.Error
				} else {
					ch <- m.Result
				}
			}
			continue
		}
		if len(m.ID) == 0 {
			if m.Method == "session/cancel" {
				select {
				case a.cancel <- struct{}{}:
				default:
				}
			}
			a.recordCall(m.Method)
			continue
		}
		a.recordCall(m.Method)
		if m.Method == "session/prompt" {
			// Discard only cancels that arrived before this prompt on the wire. Draining
			// in call() can lose a later cancel when its goroutine starts late.
			select {
			case <-a.cancel:
			default:
			}
		}
		go a.call(m.ID, m.Method, m.Params)
	}
}

func (a *Agent) recordCall(method string) {
	a.mu.Lock()
	a.Calls = append(a.Calls, method)
	close(a.callsChanged)
	a.callsChanged = make(chan struct{})
	a.mu.Unlock()
}

// Called reports the methods called so far.
func (a *Agent) Called() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.Calls...)
}

// WaitForCall waits until Serve has received method from the client.
func (a *Agent) WaitForCall(ctx context.Context, method string) error {
	for {
		a.mu.Lock()
		for _, called := range a.Calls {
			if called == method {
				a.mu.Unlock()
				return nil
			}
		}
		changed := a.callsChanged
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// Snapshot returns copies of what the agent recorded.
func (a *Agent) Snapshot() (prompts, outcomes, answers []json.RawMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]json.RawMessage(nil), a.Prompts...), append([]json.RawMessage(nil), a.Outcomes...), append([]json.RawMessage(nil), a.Answers...)
}

func (a *Agent) call(id json.RawMessage, method string, params json.RawMessage) {
	if method == "session/prompt" && a.BeforePrompt != nil {
		a.BeforePrompt()
	}
	var p struct {
		SessionID string          `json:"sessionId"`
		Prompt    json.RawMessage `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p) // only the session methods have these; others may have no params
	switch method {
	case "initialize":
		a.mu.Lock()
		a.Initial = params
		a.mu.Unlock()
		init := a.Init
		if init == nil {
			init = DefaultInit
		}
		a.reply(id, init, nil)
	case "session/new":
		a.mu.Lock()
		a.NewReq = params
		a.mu.Unlock()
		if a.AuthRequired {
			a.reply(id, nil, &rpcError{Code: -32000, Message: "Authentication required"})
			return
		}
		a.reply(id, map[string]string{"sessionId": a.SessionID}, nil)
	case "session/resume", "session/load":
		if a.MissingSessions || p.SessionID != a.SessionID {
			a.reply(id, nil, &rpcError{Code: -32002, Message: "Resource not found: session " + p.SessionID})
			return
		}
		if method == "session/load" {
			for _, u := range a.History {
				a.update(p.SessionID, u)
			}
		}
		a.reply(id, struct{}{}, nil)
	case "session/close":
		a.reply(id, struct{}{}, nil)
	case "session/prompt":
		a.mu.Lock()
		a.Prompts = append(a.Prompts, p.Prompt)
		n := a.turn
		a.turn++
		a.mu.Unlock()
		var t Turn
		if n < len(a.Turns) {
			t = a.Turns[n]
		}
		stop := t.Stop
		if stop == "" {
			stop = "end_turn"
		}
		for _, s := range t.Steps {
			switch {
			case s.Update != nil:
				a.update(p.SessionID, s.Update)
			case s.Permission != nil:
				var req map[string]json.RawMessage
				if err := json.Unmarshal(s.Permission, &req); err != nil || req == nil {
					panic("acptest: Step.Permission is not a JSON object")
				}
				req["sessionId"], _ = json.Marshal(p.SessionID)
				out, ok := a.request("session/request_permission", req)
				if !ok {
					return
				}
				a.mu.Lock()
				a.Outcomes = append(a.Outcomes, out)
				a.mu.Unlock()
			case s.Elicit != nil:
				out, ok := a.request("elicitation/create", s.Elicit)
				if !ok {
					return
				}
				a.mu.Lock()
				a.Answers = append(a.Answers, out)
				a.mu.Unlock()
			case s.WaitCancel:
				<-a.cancel
				stop = "cancelled"
			case s.Hang:
				select {}
			case s.Pause > 0:
				time.Sleep(s.Pause)
			}
			if stop == "cancelled" {
				break
			}
		}
		res := map[string]any{"stopReason": stop}
		if t.Usage != nil {
			res["usage"] = t.Usage
		}
		a.reply(id, res, nil)
	default:
		a.reply(id, nil, &rpcError{Code: -32601, Message: "method not found"})
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (a *Agent) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	a.wmu.Lock()
	defer a.wmu.Unlock()
	if _, err := a.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return a.w.Flush()
}

func (a *Agent) reply(id json.RawMessage, result any, e *rpcError) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		m["error"] = e
	} else {
		m["result"] = result
	}
	_ = a.send(m) // a client that has gone away gets no reply
}

func (a *Agent) update(session string, u json.RawMessage) {
	_ = a.send(map[string]any{"jsonrpc": "2.0", "method": "session/update",
		"params": map[string]any{"sessionId": session, "update": u}})
}

// request sends a request to the client and waits for its result.
func (a *Agent) request(method string, params any) (json.RawMessage, bool) {
	ch := make(chan json.RawMessage, 1)
	a.mu.Lock()
	a.nextID++
	id := a.nextID
	if a.waits == nil {
		a.mu.Unlock()
		return nil, false
	}
	a.waits[id] = ch
	a.mu.Unlock()
	if err := a.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, false
	}
	out, ok := <-ch
	return out, ok
}
