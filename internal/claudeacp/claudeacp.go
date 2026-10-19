// Package claudeacp is the agent side of the Agent Client Protocol for a chat with a Claude
// subscription: `studio-agent claude-acp` serves one session on stdio and runs Claude Code
// itself for its turns, `claude -p` with stream-json (docs/harnesses.md, "Chats (ACP)").
// Its output becomes ACP updates, and its permission prompts become Studio's approval cards
// (permission.go). The shim never holds the credential: the guest has the placeholder, and
// the gateway swaps it on the way to Anthropic.
package claudeacp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// PermissionTool is the tool Claude Code calls for permission prompts: the prompt tool of the
// plugin's permissions server, named mcp__plugin_<plugin>_<server>__<tool> (plugin.go).
const PermissionTool = "mcp__plugin_" + pluginName + "_" + permissionServer + "__" + promptTool

// SocketEnv names the variable that gives the permission server the path of the shim's socket.
const SocketEnv = "STUDIO_CLAUDE_PERMISSION_SOCKET"

const (
	pluginName       = "studio"
	permissionServer = "permissions"
	promptTool       = "prompt"
	agentName        = "studio-claude-acp"
	// codeResourceNotFound is ACP's error for a session the agent doesn't have.
	codeResourceNotFound = -32002
	// stopGrace is how long Claude Code gets to exit after SIGTERM before it is killed.
	stopGrace = 5 * time.Second
)

// Options are the shim's dependencies. The zero value runs the real binaries.
type Options struct {
	// Claude is the Claude Code binary. Default: $CLAUDE_CODE_EXECUTABLE, else "claude".
	Claude string
	// ClaudeEnv is added to the environment of every Claude Code process (tests use it).
	ClaudeEnv []string
	// Self is the binary the permission server runs from. Default: this executable.
	Self string
	// Log receives diagnostics and Claude Code's stderr. Default: os.Stderr.
	Log io.Writer
}

// Serve answers ACP requests read from in and writes replies and updates to out until in
// ends or ctx is done. It serves one session: a second session/new or session/resume is
// refused. When it returns, Claude Code has been stopped and the session's files removed.
func Serve(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	if opts.Log == nil {
		opts.Log = os.Stderr
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &agent{ctx: ctx, cancel: cancel, opts: opts, out: out, pending: map[int64]chan rpcMessage{}}
	defer a.shutdown()
	done := make(chan error, 1)
	go func() {
		done <- eachLine(in, func(line []byte) {
			var m rpcMessage
			if json.Unmarshal(line, &m) == nil {
				a.dispatch(m)
			}
		})
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// rpcMessage is one JSON-RPC 2.0 message; ACP is newline-delimited JSON-RPC.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// agent is the ACP agent on one stream.
type agent struct {
	ctx    context.Context
	cancel context.CancelFunc
	opts   Options
	out    io.Writer
	wmu    sync.Mutex // one line at a time

	mu      sync.Mutex // guards the fields below
	sess    *session
	nextID  int64
	pending map[int64]chan rpcMessage // requests to Studio, awaiting their replies
}

// shutdown ends the session and the agent's requests to Studio.
func (a *agent) shutdown() {
	a.cancel()
	a.mu.Lock()
	s := a.sess
	a.mu.Unlock()
	if s != nil {
		s.stop()
	}
}

func (a *agent) dispatch(m rpcMessage) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		go a.request(m)
	case m.Method != "":
		a.notification(m)
	case len(m.ID) > 0:
		a.response(m)
	}
}

// request answers one request from Studio.
func (a *agent) request(m rpcMessage) {
	result, rerr := a.handle(m)
	if rerr != nil {
		_ = a.write(rpcMessage{JSONRPC: "2.0", ID: m.ID, Error: rerr})
		return
	}
	a.reply(m.ID, result)
}

func (a *agent) handle(m rpcMessage) (any, *rpcError) {
	switch m.Method {
	case acp.MethodInitialize:
		return acp.InitializeResponse{
			ProtocolVersion: acp.ProtocolVersion,
			AgentCapabilities: acp.AgentCapabilities{
				PromptCapabilities:  acp.PromptCapabilities{Image: true, EmbeddedContext: true},
				SessionCapabilities: acp.SessionCapabilities{Resume: json.RawMessage("{}")},
			},
			AgentInfo: &acp.Implementation{Name: agentName, Version: version.Version},
		}, nil
	case acp.MethodSessionNew:
		var req acp.NewSessionRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			return nil, invalid(err)
		}
		id, err := newSessionID()
		if err != nil {
			return nil, internal(err)
		}
		if err := a.open(id, req.Cwd, req.MCPServers); err != nil {
			return nil, internal(err)
		}
		return acp.NewSessionResponse{SessionID: id}, nil
	case acp.MethodSessionResume:
		var req acp.LoadSessionRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			return nil, invalid(err)
		}
		if !validSessionID(req.SessionID) {
			return nil, &rpcError{Code: acp.CodeInvalidParams, Message: "invalid session ID"}
		}
		if !transcriptExists(req.SessionID) {
			// Studio ends the chat when its session is gone.
			return nil, &rpcError{Code: codeResourceNotFound, Message: "Resource not found: session " + req.SessionID}
		}
		if err := a.open(req.SessionID, req.Cwd, req.MCPServers); err != nil {
			return nil, internal(err)
		}
		return struct{}{}, nil
	case acp.MethodSessionPrompt:
		var req acp.PromptRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			return nil, invalid(err)
		}
		s := a.session(req.SessionID)
		if s == nil {
			return nil, &rpcError{Code: acp.CodeInvalidParams, Message: "unknown session"}
		}
		content, err := claudeContent(req.Prompt)
		if err != nil {
			return nil, &rpcError{Code: acp.CodeInvalidParams, Message: err.Error()}
		}
		resp, err := s.prompt(content)
		if err != nil {
			return nil, internal(err)
		}
		return resp, nil
	}
	return nil, &rpcError{Code: acp.CodeMethodNotFound, Message: "method not found: " + m.Method}
}

func invalid(err error) *rpcError {
	return &rpcError{Code: acp.CodeInvalidParams, Message: err.Error()}
}

func internal(err error) *rpcError { return &rpcError{Code: acp.CodeInternal, Message: err.Error()} }

// notification handles session/cancel; the other notifications are ignored.
func (a *agent) notification(m rpcMessage) {
	if m.Method != acp.MethodSessionCancel {
		return
	}
	var req acp.SessionRequest
	if json.Unmarshal(m.Params, &req) != nil {
		return
	}
	if s := a.session(req.SessionID); s != nil {
		s.cancel()
	}
}

// response routes a reply to one of the agent's requests to Studio.
func (a *agent) response(m rpcMessage) {
	id, err := parseID(m.ID)
	if err != nil {
		return
	}
	a.mu.Lock()
	ch := a.pending[id]
	delete(a.pending, id)
	a.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

// call sends a request to Studio and waits for its reply or for ctx.
func (a *agent) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan rpcMessage, 1)
	a.mu.Lock()
	a.nextID++
	id := a.nextID
	a.pending[id] = ch
	a.mu.Unlock()
	forget := func() {
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
	}
	if err := a.write(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: method, Params: p}); err != nil {
		forget()
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// reply writes a successful response.
func (a *agent) reply(id json.RawMessage, result any) {
	b, err := json.Marshal(result)
	if err != nil {
		_ = a.write(rpcMessage{JSONRPC: "2.0", ID: id, Error: internal(err)})
		return
	}
	_ = a.write(rpcMessage{JSONRPC: "2.0", ID: id, Result: b})
}

// write sends one message. A failed write means Studio has gone, and nothing else can be
// sent to it either, so the error is only returned to the callers that can use it.
func (a *agent) write(m rpcMessage) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	a.wmu.Lock()
	defer a.wmu.Unlock()
	_, err = a.out.Write(append(b, '\n'))
	return err
}

func (a *agent) session(id string) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil || a.sess.id != id {
		return nil
	}
	return a.sess
}

// open creates the session with its run directory, plugin and permission socket.
func (a *agent) open(id, cwd string, servers []acp.MCPServer) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess != nil {
		return errors.New("the agent serves one session")
	}
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("the working directory %q is not absolute", cwd)
	}
	self := a.opts.Self
	if self == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		self = exe
	}
	s, err := newSession(a, id, cwd, self, servers)
	if err != nil {
		return err
	}
	a.sess = s
	return nil
}

// claudeBinary is the Claude Code binary the session runs.
func (a *agent) claudeBinary() string {
	if a.opts.Claude != "" {
		return a.opts.Claude
	}
	if p := os.Getenv("CLAUDE_CODE_EXECUTABLE"); p != "" {
		return p
	}
	return "claude"
}

// session is the agent's one ACP session, which runs Claude Code for its turns.
type session struct {
	a      *agent
	id     string
	cwd    string
	dir    string       // the run directory, private to this agent
	plugin string       // the plugin Claude Code loads with --plugin-dir
	socket net.Listener // where the permission server asks
	// allowed are the tools Claude Code runs without asking: the MCP tools of Studio's own
	// servers, which decide what the teammate may do (the permissions server is asked about).
	allowed []string

	mu   sync.Mutex // guards proc and turn, and the turn's cancelled flag
	proc *process   // the running Claude Code process, if any
	turn *turn      // the prompt being answered, if any
}

// turn is a prompt being answered.
type turn struct {
	cancelled bool // set by cancel; guarded by session.mu
	done      chan turnResult
}

// turnResult ends a turn: its response, or the error the prompt returns.
type turnResult struct {
	resp acp.PromptResponse
	err  error
}

func newSession(a *agent, id, cwd, self string, servers []acp.MCPServer) (*session, error) {
	dir, err := os.MkdirTemp("", "studio-claude-")
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*session, error) {
		os.RemoveAll(dir)
		return nil, err
	}
	sock := filepath.Join(dir, "permission.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fail(err)
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		return fail(err)
	}
	plugin := filepath.Join(dir, "plugin")
	if err := writePlugin(plugin, sock, self, servers); err != nil {
		ln.Close()
		return fail(err)
	}
	s := &session{a: a, id: id, cwd: cwd, dir: dir, plugin: plugin, socket: ln}
	for _, srv := range servers {
		s.allowed = append(s.allowed, "mcp__plugin_"+pluginName+"_"+srv.Name+"__*")
	}
	go s.acceptPermissions(ln)
	return s, nil
}

// stop ends the session's Claude Code process, if it runs, and removes its files.
func (s *session) stop() {
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	if p != nil {
		p.stdin.Close()
		p.terminate()
		<-p.exited
	}
	s.socket.Close()
	os.RemoveAll(s.dir)
}

// prompt sends one user message to the session's Claude Code process, starting the process
// if it isn't running, and returns when the turn ends.
func (s *session) prompt(content []any) (acp.PromptResponse, error) {
	s.mu.Lock()
	if s.turn != nil {
		s.mu.Unlock()
		return acp.PromptResponse{}, errors.New("the agent is still working on the last request")
	}
	p := s.proc
	if p == nil {
		var err error
		if p, err = s.start(); err != nil {
			s.mu.Unlock()
			return acp.PromptResponse{}, err
		}
		s.proc = p
	}
	t := &turn{done: make(chan turnResult, 1)}
	s.turn = t
	s.mu.Unlock()
	msg, err := json.Marshal(userMessage{Type: "user", Message: userBody{Role: "user", Content: content}})
	if err == nil {
		err = p.write(msg)
	}
	if err != nil {
		p.terminate() // its exit ends the turn with an error
	}
	r := <-t.done
	return r.resp, r.err
}

// cancel ends the running turn. Claude Code is stopped, and the next prompt resumes the
// session in a new process: Claude Code leaves an interrupted turn as it is on resume.
func (s *session) cancel() {
	s.mu.Lock()
	t, p := s.turn, s.proc
	if t != nil {
		t.cancelled = true
	}
	s.mu.Unlock()
	if t != nil && p != nil {
		p.terminate()
	}
}

// start runs Claude Code for the session with s.mu held. The first run creates the session
// with its ID; later runs resume it, once its transcript exists.
func (s *session) start() (*process, error) {
	args := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages",
		// The VM is the sandbox, as in the TUI: tools run without asking. The prompt tool
		// still gets what Claude Code asks about in every mode, such as AskUserQuestion.
		"--permission-mode", "bypassPermissions",
		"--permission-prompt-tool", PermissionTool,
		"--plugin-dir", s.plugin,
	}
	if transcriptExists(s.id) {
		args = append(args, "--resume", s.id)
	} else {
		args = append(args, "--session-id", s.id)
	}
	if len(s.allowed) > 0 {
		// Last, since this flag takes a list of values.
		args = append(append(args, "--allowedTools"), s.allowed...)
	}
	cmd := exec.Command(s.a.claudeBinary(), args...)
	cmd.Dir = s.cwd
	cmd.Env = append(os.Environ(), s.a.opts.ClaudeEnv...)
	cmd.Stderr = s.a.opts.Log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	p := &process{cmd: cmd, stdin: stdin, exited: make(chan struct{}), mapper: newMapper()}
	go s.read(p, stdout)
	return p, nil
}

// read maps the process's output until it ends, then reaps the process and ends its turn.
func (s *session) read(p *process, out io.Reader) {
	readErr := eachLine(out, func(line []byte) {
		var m streamMessage
		if json.Unmarshal(line, &m) == nil {
			s.handle(p, m)
		}
	})
	if readErr != nil {
		_, _ = io.Copy(io.Discard, out) // let the process finish writing
	}
	err := p.cmd.Wait()
	if err == nil {
		err = readErr
	}
	s.exited(p, err)
	close(p.exited)
}

// maxLine bounds a line the shim reads: a prompt from the host carries its attachments, up
// to 15 MiB through the API, and Claude Code's output can hold a large tool result.
const maxLine = 32 << 20

// eachLine calls fn with each line of r until r ends, which is a nil error. A line over
// maxLine is skipped: one message lost, rather than every message after it.
func eachLine(r io.Reader, fn func(line []byte)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := acp.ReadLine(br, maxLine)
		switch {
		case errors.Is(err, acp.ErrLineTooLong):
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			return err
		default:
			fn(line)
		}
	}
}

// handle maps one message of the process to updates and, for its result, ends the turn.
// Messages of subagents (parent_tool_use_id set) aren't shown.
func (s *session) handle(p *process, m streamMessage) {
	if m.ParentToolUseID != nil {
		return
	}
	switch m.Type {
	case "system":
		s.system(m)
	case "stream_event":
		s.updates(p.mapper.stream(m.Event))
	case "assistant":
		s.updates(p.mapper.assistant(m.Message))
	case "user":
		s.updates(p.mapper.user(m.Message))
	case "result":
		s.result(m)
	}
}

func (s *session) system(m streamMessage) {
	switch m.Subtype {
	case "init":
		for _, srv := range m.MCPServers {
			if srv.Status != "connected" {
				fmt.Fprintf(s.a.opts.Log, "claude-acp: MCP server %s is %s\n", srv.Name, srv.Status)
			}
		}
	case "compact_boundary":
		s.updates([]acp.SessionUpdate{{Kind: acp.UpdateCompaction, Status: "completed"}})
	}
}

// result ends the running turn with what Claude Code reported.
func (s *session) result(m streamMessage) {
	s.mu.Lock()
	t := s.turn
	s.turn = nil
	cancelled := t != nil && t.cancelled
	s.mu.Unlock()
	if m.TotalCostUSD != nil {
		if cost, err := json.Marshal(map[string]any{"amount": *m.TotalCostUSD, "currency": "USD"}); err == nil {
			s.updates([]acp.SessionUpdate{{Kind: acp.UpdateUsage, Cost: cost}})
		}
	}
	if t != nil {
		t.done <- finishTurn(m, cancelled)
	}
}

// finishTurn is the prompt's outcome for a result message.
func finishTurn(m streamMessage, cancelled bool) turnResult {
	switch {
	case cancelled:
		return turnResult{resp: acp.PromptResponse{StopReason: acp.StopCancelled}}
	case m.Subtype == "error_max_turns":
		return turnResult{resp: acp.PromptResponse{StopReason: acp.StopMaxTurnRequests, Usage: m.Usage}}
	case m.IsError || strings.HasPrefix(m.Subtype, "error"):
		return turnResult{err: errors.New(resultError(m))}
	}
	stop := acp.StopEndTurn
	switch m.StopReason {
	case "max_tokens":
		stop = acp.StopMaxTokens
	case "refusal":
		stop = acp.StopRefusal
	}
	return turnResult{resp: acp.PromptResponse{StopReason: stop, Usage: m.Usage}}
}

// resultError is what a failed result says.
func resultError(m streamMessage) string {
	var text string
	if json.Unmarshal(m.Result, &text) == nil && text != "" {
		return text
	}
	var errs []string
	if json.Unmarshal(m.Errors, &errs) == nil && len(errs) > 0 {
		return strings.Join(errs, "; ")
	}
	if m.Subtype != "" {
		return "claude reported " + m.Subtype
	}
	return "claude reported an error"
}

// exited ends the turn of a process that exited. A cancelled turn ends cancelled.
func (s *session) exited(p *process, err error) {
	s.mu.Lock()
	if s.proc == p {
		s.proc = nil
	}
	t := s.turn
	s.turn = nil
	cancelled := t != nil && t.cancelled
	s.mu.Unlock()
	if t == nil {
		return
	}
	if cancelled {
		t.done <- turnResult{resp: acp.PromptResponse{StopReason: acp.StopCancelled}}
		return
	}
	msg := "claude stopped before the turn ended"
	if err != nil {
		msg += ": " + err.Error()
	}
	t.done <- turnResult{err: errors.New(msg)}
}

// updates sends session updates for the session.
func (s *session) updates(us []acp.SessionUpdate) {
	for _, u := range us {
		params, err := json.Marshal(acp.SessionNotification{SessionID: s.id, Update: u})
		if err != nil {
			continue
		}
		_ = s.a.write(rpcMessage{JSONRPC: "2.0", Method: acp.MethodSessionUpdate, Params: params})
	}
}

// process is one running Claude Code process of the session.
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	wmu    sync.Mutex
	exited chan struct{} // closed once the process is reaped and its turn ended
	mapper *mapper       // maps this process's output; only its reader uses it
}

// write sends one user message, a line of stream-json, to the process.
func (p *process) write(line []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err := p.stdin.Write(append(line, '\n'))
	return err
}

// terminate asks the process to stop, and kills it if it hasn't exited after stopGrace.
// Where SIGTERM isn't available, the process is killed at once.
func (p *process) terminate() {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.cmd.Process.Kill()
	}
	go func() {
		t := time.NewTimer(stopGrace)
		defer t.Stop()
		select {
		case <-p.exited:
		case <-t.C:
			p.cmd.Process.Kill()
		}
	}()
}

// userMessage is one user turn in Claude Code's stream-json input.
type userMessage struct {
	Type    string   `json:"type"`
	Message userBody `json:"message"`
}

type userBody struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

func validSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

// newSessionID returns a random version 4 UUID, which Claude Code takes as a session ID.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// transcriptExists reports whether Claude Code has a transcript of the session, which is
// where --resume finds it: <config dir>/projects/<project>/<session>.jsonl.
func transcriptExists(id string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".claude")
	}
	projects, err := os.ReadDir(filepath.Join(dir, "projects"))
	if err != nil {
		return false
	}
	for _, p := range projects {
		if _, err := os.Stat(filepath.Join(dir, "projects", p.Name(), id+".jsonl")); err == nil {
			return true
		}
	}
	return false
}

func parseID(raw json.RawMessage) (int64, error) {
	var id int64
	err := json.Unmarshal(raw, &id)
	return id, err
}
