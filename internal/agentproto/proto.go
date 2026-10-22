// Package agentproto defines the wire protocol between Studio and the guest agent.
//
// The guest agent dials the host over vsock and runs a yamux session on that
// connection; either side may open streams. Every stream starts with one JSON
// header line naming the stream kind. What follows depends on the kind:
//
//   - KindHello (guest → host): the guest writes a Hello line, then closes.
//   - KindPTY (host → guest): framed in both directions (see WriteFrame).
//   - KindTCP (host → guest): raw bytes to a guest loopback port.
//   - KindSessions, KindPorts, KindKill (host → guest): one JSON reply line.
//   - KindConfig (host → guest): the host writes a Config line; the guest applies it and
//     replies like KindKill: {} or an Error.
//   - KindHomeFiles, KindStartSession (host → guest): the host writes a HomeFiles or
//     StartSession line; the guest replies like KindKill.
//   - KindCall (guest → host): the guest writes a Call line; the host replies with one
//     CallReply line. Hooks and the MCP tools use it. The host knows the sandbox from the
//     channel, so a Call never names a sandbox, teammate or environment.
//   - KindACP (host → guest): the host writes a StartACP line; after the guest's {} the
//     stream carries an ACP agent's stdin and stdout (see acp.go).
//   - KindRun (host → guest, system VMs): the host writes a RunRequest line; the guest replies
//     with one RunResult line (see run.go).
//   - KindForwards (host → guest) and KindForward (guest → host), browser VMs only: the
//     loopback ports the browser reaches in the sandbox that drives it (see browser.go).
package agentproto

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// VsockPort is the guest-side vsock port routed to the host agent socket.
const VsockPort = 5000

// Stream kinds.
const (
	KindHello    = "hello"
	KindPTY      = "pty"
	KindTCP      = "tcp"
	KindSessions = "sessions"
	KindPorts    = "ports"
	KindKill     = "kill" // ends the tmux session named in Header.Session
	KindConfig   = "config"
	// KindHomeFiles writes harness config files into the terminal user's home.
	KindHomeFiles = "home-files"
	// KindStartSession starts a detached tmux session running an agent harness.
	KindStartSession = "start-session"
	// KindCall is a request from the guest to Studio (hooks, memory tools).
	KindCall = "call"
)

// Header opens every stream.
type Header struct {
	Kind    string `json:"kind"`
	Session string `json:"session,omitempty"` // KindPTY, KindKill: tmux session name
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
	Port    int    `json:"port,omitempty"` // KindTCP, KindForward: loopback port
}

// Config is what a sandbox gets from its environment. The host sends it whenever the guest
// connects and whenever it changes.
type Config struct {
	CA  string            `json:"ca"`  // PEM certificate of the environment's CA, to trust
	Env map[string]string `json:"env"` // secret names and their placeholders, for new shells
}

// Hello is sent by the guest when its session starts.
type Hello struct {
	Version  string `json:"version"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
}

// Session describes a tmux session in the guest.
type Session struct {
	Name     string `json:"name"`
	Attached int    `json:"attached"`
	Windows  int    `json:"windows"`
	Created  int64  `json:"created"` // unix seconds
	// Harness is set for agent sessions started with KindStartSession.
	Harness string `json:"harness,omitempty" doc:"The agent harness running in the session; empty for plain terminals"`
	// Chat is set for a chat opened in the terminal.
	Chat string `json:"chat,omitempty" doc:"The chat this session continues, when a chat was opened in the terminal"`
	// Activity is when the session's window last printed; guests before it send none.
	Activity int64 `json:"activity,omitempty" doc:"Unix seconds of the session window's latest output"`
}

// HarnessOption is the tmux user option that marks a session as an agent session.
const HarnessOption = "@studio_harness"

// ChatOption is the tmux user option that marks the session of a chat opened in the
// terminal (docs/design/chat-mode.md section 4).
const ChatOption = "@studio_chat"

// Limits of a HomeFiles request.
const (
	MaxHomeFiles     = 16
	MaxHomeFileBytes = 256 << 10
	MaxHomeFilesLine = 4 << 20 // the JSON line, escapes included
	// MaxKeysFileBytes caps a file Keys are merged into: harnesses keep history in such
	// files, so they can be larger than the files Studio writes whole.
	MaxKeysFileBytes = 8 << 20
)

// The markers around a managed block. A HomeFile with Block set replaces the text from
// BlockBegin to BlockEnd in the existing file and keeps everything else.
const (
	BlockBegin = "<!-- sandbox-studio:begin -->"
	BlockEnd   = "<!-- sandbox-studio:end -->"
)

// HomeFile is a file written into the terminal user's home, owned by that user. The write
// is atomic: a temporary file renamed over the target.
type HomeFile struct {
	Path    string `json:"path"` // relative to the home directory, slash-separated
	Content string `json:"content"`
	Mode    uint32 `json:"mode"` // permission bits, at most 0o755
	// Block marks Content as a managed block: it starts with BlockBegin and ends with
	// BlockEnd, and replaces only the existing block of the file (or is prepended).
	Block bool `json:"block,omitempty"`
	// Keys marks Content as a JSON object to merge into the file's JSON object: objects
	// are merged key by key, any other value replaces the file's, and keys Content doesn't
	// name are kept. It is for files the harness owns and Studio only sets a few keys in.
	// A guest older than the host would ignore it and replace the file, so the host sends
	// such files only to a guest of its own version (agentchan.Hub.WriteHomeFiles).
	Keys bool `json:"keys,omitempty"`
	// Defaults, with Keys, is a JSON object of keys set only where the file has none yet:
	// a starting value the harness's user may change, which Studio doesn't set back.
	Defaults string `json:"defaults,omitempty"`
}

// HomeFiles is the body of a KindHomeFiles stream.
type HomeFiles struct {
	Files []HomeFile `json:"files"`
}

// StartSession is the body of a KindStartSession stream: a detached tmux session named
// Name, in the workspace, running Command through a login shell with Env added.
type StartSession struct {
	Name    string            `json:"name"`
	Harness string            `json:"harness"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env"`
	// Chat tags the session with ChatOption.
	Chat string `json:"chat,omitempty"`
}

// Call methods. MethodHook carries a harness.HookEvent; the memory and task methods carry
// their tool arguments.
const (
	MethodHook         = "hook"
	MethodMemorySearch = "memory_search"
	MethodMemoryGet    = "memory_get"
	MethodRemember     = "remember"
	MethodShare        = "share"
	MethodCorrect      = "correct"
	MethodForget       = "forget"
	// MethodGitPushStatus and MethodGitPullRequestStatus are the git tools (docs/git-review.md).
	MethodGitPushStatus        = "git_push_status"
	MethodGitPullRequestStatus = "git_pull_request_status"
	// MethodGitCredential is git's credential helper in the sandbox asking about a host. It
	// is no MCP tool, and Studio answers it with a hint, never a credential.
	MethodGitCredential = "git_credential"

	MethodTasksList        = "tasks_list"
	MethodTaskCreate       = "task_create"
	MethodTaskUpdateStatus = "task_update_status"
	MethodTaskAddNote      = "task_add_note"

	// MethodSandboxInfo is the sandbox describing itself: its ports and its network policy.
	MethodSandboxInfo = "sandbox_info"
)

// TaskMethodPrefix starts every task method, as BrowserMethodPrefix does for browser ones.
const TaskMethodPrefix = "task"

// MaxCallLine bounds a Call or CallReply line, escapes included.
const MaxCallLine = 1 << 20

// CallSocket is where the guest agent accepts calls from processes in the guest (the
// `studio-agent hook` and `studio-agent mcp` commands) and forwards them to Studio.
const CallSocket = "/run/studio-agent/call.sock"

// Call is the body of a KindCall stream.
type Call struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// CallReply answers a Call: a result or an error.
type CallReply struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Port is a TCP port listening in the guest.
type Port struct {
	Port int `json:"port"`
}

// Error is the reply when a request fails.
type Error struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// CodeHarness marks a KindStartSession error about the harness itself: it is not installed
// or exits right away. The message is meant for the user.
const CodeHarness = "harness"

// WriteJSONLine writes v as one line of JSON.
func WriteJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ReadJSONLine reads one line of JSON into v.
func ReadJSONLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

// ErrLineTooLong is returned by ReadJSONLineLimit for a line over its limit.
var ErrLineTooLong = errors.New("JSON line too long")

// ReadJSONLineLimit reads one line of JSON of at most max bytes into v. A longer line is
// read to its end and dropped, so the next call starts at the next line.
func ReadJSONLineLimit(r *bufio.Reader, v any, max int) error {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > max {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
			return ErrLineTooLong
		}
		line = append(line, chunk...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return err
		}
	}
	return json.Unmarshal(line, v)
}

// PTY frame types.
const (
	FrameData   byte = 0
	FrameResize byte = 1
	FrameExit   byte = 2 // guest → host: the shell exited; payload is the exit code as text
)

// MaxFrame bounds a single frame payload.
const MaxFrame = 1 << 20

// Resize is the payload of a FrameResize frame.
type Resize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// WriteFrame writes [type u8][length u32 BE][payload].
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("frame too large: %d bytes", len(payload))
	}
	hdr := make([]byte, 5, 5+len(payload))
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	_, err := w.Write(append(hdr, payload...))
	return err
}

// ReadFrame reads one frame written by WriteFrame.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, errors.New("frame too large")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}
