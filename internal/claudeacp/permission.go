package claudeacp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"slices"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// The permission path. Claude Code asks its --permission-prompt-tool before a tool call it
// doesn't approve itself. That tool belongs to the plugin's permissions server, which runs
// `studio-agent claude-permission`: it sends the question over the session's socket to the
// shim, which asks Studio with session/request_permission and answers with Claude Code's
// allow or deny. Studio offers allow once and deny; "don't ask again" needs a rule Claude
// Code would keep, which the shim doesn't write yet.

// permissionRequest is one question Claude Code asks: a tool call and its input.
type permissionRequest struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// permissionAnswer is Claude Code's answer: allow with the input, or deny with a message.
type permissionAnswer struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// The options of a permission request, as Studio shows them.
const (
	optionAllow = "allow"
	optionDeny  = "deny"
)

// mcpVersions are the protocol revisions the permissions server speaks.
var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

func denied(message string) permissionAnswer {
	return permissionAnswer{Behavior: "deny", Message: message}
}

// acceptPermissions answers the permissions servers that connect to the socket.
func (s *session) acceptPermissions(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.answerPermission(conn)
	}
}

// answerPermission reads one question from conn and writes its answer. The answer waits for
// Studio's user, as long as the turn runs.
func (s *session) answerPermission(conn net.Conn) {
	defer conn.Close()
	var req permissionRequest
	if err := json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&req); err != nil {
		return
	}
	_ = json.NewEncoder(conn).Encode(s.decide(req)) // a caller that has gone needs no answer
}

// decide asks Studio about a permission request of the running turn. Questions are refused:
// Studio's chats don't take answers yet. Requests outside a turn are refused too.
func (s *session) decide(req permissionRequest) permissionAnswer {
	if req.ToolName == "AskUserQuestion" {
		return denied("Studio's chats can't take answers to questions yet. Ask in plain text and wait for the reply.")
	}
	s.mu.Lock()
	running := s.turn != nil
	s.mu.Unlock()
	if !running {
		return denied("No request of yours is running, so Studio has nothing to ask.")
	}
	input := req.Input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	title, kind, locations := toolCall(req.ToolName, input)
	raw, err := s.a.call(s.a.ctx, acp.MethodRequestPermission, acp.RequestPermissionRequest{
		SessionID: s.id,
		ToolCall: acp.ToolCallUpdate{ToolCallID: req.ToolUseID, Title: title, Kind: kind,
			Status: "pending", Locations: locations, RawInput: input},
		Options: []acp.PermissionOption{
			{OptionID: optionAllow, Name: "Allow", Kind: acp.AllowOnce},
			{OptionID: optionDeny, Name: "Deny", Kind: acp.RejectOnce},
		},
	})
	if err != nil {
		return denied("Studio couldn't be asked: " + err.Error())
	}
	var res acp.RequestPermissionResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return denied("Studio's answer was not valid.")
	}
	switch {
	case res.Outcome.Outcome == "selected" && res.Outcome.OptionID == optionAllow:
		return permissionAnswer{Behavior: "allow", UpdatedInput: input}
	case res.Outcome.Outcome == "cancelled":
		return denied("The user didn't answer this request in Studio.")
	}
	return denied("The user denied this action in Studio.")
}

// ServePermission serves the permissions server, `studio-agent claude-permission`: an MCP
// server on in and out with one tool, prompt, which Claude Code calls for permission prompts.
// Each call asks the shim on socket and returns the answer as the tool's text, which is how
// Claude Code reads it.
func ServePermission(ctx context.Context, in io.Reader, out io.Writer, socket string) error {
	var wmu sync.Mutex
	write := func(m rpcMessage) {
		b, err := json.Marshal(m)
		if err != nil {
			return
		}
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), acp.MaxLine)
	for ctx.Err() == nil && sc.Scan() {
		var m rpcMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue // notifications need no reply
		}
		if m.Method == "tools/call" {
			wg.Add(1)
			go func(m rpcMessage) {
				defer wg.Done()
				write(rpcMessage{JSONRPC: "2.0", ID: m.ID, Result: callPrompt(ctx, socket, m.Params)})
			}(m)
			continue
		}
		write(permissionReply(m))
	}
	return sc.Err()
}

// permissionReply answers the requests the permissions server handles without asking.
func permissionReply(m rpcMessage) rpcMessage {
	var result any
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		v := mcpVersions[0]
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "studio-permissions", "version": version.Version},
		}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": []any{promptToolSchema()}}
	default:
		return rpcMessage{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{Code: acp.CodeMethodNotFound, Message: "method not found: " + m.Method}}
	}
	b, _ := json.Marshal(result)
	return rpcMessage{JSONRPC: "2.0", ID: m.ID, Result: b}
}

func promptToolSchema() map[string]any {
	return map[string]any{
		"name":        promptTool,
		"description": "Asks Studio's user whether an action may run. Claude Code calls it for each action it doesn't approve itself.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool_name":   map[string]any{"type": "string"},
				"input":       map[string]any{"type": "object"},
				"tool_use_id": map[string]any{"type": "string"},
			},
			"required": []string{"tool_name", "input"},
		},
	}
}

// callPrompt answers a tools/call of prompt: the answer as JSON text.
func callPrompt(ctx context.Context, socket string, params json.RawMessage) json.RawMessage {
	var p struct {
		Name      string            `json:"name"`
		Arguments permissionRequest `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	ans := denied("unknown tool: " + p.Name)
	if p.Name == promptTool {
		ans = ask(ctx, socket, p.Arguments)
	}
	text, _ := json.Marshal(ans)
	b, _ := json.Marshal(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": string(text)}},
		"isError": false,
	})
	return b
}

// ask sends one question to the shim and returns its answer. Whatever goes wrong denies the
// action.
func ask(ctx context.Context, socket string, req permissionRequest) permissionAnswer {
	if socket == "" {
		return denied("The permission socket isn't set, so Studio can't be asked.")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return denied("Studio can't be reached for this approval: " + err.Error())
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return denied("Studio can't be reached for this approval: " + err.Error())
	}
	var ans permissionAnswer
	if err := json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&ans); err != nil {
		return denied("Studio's answer didn't arrive: " + err.Error())
	}
	if ans.Behavior != "allow" && ans.Behavior != "deny" {
		return denied("Studio's answer was not valid.")
	}
	return ans
}
