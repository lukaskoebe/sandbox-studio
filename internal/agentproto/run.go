package agentproto

// KindRun (host → guest, system VMs only): the host writes a RunRequest line; the guest runs
// the command as the agent user in its home, and replies with one RunResult line or an Error.
// Studio runs the CLI logins and one-shot calls in the system VMs this way (docs/teammates.md,
// subscription logins). Only the programs in RunCommands run.
const KindRun = "run"

// Limits of a run. MaxRunLine bounds a request or reply line, escapes included.
const (
	MaxRunArgs    = 32
	MaxRunArg     = 16 << 10
	MaxRunEnv     = 16
	MaxRunEnvLen  = 4 << 10
	MaxRunStdin   = 1 << 20
	MaxRunOutput  = 1 << 20 // stdout and stderr each, before the JSON encoding
	MaxRunTimeout = 15 * 60 // seconds
	MaxRunLine    = 8 << 20
)

// RunCommands are the programs a run may start, by their bare names. The guest refuses any
// other program, whatever the host asks.
var RunCommands = []string{"claude", "codex", "tmux", "cat"}

// RunRequest is the body of a KindRun stream.
type RunRequest struct {
	// Args is the command line; Args[0] is one of RunCommands.
	Args []string `json:"args"`
	// Env is added to the agent's environment. It holds secret placeholders only, never a
	// credential (docs/harnesses.md).
	Env   map[string]string `json:"env,omitempty"`
	Stdin string            `json:"stdin,omitempty"`
	// TimeoutSec ends the command, and the stream with an error, after this many seconds.
	TimeoutSec int `json:"timeoutSec"`
}

// RunResult is the reply of a KindRun stream. A non-zero exit code is a result, not an error.
type RunResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
	// Truncated is set when stdout or stderr was longer than MaxRunOutput and was cut.
	Truncated bool `json:"truncated,omitempty"`
}
