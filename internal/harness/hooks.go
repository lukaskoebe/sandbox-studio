package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// AgentPath is where Studio mounts the guest agent in every sandbox; hook configs and MCP
// registrations run it by this path.
const AgentPath = "/opt/studio/bin/studio-agent"

// Home is the agent user's home in the guest.
const Home = "/home/agent"

// The harness-neutral hook events, as `studio-agent hook <event>` takes them.
const (
	HookSessionStart = "session-start" // also on resume, clear and after compaction (Source)
	HookUserPrompt   = "user-prompt"
	HookPreCompact   = "pre-compact"
	HookStop         = "stop" // the end of a turn
	HookSessionEnd   = "session-end"
)

// HookEvents lists every hook event.
var HookEvents = []string{HookSessionStart, HookUserPrompt, HookPreCompact, HookStop, HookSessionEnd}

// Extracts reports whether event sends the transcript to extraction.
func Extracts(event string) bool {
	return event == HookPreCompact || event == HookStop || event == HookSessionEnd
}

// Limits of a hook's input.
const (
	MaxHookInput  = 1 << 20 // stdin
	MaxPrompt     = 8000    // characters of a prompt that are searched
	MaxSessionID  = 128
	MaxHookString = 4096
)

// HookEvent is a harness's hook payload in neutral form. It names no sandbox, persona or
// environment: Studio knows the sandbox from the channel the event arrives on.
type HookEvent struct {
	Harness   string `json:"harness"`
	Event     string `json:"event"`
	SessionID string `json:"sessionId,omitempty"`
	// Source says why a session-start fired: startup, resume, clear or compact.
	Source string `json:"source,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
	// GitRemote is the normalized origin of the repository in Cwd, set by the guest agent.
	GitRemote string `json:"gitRemote,omitempty"`
	// TranscriptPath is where the harness keeps the session's transcript. The guest agent
	// reads it and sends only the part since the last extraction as Transcript; the path
	// never leaves the guest.
	TranscriptPath string `json:"-"`
	// Transcript is the text of the session since the last extraction, condensed to the
	// messages. OpenCode's plugin sends it inline, starting at TranscriptOffset.
	Transcript       string `json:"transcript,omitempty"`
	TranscriptOffset int64  `json:"transcriptOffset,omitempty"`
	// TranscriptSkipped counts bytes of a long delta that were dropped to bound the size.
	TranscriptSkipped int64 `json:"transcriptSkipped,omitempty"`
}

// HookResult is Studio's answer to a hook: context to add to the conversation, if any.
type HookResult struct {
	Event   string `json:"event"`
	Context string `json:"context,omitempty"`
}

// ErrUnknownEvent is returned for an event a harness doesn't have.
var ErrUnknownEvent = errors.New("unknown hook event")

// claudeStyle is the hook stdin of Claude Code and Codex: both use the same field names
// for the events Studio uses (https://code.claude.com/docs/en/hooks,
// https://learn.chatgpt.com/docs/hooks).
type claudeStyle struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Source         string `json:"source"`
	Prompt         string `json:"prompt"`
}

// vendorEvents maps the neutral events to the event names of Claude Code and Codex.
var vendorEvents = map[string]string{
	HookSessionStart: "SessionStart",
	HookUserPrompt:   "UserPromptSubmit",
	HookPreCompact:   "PreCompact",
	HookStop:         "Stop",
	HookSessionEnd:   "SessionEnd",
}

func parseClaudeStyle(harness, event string, stdin []byte) (HookEvent, error) {
	if !slices.Contains(HookEvents, event) {
		return HookEvent{}, fmt.Errorf("%w %q", ErrUnknownEvent, event)
	}
	if len(stdin) > MaxHookInput {
		return HookEvent{}, fmt.Errorf("hook input larger than %d bytes", MaxHookInput)
	}
	var in claudeStyle
	if err := json.Unmarshal(stdin, &in); err != nil {
		return HookEvent{}, fmt.Errorf("hook input: %w", err)
	}
	if in.HookEventName != "" && in.HookEventName != vendorEvents[event] {
		return HookEvent{}, fmt.Errorf("hook %s got a %s payload", event, in.HookEventName)
	}
	ev := HookEvent{
		Harness: harness, Event: event, SessionID: in.SessionID, Source: in.Source, Cwd: in.Cwd,
		TranscriptPath: in.TranscriptPath,
	}
	if event == HookUserPrompt {
		ev.Prompt = in.Prompt
	}
	return ev.clean()
}

// clean bounds and checks the fields of an event; it doesn't trust the harness's input.
func (ev HookEvent) clean() (HookEvent, error) {
	if len(ev.SessionID) > MaxSessionID || strings.ContainsFunc(ev.SessionID, isControl) {
		return ev, errors.New("invalid session ID")
	}
	switch ev.Source {
	case "", "startup", "resume", "clear", "compact", "fork":
	default:
		ev.Source = "other"
	}
	if utf8.RuneCountInString(ev.Prompt) > MaxPrompt {
		ev.Prompt = string([]rune(ev.Prompt)[:MaxPrompt])
	}
	if len(ev.Cwd) > MaxHookString || strings.ContainsFunc(ev.Cwd, isControl) {
		ev.Cwd = ""
	}
	if len(ev.TranscriptPath) > MaxHookString || strings.ContainsFunc(ev.TranscriptPath, isControl) {
		ev.TranscriptPath = ""
	}
	if ev.TranscriptOffset < 0 {
		ev.TranscriptOffset = 0
	}
	return ev, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// renderClaudeStyle answers Claude Code and Codex: hookSpecificOutput.additionalContext on
// session-start and user-prompt, an empty JSON object otherwise. Both accept {} as "no
// decision"; Codex refuses plain text on Stop, so the neutral answer is JSON too.
func renderClaudeStyle(r HookResult) []byte {
	if r.Context == "" || (r.Event != HookSessionStart && r.Event != HookUserPrompt) {
		return []byte("{}\n")
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     vendorEvents[r.Event],
		"additionalContext": r.Context,
	}})
	return append(b, '\n')
}

// hookCommand is the shell command line a hook config runs.
func hookCommand(harness, event string) string {
	return AgentPath + " hook " + event + " --harness " + harness
}

// hookTimeouts are the seconds a harness waits for a hook before going on without it.
// The guest agent answers well within them (HookDeadline). Codex caps SessionEnd at 3.
var hookTimeouts = map[string]int{
	HookSessionStart: 15, HookUserPrompt: 10, HookPreCompact: 15, HookStop: 15, HookSessionEnd: 3,
}

// claudeStyleHooks is the "hooks" object of Claude Code's settings and Codex's hooks.json:
// one matcher-less group per event (no matcher matches every source).
func claudeStyleHooks(harness string) map[string]any {
	hooks := map[string]any{}
	for _, event := range HookEvents {
		handler := map[string]any{"type": "command", "command": hookCommand(harness, event), "timeout": hookTimeouts[event]}
		if harness == "claude" && event == HookSessionEnd {
			handler["timeout"] = 10 // Claude Code's SessionEnd budget is 1.5 s unless raised
		}
		hooks[vendorEvents[event]] = []any{map[string]any{"hooks": []any{handler}}}
	}
	return hooks
}

// mcpServerName is what the harnesses call Studio's MCP server; tools show up as
// studio's memory_search and so on.
const mcpServerName = "studio"

// opencodeHook is the stdin the OpenCode plugin writes; the plugin is Studio's own, so the
// format is too.
type opencodeHook struct {
	SessionID        string `json:"sessionID"`
	Source           string `json:"source"`
	Prompt           string `json:"prompt"`
	Cwd              string `json:"cwd"`
	Transcript       string `json:"transcript"`
	TranscriptOffset int64  `json:"transcriptOffset"`
}

func parseOpenCode(event string, stdin []byte) (HookEvent, error) {
	if !slices.Contains(HookEvents, event) {
		return HookEvent{}, fmt.Errorf("%w %q", ErrUnknownEvent, event)
	}
	if len(stdin) > MaxHookInput {
		return HookEvent{}, fmt.Errorf("hook input larger than %d bytes", MaxHookInput)
	}
	var in opencodeHook
	if err := json.Unmarshal(stdin, &in); err != nil {
		return HookEvent{}, fmt.Errorf("hook input: %w", err)
	}
	ev := HookEvent{Harness: "opencode", Event: event, SessionID: in.SessionID, Source: in.Source, Cwd: in.Cwd}
	if event == HookUserPrompt {
		ev.Prompt = in.Prompt
	}
	if Extracts(event) {
		ev.Transcript, ev.TranscriptOffset = in.Transcript, in.TranscriptOffset
	}
	return ev.clean()
}

func renderOpenCode(r HookResult) []byte {
	b, _ := json.Marshal(map[string]string{"context": r.Context})
	return append(b, '\n')
}
