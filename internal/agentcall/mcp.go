package agentcall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// The MCP server is the minimal subset of the Model Context Protocol the harnesses use:
// newline-delimited JSON-RPC 2.0 over stdio with initialize, ping, tools/list and
// tools/call. Notifications are ignored. Every tool call goes to Studio, which decides
// what the sandbox's persona may read and write.

// mcpVersions are the protocol revisions the server speaks; it answers with the client's
// if listed, else the newest.
var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// mcpToolTimeout bounds one tool call.
const mcpToolTimeout = 60 * time.Second

const mcpInstructions = "Studio's memory: search and read what you and the team remember, save what should last, " +
	"fix or retract your own facts, and propose facts for shared memory (the user approves them)."

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string, extra ...any) map[string]any {
	m := map[string]any{"type": "string", "description": desc}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

var kinds = []string{"preference", "decision", "fact", "procedure", "event"}

// Tools are the memory tools; their arguments go to Studio unchanged.
var Tools = []Tool{
	{agentproto.MethodMemorySearch, "Search your memory and the shared memory (facts and pages), ranked by relevance. Each hit says why it matched.",
		obj([]string{"query"}, map[string]any{
			"query": str("What to look for, in words", "maxLength", 2000),
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Default 8"},
		})},
	{agentproto.MethodMemoryGet, "Read one fact by ID, or one page with its timeline by ID or slug.",
		obj(nil, map[string]any{
			"id":   str("A fact or page ID from a search hit"),
			"slug": str("A page slug, e.g. project/github.com/acme/api"),
		})},
	{agentproto.MethodRemember, "Save a fact to your own memory right away.",
		obj([]string{"text", "kind"}, map[string]any{
			"text":     str("One self-contained claim", "maxLength", 2000),
			"kind":     str("What sort of fact", "enum", kinds),
			"entity":   str("What it is about, e.g. a repo, service or person"),
			"scope":    str("persona (default) saves privately; shared proposes it for everyone and needs the user's approval", "enum", []string{"persona", "shared"}),
			"source":   str("user: the user said so; verified: you ran it and it worked; inferred (default): your conclusion", "enum", []string{"user", "verified", "inferred"}),
			"evidence": str("A short quote or the command and its result", "maxLength", 500),
		})},
	{agentproto.MethodShare, "Propose a fact for shared memory, which every persona reads. The user approves it first.",
		obj(nil, map[string]any{
			"fact_id": str("One of your facts to share"),
			"text":    str("Or a new claim to share", "maxLength", 2000),
			"kind":    str("With text: what sort of fact", "enum", kinds),
			"entity":  str("With text: what it is about"),
		})},
	{agentproto.MethodCorrect, "Replace one of your own facts with a corrected version; the old one is kept as superseded.",
		obj([]string{"fact_id", "text"}, map[string]any{
			"fact_id": str("The fact to correct"),
			"text":    str("The corrected claim", "maxLength", 2000),
		})},
	{agentproto.MethodForget, "Retract one of your own facts that turned out wrong.",
		obj([]string{"fact_id"}, map[string]any{
			"fact_id": str("The fact to retract"),
		})},
}

// ServeMCP answers MCP requests from in on out until in ends or ctx is done.
func ServeMCP(ctx context.Context, in io.Reader, out io.Writer, c Caller) error {
	br := bufio.NewReaderSize(in, 64<<10)
	var wmu sync.Mutex
	write := func(r rpcResponse) {
		r.JSONRPC = "2.0"
		if r.ID == nil {
			r.ID = json.RawMessage("null")
		}
		wmu.Lock()
		defer wmu.Unlock()
		agentproto.WriteJSONLine(out, r)
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for ctx.Err() == nil {
		var req rpcRequest
		err := agentproto.ReadJSONLineLimit(br, &req, agentproto.MaxCallLine)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, agentproto.ErrLineTooLong) {
			write(rpcResponse{Error: &rpcError{-32600, "request too large"}})
			continue
		}
		if err != nil {
			var syn *json.SyntaxError
			var typ *json.UnmarshalTypeError
			if errors.As(err, &syn) || errors.As(err, &typ) {
				write(rpcResponse{Error: &rpcError{-32700, "parse error"}})
				continue
			}
			return err
		}
		if req.ID == nil || string(req.ID) == "null" {
			continue // a notification
		}
		if req.Method == "tools/call" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				write(callTool(ctx, c, req))
			}()
			continue
		}
		write(handle(req))
	}
	return ctx.Err()
}

func handle(req rpcRequest) rpcResponse {
	r := rpcResponse{ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := mcpVersions[0]
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		r.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "studio", "version": version.Version},
			"instructions":    mcpInstructions,
		}
	case "ping":
		r.Result = map[string]any{}
	case "tools/list":
		r.Result = map[string]any{"tools": Tools}
	default:
		r.Error = &rpcError{-32601, "method not found: " + req.Method}
	}
	return r
}

func callTool(ctx context.Context, c Caller, req rpcRequest) rpcResponse {
	r := rpcResponse{ID: req.ID}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		r.Error = &rpcError{-32602, "invalid params"}
		return r
	}
	if !slices.ContainsFunc(Tools, func(t Tool) bool { return t.Name == p.Name }) {
		r.Error = &rpcError{-32602, "unknown tool: " + p.Name}
		return r
	}
	args := p.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	ctx, cancel := context.WithTimeout(ctx, mcpToolTimeout)
	defer cancel()
	var result json.RawMessage
	if err := c.Call(ctx, p.Name, args, &result); err != nil {
		r.Result = toolText(err.Error(), true)
		return r
	}
	text := string(result)
	var pretty any
	if json.Unmarshal(result, &pretty) == nil {
		if s, ok := pretty.(string); ok {
			text = s
		} else if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			text = string(b)
		}
	}
	r.Result = toolText(text, false)
	return r
}

func toolText(text string, isError bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
}
