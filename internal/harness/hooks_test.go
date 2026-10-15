package harness

import (
	"encoding/json"
	"fmt"
	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookFixture is a hook payload as the harness sends it: testdata/hooks/<harness>/<event>.json,
// one per harness and event, shaped after the harness's documentation. They are not live
// recordings (docs/harnesses.md, "Hook fixtures").
func hookFixture(t *testing.T, harness, event string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hooks", harness, event+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	claudeSession    = "5b1c2e0a-7d3f-4c8e-9a61-2f0d4e8b7c10"
	claudeTranscript = "/home/agent/.claude/projects/-workspace/" + claudeSession + ".jsonl"
	codexSession     = "019a6c3e-8f21-7d40-b5e2-3c9a1f0e7d44"
	codexTranscript  = "/home/agent/.codex/sessions/2026/10/13/rollout-2026-10-13T09-12-44-" + codexSession + ".jsonl"
	opencodeSession  = "ses_2f9b7c1d0e4a8KQx"
)

// hookWant is the neutral event each fixture parses to. The test fills in Harness and Event.
var hookWant = map[string]map[string]HookEvent{
	"claude": {
		HookSessionStart: {SessionID: claudeSession, Source: "startup", Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookUserPrompt:   {SessionID: claudeSession, Prompt: "How do we deploy to staging?", Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookPreCompact:   {SessionID: claudeSession, Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookStop:         {SessionID: claudeSession, Cwd: "/workspace", TranscriptPath: claudeTranscript},
		HookSessionEnd:   {SessionID: claudeSession, Cwd: "/workspace", TranscriptPath: claudeTranscript},
	},
	"codex": {
		HookSessionStart: {SessionID: codexSession, Source: "startup", Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookUserPrompt:   {SessionID: codexSession, Prompt: "Run the tests", Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookPreCompact:   {SessionID: codexSession, Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookStop:         {SessionID: codexSession, Cwd: "/workspace", TranscriptPath: codexTranscript},
		HookSessionEnd:   {SessionID: codexSession, Cwd: "/workspace", TranscriptPath: codexTranscript},
	},
	"opencode": {
		HookSessionStart: {SessionID: opencodeSession, Source: "startup", Cwd: "/workspace"},
		HookUserPrompt:   {SessionID: opencodeSession, Prompt: "Which port does the API use?", Cwd: "/workspace"},
		HookPreCompact:   {SessionID: opencodeSession, Cwd: "/workspace", Transcript: "user: use pnpm\nassistant: ok\n"},
		HookStop:         {SessionID: opencodeSession, Cwd: "/workspace", Transcript: "user: more\n", TranscriptOffset: 30},
		HookSessionEnd:   {SessionID: opencodeSession, Cwd: "/workspace"},
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
