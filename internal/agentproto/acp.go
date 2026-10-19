package agentproto

// KindACP (host → guest): the host writes a StartACP line; the guest starts the harness's
// ACP agent and replies with {} or an Error (CodeHarness when the adapter is missing,
// CodeFull when MaxACPProcesses agents already run).
// After {} the stream carries the agent's stdin and stdout as raw bytes: newline-delimited
// JSON-RPC, read and written by internal/acp on the host. The guest kills the process when
// the stream closes and closes the stream when the process exits.
const KindACP = "acp"

// Limits of ACP processes.
const (
	// MaxACPProcesses is how many ACP agents a sandbox runs at once, one per chat.
	MaxACPProcesses = 4
	// MaxACPLog caps the agent's stderr log, /run/studio-agent/acp-<chat>.log.
	MaxACPLog = 256 << 10
)

// CodeFull marks a KindACP error: the sandbox already runs MaxACPProcesses agents. A slot
// frees once an agent's process exits, which can be a moment after its stream closed.
const CodeFull = "full"

// StartACP is the body of a KindACP stream: the ACP agent of one chat, run in the
// workspace through a login shell with Env added, like StartSession. Env holds
// placeholders and fixed paths only.
type StartACP struct {
	Chat    string            `json:"chat"` // the chat ID, a session-name-like string
	Harness string            `json:"harness"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env"`
}
