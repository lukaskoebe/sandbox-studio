package harness

import "github.com/lukaskoebe/sandbox-studio/internal/agenthook"

// AgentPath is where Studio mounts the guest agent in every sandbox; hook configs and MCP
// registrations run it by this path.
const AgentPath = "/opt/studio/bin/studio-agent"

// Home is the agent user's home in the guest.
const Home = "/home/agent"

// The hook types and events live in agenthook, which the guest agent uses without the
// rest of this package.
type (
	HookEvent  = agenthook.Event
	HookResult = agenthook.Result
)

const (
	HookSessionStart = agenthook.SessionStart
	HookUserPrompt   = agenthook.UserPrompt
	HookPreCompact   = agenthook.PreCompact
	HookStop         = agenthook.Stop
	HookSessionEnd   = agenthook.SessionEnd
)

// hookCommand is the shell command line a hook config runs.
func hookCommand(harness, event string) string {
	return AgentPath + " hook " + event + " --harness " + harness
}

// hookTimeouts are the seconds a harness waits for a hook before going on without it.
// The guest agent answers well within them (agentcall.HookDeadline). Codex caps SessionEnd at 3.
var hookTimeouts = map[string]int{
	HookSessionStart: 15, HookUserPrompt: 10, HookPreCompact: 15, HookStop: 15, HookSessionEnd: 3,
}

// claudeStyleHooks is the "hooks" object of Claude Code's settings and Codex's hooks.json:
// one matcher-less group per event (no matcher matches every source).
func claudeStyleHooks(harness string) map[string]any {
	hooks := map[string]any{}
	for _, event := range agenthook.Events {
		handler := map[string]any{"type": "command", "command": hookCommand(harness, event), "timeout": hookTimeouts[event]}
		if harness == "claude" && event == HookSessionEnd {
			handler["timeout"] = 10 // Claude Code's SessionEnd budget is 1.5 s unless raised
		}
		hooks[agenthook.VendorEvents[event]] = []any{map[string]any{"hooks": []any{handler}}}
	}
	return hooks
}

// mcpServerName is what the harnesses call Studio's MCP server.
const mcpServerName = "studio"

func hookFormat(name string) agenthook.Format {
	f, _ := agenthook.Lookup(name)
	return f
}
