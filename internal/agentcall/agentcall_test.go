package agentcall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// fakeCaller records calls and answers from reply.
type fakeCaller struct {
	mu    sync.Mutex
	calls []fakeCall
	reply func(method string, params json.RawMessage) (any, error)
}

type fakeCall struct {
	Method string
	Params json.RawMessage
}

func (f *fakeCaller) Call(ctx context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{method, raw})
	f.mu.Unlock()
	var out any = map[string]any{}
	if f.reply != nil {
		var err error
		if out, err = f.reply(method, raw); err != nil {
			return err
		}
	}
	if result == nil {
		return nil
	}
	b, _ := json.Marshal(out)
	return json.Unmarshal(b, result)
}

func (f *fakeCaller) events(t *testing.T) []agenthook.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agenthook.Event
	for _, c := range f.calls {
		var ev agenthook.Event
		if err := json.Unmarshal(c.Params, &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func TestMCPOverPipe(t *testing.T) {
	fc := &fakeCaller{reply: func(method string, params json.RawMessage) (any, error) {
		if method == agentproto.MethodForget {
			return nil, errors.New("fact abc is not in your memory")
		}
		return map[string]any{"method": method, "args": json.RawMessage(params)}, nil
	}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeMCP(context.Background(), inR, outW, fc); outW.Close() }()
	dec := bufio.NewReader(outR)
	send := func(s string) { io.WriteString(inW, s+"\n") }
	recv := func() map[string]any {
		t.Helper()
		line, err := dec.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		return m
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t"}}}`)
	if r := recv(); r["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatalf("initialize: %v", r)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":"two","method":"tools/list"}`)
	r := recv()
	if r["id"] != "two" || len(r["result"].(map[string]any)["tools"].([]any)) != len(Tools) {
		t.Fatalf("tools/list: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remember","arguments":{"text":"use pnpm","kind":"preference"}}}`)
	r = recv()
	res := r["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != false || !strings.Contains(text, `"use pnpm"`) {
		t.Fatalf("tools/call: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"forget","arguments":{"fact_id":"abc"}}}`)
	if r := recv(); r["result"].(map[string]any)["isError"] != true {
		t.Fatalf("tool error: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"rm_rf","arguments":{}}}`)
	if r := recv(); r["error"] == nil {
		t.Fatalf("unknown tool: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	if r := recv(); r["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method: %v", r)
	}
	send(`{not json`)
	if r := recv(); r["error"].(map[string]any)["code"] != float64(-32700) {
		t.Fatalf("parse error: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"` + strings.Repeat("x", agentproto.MaxCallLine) + `"}}}`)
	if r := recv(); r["error"] == nil {
		t.Fatalf("oversized: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if r := recv(); r["id"] != float64(8) {
		t.Fatalf("after oversized: %v", r)
	}
	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(fc.calls) != 2 || fc.calls[0].Method != "remember" {
		t.Fatalf("calls: %+v", fc.calls)
	}
}

// guestHome makes a home with the harness's state dirs, like the sandbox's.
func guestHome(t *testing.T) string {
	home := t.TempDir()
	for _, d := range []string{".claude/projects/-workspace", ".codex/sessions", ".local/share/opencode"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	return home
}

func claudeLine(role, text string) string {
	b, _ := json.Marshal(map[string]any{"type": role, "message": map[string]any{"role": role, "content": []any{
		map[string]any{"type": "text", "text": text},
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "secret-tool-noise"}},
	}}})
	return string(b) + "\n"
}

func TestHookRoundTripPerHarness(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"claude", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			h, _ := agenthook.Lookup(name)
			home := guestHome(t)
			fc := &fakeCaller{reply: func(string, json.RawMessage) (any, error) {
				return agenthook.Result{Context: "<studio-memory>use pnpm</studio-memory>"}, nil
			}}
			o := HookOptions{Caller: fc, Home: home, GitRemote: func(context.Context, string) string {
				return "git@github.com:acme/api.git"
			}}
			var stdin string
			switch name {
			case "opencode":
				stdin = `{"sessionID":"s","source":"startup","cwd":"/workspace"}`
			default:
				stdin = `{"session_id":"s","cwd":"/workspace","hook_event_name":"SessionStart","source":"startup"}`
			}
			var out bytes.Buffer
			if err := RunHook(ctx, h, agenthook.SessionStart, strings.NewReader(stdin), &out, o); err != nil {
				t.Fatal(err)
			}
			want := string(h.Render(agenthook.Result{Event: agenthook.SessionStart, Context: "<studio-memory>use pnpm</studio-memory>"}))
			if out.String() != want {
				t.Fatalf("stdout %q, want %q", out.String(), want)
			}
			ev := fc.events(t)[0]
			if ev.GitRemote != "github.com/acme/api" || ev.Event != agenthook.SessionStart || ev.Harness != name {
				t.Fatalf("event: %+v", ev)
			}
		})
	}
}

func TestHookNeutralOnFailure(t *testing.T) {
	h, _ := agenthook.Lookup("claude")
	stdin := `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"hi"}`
	// Studio unreachable.
	var out bytes.Buffer
	err := RunHook(context.Background(), h, agenthook.UserPrompt, strings.NewReader(stdin), &out,
		HookOptions{Caller: SocketCaller{Path: filepath.Join(t.TempDir(), "none.sock")}, Home: t.TempDir()})
	if err == nil || out.String() != "{}\n" {
		t.Fatalf("unreachable: %v %q", err, out.String())
	}
	// Studio hangs: the answer comes at the deadline.
	block := make(chan struct{})
	defer close(block)
	hang := &fakeCaller{reply: func(string, json.RawMessage) (any, error) { <-block; return nil, nil }}
	out.Reset()
	start := time.Now()
	err = RunHook(context.Background(), h, agenthook.UserPrompt, strings.NewReader(stdin), &out,
		HookOptions{Caller: hang, Home: t.TempDir(), Deadline: 100 * time.Millisecond})
	if err == nil || out.String() != "{}\n" || time.Since(start) > 2*time.Second {
		t.Fatalf("hang: %v %q %s", err, out.String(), time.Since(start))
	}
	// Garbage input.
	out.Reset()
	if err := RunHook(context.Background(), h, agenthook.UserPrompt, strings.NewReader("garbage"), &out, HookOptions{Caller: hang, Home: t.TempDir()}); err == nil || out.String() != "{}\n" {
		t.Fatalf("garbage: %v %q", err, out.String())
	}
}

func TestTranscriptDelta(t *testing.T) {
	ctx := context.Background()
	h, _ := agenthook.Lookup("claude")
	home := guestHome(t)
	path := filepath.Join(home, ".claude/projects/-workspace/s.jsonl")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	fail := false
	fc := &fakeCaller{reply: func(string, json.RawMessage) (any, error) {
		if fail {
			return nil, errors.New("down")
		}
		return map[string]any{}, nil
	}}
	o := HookOptions{Caller: fc, Home: home, Now: func() time.Time { return now }}
	run := func(event string) error {
		in, _ := json.Marshal(map[string]any{"session_id": "s", "transcript_path": path, "hook_event_name": map[string]string{
			agenthook.PreCompact: "PreCompact", agenthook.Stop: "Stop", agenthook.SessionEnd: "SessionEnd"}[event]})
		var out bytes.Buffer
		err := RunHook(ctx, h, event, bytes.NewReader(in), &out, o)
		if out.String() != "{}\n" {
			t.Fatalf("stdout %q", out.String())
		}
		return err
	}
	appendFile := func(s string) {
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		f.WriteString(s)
		f.Close()
	}

	appendFile(claudeLine("user", "We always deploy with make ship.") + claudeLine("assistant", "Noted.") + `{"type":"user","message":{"role":"user","content":"partial`)
	if err := run(agenthook.PreCompact); err != nil {
		t.Fatal(err)
	}
	evs := fc.events(t)
	if len(evs) != 1 || evs[0].Transcript != "user: We always deploy with make ship.\nassistant: Noted.\n" {
		t.Fatalf("first delta: %+v", evs)
	}
	if strings.Contains(string(fc.calls[0].Params), path) || strings.Contains(string(fc.calls[0].Params), "secret-tool-noise") {
		t.Fatal("path or tool output left the guest")
	}

	// The partial line completes; only it is sent next.
	appendFile(` line"}}` + "\n")
	if err := run(agenthook.SessionEnd); err != nil {
		t.Fatal(err)
	}
	if evs = fc.events(t); len(evs) != 2 || evs[1].Transcript != "user: partial line\n" {
		t.Fatalf("second delta: %+v", evs[len(evs)-1])
	}

	// Stop is debounced: too soon and too small.
	appendFile(claudeLine("user", "small"))
	if err := run(agenthook.Stop); err != nil || len(fc.events(t)) != 2 {
		t.Fatalf("stop debounce: %v %d", err, len(fc.events(t)))
	}
	now = now.Add(10 * time.Minute)
	appendFile(claudeLine("assistant", strings.Repeat("long answer ", 300)))
	if err := run(agenthook.Stop); err != nil || len(fc.events(t)) != 3 || !strings.HasPrefix(fc.events(t)[2].Transcript, "user: small\n") {
		t.Fatalf("stop after debounce: %v %+v", err, fc.events(t))
	}

	// A failed send keeps the offset, so the next hook sends the same delta again.
	appendFile(claudeLine("user", "retry me"))
	fail = true
	if err := run(agenthook.PreCompact); err == nil {
		t.Fatal("failed send reported success")
	}
	fail = false
	if err := run(agenthook.PreCompact); err != nil {
		t.Fatal(err)
	}
	evs = fc.events(t)
	if evs[len(evs)-1].Transcript != "user: retry me\n" || evs[len(evs)-2].Transcript != "user: retry me\n" {
		t.Fatalf("retry: %+v", evs[len(evs)-2:])
	}

	// A huge delta keeps its most recent part, aligned to a line.
	big := ""
	for i := 0; len(big) < 2*MaxRawDelta; i++ {
		big += claudeLine("user", "filler "+strings.Repeat("y", 500))
	}
	appendFile(big + claudeLine("user", "the newest"))
	if err := run(agenthook.PreCompact); err != nil {
		t.Fatal(err)
	}
	last := fc.events(t)[len(fc.events(t))-1]
	if last.TranscriptSkipped == 0 || !strings.HasSuffix(last.Transcript, "user: the newest\n") || len(last.Transcript) > MaxCondensed ||
		!strings.HasPrefix(last.Transcript, "user: filler") {
		t.Fatalf("big delta: skipped %d, %d bytes", last.TranscriptSkipped, len(last.Transcript))
	}

	// A replaced (shorter) file is read from the start.
	os.WriteFile(path, []byte(claudeLine("user", "fresh")), 0o644)
	if err := run(agenthook.PreCompact); err != nil {
		t.Fatal(err)
	}
	if last := fc.events(t)[len(fc.events(t))-1]; last.Transcript != "user: fresh\n" {
		t.Fatalf("replaced: %q", last.Transcript)
	}
}

func TestTranscriptPathRestricted(t *testing.T) {
	h, _ := agenthook.Lookup("claude")
	home := guestHome(t)
	outside := filepath.Join(t.TempDir(), "secret.jsonl")
	os.WriteFile(outside, []byte(claudeLine("user", "secret")), 0o644)
	link := filepath.Join(home, ".claude/projects/-workspace/link.jsonl")
	os.Symlink(outside, link)
	fc := &fakeCaller{}
	for _, p := range []string{outside, link, "relative.jsonl", filepath.Join(home, ".claude/../.ssh/id")} {
		in, _ := json.Marshal(map[string]any{"session_id": "s", "transcript_path": p})
		var out bytes.Buffer
		if err := RunHook(context.Background(), h, agenthook.SessionEnd, bytes.NewReader(in), &out, HookOptions{Caller: fc, Home: home}); err == nil {
			t.Errorf("%s: read", p)
		}
	}
	if len(fc.calls) != 0 {
		t.Fatal("a transcript outside the state dirs was sent")
	}
}

func TestOpenCodeInlineDelta(t *testing.T) {
	h, _ := agenthook.Lookup("opencode")
	fc := &fakeCaller{}
	o := HookOptions{Caller: fc, Home: guestHome(t)}
	send := func(text string, offset int) {
		in, _ := json.Marshal(map[string]any{"sessionID": "s", "transcript": text, "transcriptOffset": offset})
		var out bytes.Buffer
		if err := RunHook(context.Background(), h, agenthook.PreCompact, bytes.NewReader(in), &out, o); err != nil {
			t.Fatal(err)
		}
	}
	send("user: héllo 👋\n", 0)
	send("user: héllo 👋\nassistant: hi\n", 0)
	// The plugin cut the front: offset in UTF-16 units of the whole text.
	send("hi\nuser: bye\n", 26) // "user: héllo 👋\nassistant: " is 26 UTF-16 units
	evs := fc.events(t)
	got := []string{evs[0].Transcript, evs[1].Transcript, evs[2].Transcript}
	want := []string{"user: héllo 👋\n", "assistant: hi\n", "user: bye\n"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("send %d: %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCondenseCodex(t *testing.T) {
	lines := []string{
		`{"type":"session_meta","payload":{"id":"x"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"system stuff"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>cwd</environment_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Use port 8080 <studio-memory>old memory</studio-memory>"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"Use port 8080"}}`,
	}
	got := Condense([]byte(strings.Join(lines, "\n")))
	if got != "user: Use port 8080\nassistant: OK\n" {
		t.Fatalf("%q", got)
	}
}
