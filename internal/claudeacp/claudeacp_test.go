package claudeacp

// The tests drive the shim as an ACP client (acp.Conn) and run Claude Code as a fake: the
// test binary re-executed with roleEnv=claude. The fake prints the stream-json of the
// fixtures in testdata, which are written from Claude Code's documentation, not recorded
// from a live run, and it asks the real permissions server about the tool calls a prompt
// asks for. No real claude runs, and no model is called.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

const (
	roleEnv = "CLAUDEACP_TEST_ROLE" // "claude" for the fake, "permission" for the permissions server
	argvEnv = "CLAUDEACP_TEST_ARGV" // the file the fake records each start's arguments in
	dataEnv = "CLAUDEACP_TEST_DATA" // the directory of the fixtures
	// testToolID and testBashInput are what doc-tool-use.jsonl's tool call carries.
	testToolID    = "toolu_doc_1"
	testBashInput = `{"command":"echo hi","description":"Say hi"}`
	testTimeout   = 20 * time.Second
)

func TestMain(m *testing.M) {
	switch os.Getenv(roleEnv) {
	case "claude":
		fakeClaude()
		os.Exit(0)
	case "permission":
		if err := ServePermission(context.Background(), os.Stdin, os.Stdout, os.Getenv(SocketEnv)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeClaude is Claude Code for the tests. A prompt starting with "say" gets the text
// fixture, "run" asks about a Bash call and "ask" about a question, "wait" streams until it
// is stopped.
func fakeClaude() {
	args := os.Args[1:]
	if path := os.Getenv(argvEnv); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			exitOn(json.NewEncoder(f).Encode(args))
			f.Close()
		}
	}
	session := flagOf(args, "--session-id")
	if session == "" {
		session = flagOf(args, "--resume")
	}
	servers := pluginServers(flagOf(args, "--plugin-dir"))
	emit := func(fixture string, replace ...string) {
		b := readFixture(fixture)
		b = bytes.ReplaceAll(b, []byte("SESSION"), []byte(session))
		for i := 0; i+1 < len(replace); i += 2 {
			b = bytes.ReplaceAll(b, []byte(replace[i]), []byte(replace[i+1]))
		}
		os.Stdout.Write(b)
	}
	emit("doc-init.jsonl")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		text := promptText(sc.Bytes())
		writeTranscript(session)
		switch {
		case strings.HasPrefix(text, "say"):
			emit("doc-text.jsonl")
		case strings.HasPrefix(text, "run"):
			emit("doc-tool-use.jsonl")
			if ans := askPermission(servers, PermissionTool, "Bash", testBashInput); ans.Behavior == "allow" {
				emit("doc-tool-result.jsonl")
			} else {
				emit("doc-tool-denied.jsonl")
			}
		case strings.HasPrefix(text, "ask"):
			emit("doc-tool-use.jsonl", `"name":"Bash"`, `"name":"AskUserQuestion"`)
			askPermission(servers, PermissionTool, "AskUserQuestion", `{"questions":[]}`)
			emit("doc-tool-denied.jsonl")
		case strings.HasPrefix(text, "wait"):
			emit("doc-wait.jsonl")
			time.Sleep(time.Hour) // until the shim stops it
		}
	}
}

func readFixture(name string) []byte {
	b, err := os.ReadFile(filepath.Join(os.Getenv(dataEnv), name))
	exitOn(err)
	return b
}

// exitOn ends the fake on an error, which the test sees as Claude Code failing.
func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// flagOf is the value of flag in args, or "".
func flagOf(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// promptText is the text of a stream-json user message.
func promptText(line []byte) string {
	var m struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &m) != nil {
		return ""
	}
	for _, b := range m.Message.Content {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}

// writeTranscript writes the session's transcript where Claude Code keeps it, as the first
// turn does.
func writeTranscript(session string) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	p := filepath.Join(dir, "projects", "test-project")
	exitOn(os.MkdirAll(p, 0o700))
	exitOn(os.WriteFile(filepath.Join(p, session+".jsonl"), []byte("{}\n"), 0o600))
}

type pluginServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// pluginServers reads the MCP servers of a plugin directory.
func pluginServers(dir string) map[string]pluginServer {
	var cfg struct {
		MCPServers map[string]pluginServer `json:"mcpServers"`
	}
	b, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil || json.Unmarshal(b, &cfg) != nil {
		fmt.Fprintln(os.Stderr, "plugin:", err)
		os.Exit(1)
	}
	return cfg.MCPServers
}

// askPermission asks the permissions server of the plugin, over MCP as Claude Code does,
// about one tool call, and returns its answer.
func askPermission(servers map[string]pluginServer, tool, name, input string) permissionAnswer {
	rest, _ := strings.CutPrefix(tool, "mcp__plugin_studio_")
	server, toolName, _ := strings.Cut(rest, "__")
	s := servers[server]
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, roleEnv+"=permission")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.Exit(1)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		os.Exit(1)
	}
	enc := json.NewEncoder(stdin)
	exitOn(enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18"}}))
	exitOn(enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}))
	exitOn(enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": toolName,
		"arguments": map[string]any{"tool_name": name, "input": json.RawMessage(input),
			"tool_use_id": testToolID},
	}}))
	var ans permissionAnswer
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		var r struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.ID != 2 {
			continue
		}
		if len(r.Result.Content) > 0 {
			exitOn(json.Unmarshal([]byte(r.Result.Content[0].Text), &ans))
		}
		break
	}
	stdin.Close()
	exitOn(cmd.Wait())
	return ans
}

// testClient is Studio's side of the stream: it records updates and gives permission
// requests to the test, which answers them through answer.
type testClient struct {
	updates chan acp.SessionNotification
	perms   chan acp.RequestPermissionRequest
	answer  chan acp.RequestPermissionResponse
}

func (c *testClient) SessionUpdate(n acp.SessionNotification) { c.updates <- n }

func (c *testClient) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.perms <- req
	select {
	case ans := <-c.answer:
		return ans, nil
	case <-ctx.Done():
		return acp.Cancelled(), nil
	}
}

func (c *testClient) CreateElicitation(context.Context, acp.ElicitationRequest) (acp.ElicitationResponse, error) {
	return acp.ElicitationResponse{Action: acp.ElicitDecline}, nil
}

// shim is one Serve on a pipe, with its client.
type shim struct {
	conn   *acp.Conn
	client *testClient
	argv   string // the file where the fake records the starts of Claude Code
}

// startShim serves an agent on a pipe; the test's end is the client.
func startShim(t *testing.T) *shim {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(t.TempDir(), "argv")
	data, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	serverEnd, clientEnd := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Serve ends with the test, when its error has no one left to report to.
	go func() {
		_ = Serve(ctx, serverEnd, serverEnd, Options{
			Claude:    exe,
			ClaudeEnv: []string{roleEnv + "=claude", argvEnv + "=" + argv, dataEnv + "=" + data},
			Self:      exe,
			Log:       os.Stderr,
		})
	}()
	cl := &testClient{
		updates: make(chan acp.SessionNotification, 256),
		perms:   make(chan acp.RequestPermissionRequest, 16),
		answer:  make(chan acp.RequestPermissionResponse, 16),
	}
	conn := acp.NewConn(clientEnd, cl)
	t.Cleanup(func() { conn.Close() })
	return &shim{conn: conn, client: cl, argv: argv}
}

// setHome points the home directory at a temporary one, where transcripts are kept.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

// testServers are the MCP servers Studio sends with a session.
func testServers() []acp.MCPServer {
	return []acp.MCPServer{{Name: "studio", Command: "/opt/studio/bin/studio-agent", Args: []string{"mcp"},
		Env: []acp.EnvVariable{{Name: "STUDIO_TEST", Value: "1"}}}}
}

// open initializes the agent and creates a session in a temporary workspace.
func (s *shim) open(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	init, err := s.conn.Initialize(ctx, acp.Implementation{Name: "test", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if !init.AgentCapabilities.CanResume() || init.AgentCapabilities.LoadSession {
		t.Fatalf("capabilities: %+v", init.AgentCapabilities)
	}
	id, err := s.conn.NewSession(ctx, t.TempDir(), testServers())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// promptAsync starts a prompt and returns the channel of its result.
func (s *shim) promptAsync(t *testing.T, id, text string) <-chan acp.PromptResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	done, err := s.conn.StartPrompt(ctx, id, []acp.ContentBlock{{Type: "text", Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	return done
}

// await waits for a prompt's result.
func await(t *testing.T, done <-chan acp.PromptResult) acp.PromptResponse {
	t.Helper()
	select {
	case r := <-done:
		if r.Err != nil {
			t.Fatalf("prompt: %v", r.Err)
		}
		return r.Response
	case <-time.After(testTimeout):
		t.Fatal("the prompt never ended")
	}
	return acp.PromptResponse{}
}

func (s *shim) prompt(t *testing.T, id, text string) acp.PromptResponse {
	t.Helper()
	return await(t, s.promptAsync(t, id, text))
}

// drain returns the updates received so far. A prompt's updates arrive before its result.
func (s *shim) drain() []acp.SessionNotification {
	var out []acp.SessionNotification
	for {
		select {
		case n := <-s.client.updates:
			out = append(out, n)
		default:
			return out
		}
	}
}

// nextPermission waits for the agent to ask Studio's user.
func (s *shim) nextPermission(t *testing.T) acp.RequestPermissionRequest {
	t.Helper()
	select {
	case req := <-s.client.perms:
		return req
	case <-time.After(testTimeout):
		t.Fatal("the agent never asked for a permission")
	}
	return acp.RequestPermissionRequest{}
}

// argvs are the arguments of each start of the fake Claude Code.
func (s *shim) argvs(t *testing.T) [][]string {
	t.Helper()
	b, err := os.ReadFile(s.argv)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var args []string
		if err := json.Unmarshal(line, &args); err != nil {
			t.Fatal(err)
		}
		out = append(out, args)
	}
	return out
}

func agentText(updates []acp.SessionNotification) string {
	var b strings.Builder
	for _, n := range updates {
		if n.Update.Kind == acp.UpdateAgentMessageChunk {
			c, _ := n.Update.Chunk()
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func TestServeStreamsTextAndRunsClaudeForTheSession(t *testing.T) {
	setHome(t)
	s := startShim(t)
	id := s.open(t)
	resp := s.prompt(t, id, "say hello")
	if resp.StopReason != acp.StopEndTurn || string(resp.Usage) == "" {
		t.Fatalf("response: %+v", resp)
	}
	// The deltas are shown once: the complete message's text was streamed already.
	if got := agentText(s.drain()); got != "Hello" {
		t.Fatalf("text %q", got)
	}

	argv := s.argvs(t)
	if len(argv) != 1 || flagOf(argv[0], "--session-id") != id || slices.Contains(argv[0], "--resume") {
		t.Fatalf("first start: %q", argv)
	}
	for _, want := range []string{"--input-format", "stream-json", "--include-partial-messages", "--verbose"} {
		if !slices.Contains(argv[0], want) {
			t.Errorf("start without %s: %q", want, argv[0])
		}
	}
	if flagOf(argv[0], "--permission-mode") != "bypassPermissions" || flagOf(argv[0], "--permission-prompt-tool") != PermissionTool {
		t.Errorf("permissions: %q", argv[0])
	}
	// Studio's own tools run without asking; the permissions server's tools are asked about.
	if i := slices.Index(argv[0], "--allowedTools"); i < 0 || !slices.Equal(argv[0][i+1:], []string{"mcp__plugin_studio_studio__*"}) {
		t.Errorf("allowed tools: %q", argv[0])
	}

	// The plugin: Studio's servers and the permissions server, with the socket.
	dir := flagOf(argv[0], "--plugin-dir")
	var manifest struct {
		Name string `json:"name"`
	}
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil || json.Unmarshal(b, &manifest) != nil || manifest.Name != pluginName {
		t.Fatalf("manifest: %v %s", err, b)
	}
	servers := pluginServers(dir)
	if servers["studio"].Command != "/opt/studio/bin/studio-agent" || servers["studio"].Env["STUDIO_TEST"] != "1" {
		t.Errorf("studio server: %+v", servers["studio"])
	}
	perm := servers[permissionServer]
	if len(perm.Args) != 1 || perm.Args[0] != "claude-permission" || perm.Env[SocketEnv] == "" {
		t.Errorf("permissions server: %+v", perm)
	}
}

func TestPermissionAllowRunsTheToolCall(t *testing.T) {
	setHome(t)
	s := startShim(t)
	id := s.open(t)
	done := s.promptAsync(t, id, "run the command")

	req := s.nextPermission(t)
	if req.SessionID != id || req.ToolCall.ToolCallID != testToolID || req.ToolCall.Title != "Run Say hi" ||
		req.ToolCall.Kind != "execute" || !strings.Contains(string(req.ToolCall.RawInput), "echo hi") {
		t.Fatalf("permission request: %+v", req)
	}
	// Studio offers allow once and deny; "don't ask again" needs a rule the shim doesn't write.
	var kinds []string
	for _, o := range req.Options {
		kinds = append(kinds, o.Kind)
	}
	if !slices.Equal(kinds, []string{acp.AllowOnce, acp.RejectOnce}) {
		t.Fatalf("options: %v", kinds)
	}
	s.client.answer <- acp.Selected("allow")

	if resp := await(t, done); resp.StopReason != acp.StopEndTurn {
		t.Fatalf("response: %+v", resp)
	}
	var started, completed, text bool
	for _, n := range s.drain() {
		switch {
		case n.Update.Kind == acp.UpdateToolCall && n.Update.ToolCallID == testToolID && n.Update.Status == "in_progress":
			started = true
		case n.Update.Kind == acp.UpdateToolCallUpdate && n.Update.ToolCallID == testToolID && n.Update.Status == "completed":
			completed = true
		case n.Update.Kind == acp.UpdateAgentMessageChunk:
			c, _ := n.Update.Chunk()
			text = text || c.Text == "Done."
		}
	}
	if !started || !completed || !text {
		t.Fatalf("started %v completed %v text %v", started, completed, text)
	}
}

func TestPermissionDenyFailsTheToolCall(t *testing.T) {
	setHome(t)
	s := startShim(t)
	id := s.open(t)
	done := s.promptAsync(t, id, "run the command")
	s.nextPermission(t)
	s.client.answer <- acp.Selected("deny")

	if resp := await(t, done); resp.StopReason != acp.StopEndTurn {
		t.Fatalf("response: %+v", resp)
	}
	var failed bool
	var said string
	for _, n := range s.drain() {
		if n.Update.Kind == acp.UpdateToolCallUpdate && n.Update.ToolCallID == testToolID && n.Update.Status == "failed" {
			failed = true
		}
		if n.Update.Kind == acp.UpdateAgentMessageChunk {
			c, _ := n.Update.Chunk()
			said += c.Text
		}
	}
	if !failed || said != "Okay, I won't run it." {
		t.Fatalf("failed %v said %q", failed, said)
	}
}

func TestIgnoredPermissionIsDenied(t *testing.T) {
	setHome(t)
	s := startShim(t)
	id := s.open(t)
	done := s.promptAsync(t, id, "run the command")
	s.nextPermission(t)
	s.client.answer <- acp.Cancelled() // Studio's Ignore

	if resp := await(t, done); resp.StopReason != acp.StopEndTurn {
		t.Fatalf("response: %+v", resp)
	}
}

func TestQuestionsAreRefusedWithoutAsking(t *testing.T) {
	setHome(t)
	s := startShim(t)
	id := s.open(t)
	if resp := s.prompt(t, id, "ask me something"); resp.StopReason != acp.StopEndTurn {
		t.Fatalf("response: %+v", resp)
	}
	select {
	case req := <-s.client.perms:
		t.Fatalf("Studio was asked about a question: %+v", req)
	default:
	}
}

func TestCancelStopsTheTurnAndTheNextPromptResumes(t *testing.T) {
	setHome(t)
	s := startShim(t)
	id := s.open(t)
	done := s.promptAsync(t, id, "wait for it")
	// Wait until the turn has streamed its first text, so Claude Code is running.
	deadline := time.After(testTimeout)
	for seen := false; !seen; {
		select {
		case n := <-s.client.updates:
			seen = n.Update.Kind == acp.UpdateAgentMessageChunk
		case <-deadline:
			t.Fatal("no text before the cancel")
		}
	}
	if err := s.conn.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if resp := await(t, done); resp.StopReason != acp.StopCancelled {
		t.Fatalf("response: %+v", resp)
	}

	if resp := s.prompt(t, id, "say again"); resp.StopReason != acp.StopEndTurn {
		t.Fatalf("after the cancel: %+v", resp)
	}
	argv := s.argvs(t)
	if len(argv) != 2 || flagOf(argv[1], "--resume") != id || slices.Contains(argv[1], "--session-id") {
		t.Fatalf("starts: %q", argv)
	}
}

func TestResumeUsesTheTranscript(t *testing.T) {
	home := setHome(t)
	const id = "3f1c2b9a-7d4e-4f10-8a6b-5c2d1e0f9a8b"
	dir := filepath.Join(home, ".claude", "projects", "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startShim(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if _, err := s.conn.Initialize(ctx, acp.Implementation{Name: "test", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.conn.ResumeSession(ctx, id, t.TempDir(), testServers()); err != nil {
		t.Fatal(err)
	}
	if resp := s.prompt(t, id, "say hi"); resp.StopReason != acp.StopEndTurn {
		t.Fatalf("response: %+v", resp)
	}
	argv := s.argvs(t)
	if len(argv) != 1 || flagOf(argv[0], "--resume") != id || slices.Contains(argv[0], "--session-id") {
		t.Fatalf("start: %q", argv)
	}
}

func TestResumeWithoutTranscriptIsNotFound(t *testing.T) {
	setHome(t)
	s := startShim(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if _, err := s.conn.Initialize(ctx, acp.Implementation{Name: "test", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	err := s.conn.ResumeSession(ctx, "3f1c2b9a-7d4e-4f10-8a6b-5c2d1e0f9a8b", t.TempDir(), testServers())
	var ae *acp.Error
	if !errors.As(err, &ae) || ae.Code != codeResourceNotFound {
		t.Fatalf("resume: %v", err)
	}
}

func TestSessionIDsAreUUIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		id, err := newSessionID()
		if err != nil || len(id) != 36 || seen[id] || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("id %q: %v", id, err)
		}
		seen[id] = true
	}
}

func TestClaudeContentMapsAttachments(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///attachments/notes.txt","mimeType":"text/plain","text":"hi"}`)
	blocks := []acp.ContentBlock{
		{Type: "text", Text: "look"},
		{Type: "image", MimeType: "image/png", Data: "AAAA"},
		{Type: "image", MimeType: "image/svg+xml", Data: "AAAA"},
		{Type: "resource", Resource: raw},
		{Type: "resource_link", URI: "file:///workspace/web/coupon%20code.ts?start=20&end=28#L20-L28", Name: "coupon code.ts"},
		{Type: "resource_link", URI: "file:///workspace/README.md", Name: "README.md"},
	}
	out, err := claudeContent(blocks)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	s := string(b)
	for _, want := range []string{`"type":"image"`, `"media_type":"image/png"`, "isn't shown", "Attached file file:///attachments/notes.txt",
		"Mentioned file: /workspace/web/coupon code.ts, lines 20 to 28", "Mentioned file: /workspace/README.md\""} {
		if !strings.Contains(s, want) {
			t.Errorf("content %s lacks %s", s, want)
		}
	}
	if strings.Count(s, `"type":"image"`) != 1 {
		t.Errorf("an SVG went to the API as an image: %s", s)
	}
}

// A prompt with attachments near the API's limit gets through, and a line over maxLine is
// skipped without losing the lines after it.
func TestEachLineTakesLargePromptsAndSkipsHugeLines(t *testing.T) {
	big := strings.Repeat("a", 15<<20)
	huge := strings.Repeat("b", maxLine+1)
	var got []int
	err := eachLine(strings.NewReader("one\n"+big+"\n"+huge+"\nlast\n"), func(line []byte) {
		got = append(got, len(line))
	})
	if err != nil || len(got) != 3 || got[0] != 4 || got[1] != len(big)+1 || got[2] != 5 {
		t.Fatalf("lines %v, err %v; want one, the big one and last", got, err)
	}
}
