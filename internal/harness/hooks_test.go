package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// hookFixture is a hook payload as the harness sends it: testdata/hooks/<harness>/<event>.json,
// one per harness and event, plus variants such as session-start-compact. They are live
// recordings of the pinned versions against a fake model endpoint (spike S7, docs/harnesses.md,
// "Hook fixtures"), except OpenCode's session-end, which the plugin never sends.
func hookFixture(t *testing.T, harness, event string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hooks", harness, event+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	claudeSession    = "998237e6-3c99-46a1-9f0f-fff7845b26fc"
	claudeTranscript = "/home/agent/.claude/projects/-workspace/" + claudeSession + ".jsonl"
	codexSession     = "01a14a09-0dd8-7250-9acd-82991f22cd94"
	codexTranscript  = "/home/agent/.codex/sessions/2026/10/17/rollout-2026-10-17T13-24-29-" + codexSession + ".jsonl"
	opencodeSession  = "ses_eb5f524b2ffeSfgwe2MdoL2d4p"
)

// hookWant is the neutral event each fixture parses to. The test fills in Harness and Event.
var hookWant = map[string]map[string]HookEvent{
	"claude": {
		HookSessionStart: {SessionID: claudeSession, Source: "startup", Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookUserPrompt:   {SessionID: claudeSession, Prompt: "please run echo-please", Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookPreCompact:   {SessionID: claudeSession, Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookStop:         {SessionID: claudeSession, Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookSessionEnd:   {SessionID: claudeSession, Cwd: "/workspace", TranscriptPath: claudeTranscript},
	},
	"codex": {
		HookSessionStart: {SessionID: codexSession, Source: "startup", Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookUserPrompt:   {SessionID: codexSession, Prompt: "please run echo-please", Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookPreCompact:   {SessionID: codexSession, Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookStop:         {SessionID: codexSession, Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookSessionEnd:   {SessionID: codexSession, Cwd: "/workspace", TranscriptPath: codexTranscript},
	},
	"opencode": {
		HookSessionStart: {SessionID: opencodeSession, Source: "startup", Cwd: "/workspace"},
		HookUserPrompt:   {SessionID: opencodeSession, Prompt: "please run echo-please again", Cwd: "/workspace"},
		HookPreCompact:   {SessionID: opencodeSession, Cwd: "/workspace", Transcript: "user: \"please run echo-please\"\nassistant: Done: it printed hi.\n"},
		HookStop: {SessionID: opencodeSession, Cwd: "/workspace",
			Transcript: "user: \"please run echo-please\"\nassistant: Done: it printed hi.\nassistant: Echo test\nuser: please run echo-please again\nassistant: Done: it printed hi.\n"},
		HookSessionEnd: {SessionID: opencodeSession, Cwd: "/workspace"},
	},
}

// TestHookFixtures runs every fixture through its harness's adapter and checks the neutral
// event it parses to.
func TestHookFixtures(t *testing.T) {
	for _, h := range all {
		for _, event := range agenthook.Events {
			t.Run(h.Name()+"/"+event, func(t *testing.T) {
				want, ok := hookWant[h.Name()][event]
				if !ok {
					t.Fatal("no expected event")
				}
				want.Harness, want.Event = h.Name(), event
				got, err := h.ParseHook(event, []byte(hookFixture(t, h.Name(), event)))
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("parsed %+v, want %+v", got, want)
				}
			})
		}
	}
}

// TestHookGolden parses each harness's payload for every event, renders the answer with
// and without context, and compares with testdata/hooks/<harness>.golden. Run with -update
// after a deliberate change.
func TestHookGolden(t *testing.T) {
	for _, h := range all {
		t.Run(h.Name(), func(t *testing.T) {
			var b strings.Builder
			for _, event := range agenthook.Events {
				in := hookFixture(t, h.Name(), event)
				ev, err := h.ParseHook(event, []byte(in))
				if err != nil {
					t.Fatalf("%s: %v", event, err)
				}
				parsed, _ := json.Marshal(ev)
				fmt.Fprintf(&b, "== %s\n-- stdin\n%s\n-- parsed (transcript path %q)\n%s\n", event, in, ev.TranscriptPath, parsed)
				fmt.Fprintf(&b, "-- neutral\n%s", h.RenderHookResponse(HookResult{Event: event}))
				fmt.Fprintf(&b, "-- with context\n%s", h.RenderHookResponse(HookResult{Event: event, Context: "Remember: \"pnpm\" <not npm>"}))
			}
			golden := filepath.Join("testdata", "hooks", h.Name()+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if b.String() != string(want) {
				t.Errorf("%s differs; run with -update and review the diff:\n%s", golden, b.String())
			}
		})
	}
}

// TestHookFixtureVariants: the recorded session-start after a compaction carries source
// "compact" in every harness, and the hooks of codex app-server's titling thread, which has no
// transcript (transcript_path null), parse as ephemeral.
func TestHookFixtureVariants(t *testing.T) {
	for _, h := range all {
		ev, err := h.ParseHook(HookSessionStart, []byte(hookFixture(t, h.Name(), "session-start-compact")))
		if err != nil || ev.Source != "compact" || ev.Ephemeral {
			t.Errorf("%s: compact session start parsed %+v, %v", h.Name(), ev, err)
		}
	}
	ev, err := (Codex{}).ParseHook(HookUserPrompt, []byte(hookFixture(t, "codex", "user-prompt-title")))
	if err != nil || !ev.Ephemeral || ev.TranscriptPath != "" {
		t.Errorf("codex title thread parsed %+v, %v", ev, err)
	}
}

func TestParseHookRefuses(t *testing.T) {
	for _, h := range all {
		if _, err := h.ParseHook("bogus", []byte(`{}`)); err == nil {
			t.Errorf("%s: unknown event parsed", h.Name())
		}
		if _, err := h.ParseHook(HookStop, []byte(`not json`)); err == nil {
			t.Errorf("%s: bad JSON parsed", h.Name())
		}
		if _, err := h.ParseHook(HookStop, []byte(strings.Repeat(" ", agenthook.MaxInput+1))); err == nil {
			t.Errorf("%s: oversized input parsed", h.Name())
		}
		if _, err := h.ParseHook(HookStop, []byte(`{"session_id":"a\nb","sessionID":"a\nb"}`)); err == nil {
			t.Errorf("%s: control characters in the session ID", h.Name())
		}
	}
	// A payload of another event is refused rather than misread.
	if _, err := (Claude{}).ParseHook(HookUserPrompt, []byte(`{"hook_event_name":"Stop"}`)); err == nil {
		t.Error("claude: mismatched event parsed")
	}
	// Prompts are bounded; fields Studio decides on are never taken from the payload.
	long := `{"prompt":"` + strings.Repeat("x", agenthook.MaxPrompt+10) + `","hook_event_name":"UserPromptSubmit","teammate":"other","sandbox":"other"}`
	ev, err := (Claude{}).ParseHook(HookUserPrompt, []byte(long))
	if err != nil || len(ev.Prompt) != agenthook.MaxPrompt {
		t.Fatalf("prompt: %v %d", err, len(ev.Prompt))
	}
}

// TestCodexHookTrust checks codexHookTrust against the trusted_hash values Codex 0.162.1 wrote
// to config.toml when its TUI was told to trust these hooks (S7): the hooks.json of the
// recording, whose commands ran a recorder instead of studio-agent.
func TestCodexHookTrust(t *testing.T) {
	hooks := claudeStyleHooks(teammates.HarnessCodex)
	for _, groups := range hooks {
		handler := groups.([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		handler["command"] = strings.Replace(handler["command"].(string), AgentPath+" hook ", "/home/agent/s7/rec.sh ", 1)
	}
	got, err := codexHookTrust(hooks)
	if err != nil {
		t.Fatal(err)
	}
	for event, hash := range map[string]string{
		"session_start":      "5e44c2034822f5985ec4151c1dc75c12b65582cd663c1f959fa799fb9bd27d5a",
		"user_prompt_submit": "bf397471d1da0957902012883e66bf76b8d4c1b43c97bcd7d72f39d2dc813dab",
		"pre_compact":        "f817d4889b44b7be637c1c64faeb2bdcd06dd383bf144bfe2cfc60a022828658",
		"stop":               "613a8c80267cc374a133e894fbf8eae728fab2749287133ac5b05f7f8362b85f",
		"session_end":        "83ff0d3f36d34ab2d67d57c36470b41b2d86a33515eea901ec5d7acc420d1f6d",
	} {
		want := fmt.Sprintf("[hooks.state.\"/home/agent/.codex/hooks.json:%s:0:0\"]\ntrusted_hash = \"sha256:%s\"\n", event, hash)
		if !strings.Contains(got, want) {
			t.Errorf("%s: no %q in\n%s", event, want, got)
		}
	}
}
