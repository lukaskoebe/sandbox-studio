// Package agenthook is the hook format of each harness as the guest agent sees it: the
// neutral events, how each harness's hook stdin parses into them and how answers render.
// It has no dependencies, so `studio-agent hook` stays small; package harness writes the
// configs that run it.
package agenthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// The harness-neutral hook events, as `studio-agent hook <event>` takes them.
const (
	SessionStart = "session-start" // also on resume, clear and after compaction (Source)
	UserPrompt   = "user-prompt"
	PreCompact   = "pre-compact"
	Stop         = "stop" // the end of a turn
	SessionEnd   = "session-end"
)

// Events lists every hook event.
var Events = []string{SessionStart, UserPrompt, PreCompact, Stop, SessionEnd}

// Extracts reports whether event sends the transcript to extraction.
func Extracts(event string) bool {
	return event == PreCompact || event == Stop || event == SessionEnd
}

// Limits of a hook's input.
const (
	MaxInput     = 1 << 20 // stdin
	MaxPrompt    = 8000    // characters of a prompt that are searched
	MaxSessionID = 128
	MaxString    = 4096
)

// Event is a harness's hook payload in neutral form. It names no sandbox, persona or
// environment: Studio knows the sandbox from the channel the event arrives on.
type Event struct {
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

// Result is Studio's answer to a hook: context to add to the conversation, if any.
type Result struct {
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

// VendorEvents maps the neutral events to the event names of Claude Code and Codex.
var VendorEvents = map[string]string{
	SessionStart: "SessionStart",
	UserPrompt:   "UserPromptSubmit",
	PreCompact:   "PreCompact",
	Stop:         "Stop",
	SessionEnd:   "SessionEnd",
}

func parseClaudeStyle(harness, event string, stdin []byte) (Event, error) {
	if !slices.Contains(Events, event) {
		return Event{}, fmt.Errorf("%w %q", ErrUnknownEvent, event)
	}
	if len(stdin) > MaxInput {
		return Event{}, fmt.Errorf("hook input larger than %d bytes", MaxInput)
	}
	var in claudeStyle
	if err := json.Unmarshal(stdin, &in); err != nil {
		return Event{}, fmt.Errorf("hook input: %w", err)
	}
	if in.HookEventName != "" && in.HookEventName != VendorEvents[event] {
		return Event{}, fmt.Errorf("hook %s got a %s payload", event, in.HookEventName)
	}
	ev := Event{
		Harness: harness, Event: event, SessionID: in.SessionID, Source: in.Source, Cwd: in.Cwd,
		TranscriptPath: in.TranscriptPath,
	}
	if event == UserPrompt {
		ev.Prompt = in.Prompt
	}
	return ev.clean()
}

// clean bounds and checks the fields of an event; it doesn't trust the harness's input.
func (ev Event) clean() (Event, error) {
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
	if len(ev.Cwd) > MaxString || strings.ContainsFunc(ev.Cwd, isControl) {
		ev.Cwd = ""
	}
	if len(ev.TranscriptPath) > MaxString || strings.ContainsFunc(ev.TranscriptPath, isControl) {
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
func renderClaudeStyle(r Result) []byte {
	if r.Context == "" || (r.Event != SessionStart && r.Event != UserPrompt) {
		return []byte("{}\n")
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     VendorEvents[r.Event],
		"additionalContext": r.Context,
	}})
	return append(b, '\n')
}

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

func parseOpenCode(event string, stdin []byte) (Event, error) {
	if !slices.Contains(Events, event) {
		return Event{}, fmt.Errorf("%w %q", ErrUnknownEvent, event)
	}
	if len(stdin) > MaxInput {
		return Event{}, fmt.Errorf("hook input larger than %d bytes", MaxInput)
	}
	var in opencodeHook
	if err := json.Unmarshal(stdin, &in); err != nil {
		return Event{}, fmt.Errorf("hook input: %w", err)
	}
	ev := Event{Harness: "opencode", Event: event, SessionID: in.SessionID, Source: in.Source, Cwd: in.Cwd}
	if event == UserPrompt {
		ev.Prompt = in.Prompt
	}
	if Extracts(event) {
		ev.Transcript, ev.TranscriptOffset = in.Transcript, in.TranscriptOffset
	}
	return ev.clean()
}

func renderOpenCode(r Result) []byte {
	b, _ := json.Marshal(map[string]string{"context": r.Context})
	return append(b, '\n')
}

// Format is one harness's hook format.
type Format struct {
	Name string
	// StateDirs are where the harness keeps sessions, relative to the home; hooks read
	// transcripts only inside them.
	StateDirs []string
	Parse     func(event string, stdin []byte) (Event, error)
	Render    func(Result) []byte
}

// The formats, by harness name.
var formats = map[string]Format{
	"claude": {
		Name: "claude", StateDirs: []string{".claude"},
		Parse:  func(e string, b []byte) (Event, error) { return parseClaudeStyle("claude", e, b) },
		Render: renderClaudeStyle,
	},
	"codex": {
		Name: "codex", StateDirs: []string{".codex"},
		Parse:  func(e string, b []byte) (Event, error) { return parseClaudeStyle("codex", e, b) },
		Render: renderClaudeStyle,
	},
	"opencode": {
		Name: "opencode", StateDirs: []string{".local/share/opencode", ".local/state/opencode"},
		Parse:  parseOpenCode,
		Render: renderOpenCode,
	},
}

// Lookup returns the hook format of a harness.
func Lookup(name string) (Format, bool) {
	f, ok := formats[name]
	return f, ok
}

var remoteUnsafe = regexp.MustCompile(`[^a-z0-9._/-]+`)

// NormalizeRemote turns a git remote URL into host/path, without scheme, credentials,
// port or ".git": https://user:tok@GitHub.com/a/b.git and git@github.com:a/b both become
// github.com/a/b. It returns "" for anything it can't read.
func NormalizeRemote(remote string) string {
	r := strings.TrimSpace(remote)
	if r == "" || len(r) > 1024 {
		return ""
	}
	var host, path string
	if strings.Contains(r, "://") {
		u, err := url.Parse(r)
		if err != nil {
			return ""
		}
		host, path = u.Hostname(), u.Path
	} else if at, rest, ok := strings.Cut(r, ":"); ok && !strings.Contains(at, "/") {
		// scp-like: [user@]host:path
		if i := strings.LastIndex(at, "@"); i >= 0 {
			at = at[i+1:]
		}
		host, path = at, rest
	} else {
		return "" // a local path
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	out := strings.ToLower(host + "/" + path)
	out = remoteUnsafe.ReplaceAllString(out, "-")
	out = strings.Trim(out, "/-.")
	if host == "" || path == "" || strings.Contains(out, "..") {
		return ""
	}
	return out
}
