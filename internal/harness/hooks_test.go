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

// hookInputs are hook payloads per harness and event, as the vendor docs describe them.
var hookInputs = map[string]map[string]string{
	"claude": {
		HookSessionStart: `{"session_id":"s-1","transcript_path":"/home/agent/.claude/projects/-workspace/s-1.jsonl","cwd":"/workspace","hook_event_name":"SessionStart","source":"compact","model":"claude-x"}`,
		HookUserPrompt:   `{"session_id":"s-1","transcript_path":"/home/agent/.claude/projects/-workspace/s-1.jsonl","cwd":"/workspace","hook_event_name":"UserPromptSubmit","prompt":"How do we deploy to staging?"}`,
		HookPreCompact:   `{"session_id":"s-1","transcript_path":"/home/agent/.claude/projects/-workspace/s-1.jsonl","cwd":"/workspace","hook_event_name":"PreCompact","trigger":"auto","custom_instructions":""}`,
		HookStop:         `{"session_id":"s-1","transcript_path":"/home/agent/.claude/projects/-workspace/s-1.jsonl","cwd":"/workspace","hook_event_name":"Stop","stop_hook_active":false}`,
		HookSessionEnd:   `{"session_id":"s-1","transcript_path":"/home/agent/.claude/projects/-workspace/s-1.jsonl","cwd":"/workspace","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
	},
	"codex": {
		HookSessionStart: `{"session_id":"019a","transcript_path":"/home/agent/.codex/sessions/2026/10/10/rollout-019a.jsonl","cwd":"/workspace","hook_event_name":"SessionStart","model":"gpt-x","source":"startup"}`,
		HookUserPrompt:   `{"session_id":"019a","transcript_path":"/home/agent/.codex/sessions/2026/10/10/rollout-019a.jsonl","cwd":"/workspace","hook_event_name":"UserPromptSubmit","model":"gpt-x","turn_id":"t1","prompt":"Run the tests"}`,
		HookPreCompact:   `{"session_id":"019a","transcript_path":"/home/agent/.codex/sessions/2026/10/10/rollout-019a.jsonl","cwd":"/workspace","hook_event_name":"PreCompact","model":"gpt-x","turn_id":"t2","trigger":"manual"}`,
		HookStop:         `{"session_id":"019a","transcript_path":"/home/agent/.codex/sessions/2026/10/10/rollout-019a.jsonl","cwd":"/workspace","hook_event_name":"Stop","model":"gpt-x","turn_id":"t2","stop_hook_active":false,"last_assistant_message":"Done."}`,
		HookSessionEnd:   `{"session_id":"019a","transcript_path":"/home/agent/.codex/sessions/2026/10/10/rollout-019a.jsonl","cwd":"/workspace","hook_event_name":"SessionEnd","model":"gpt-x","reason":"other"}`,
	},
	"opencode": {
		HookSessionStart: `{"sessionID":"ses_1","source":"startup","cwd":"/workspace"}`,
		HookUserPrompt:   `{"sessionID":"ses_1","prompt":"Which port does the API use?","cwd":"/workspace"}`,
		HookPreCompact:   `{"sessionID":"ses_1","cwd":"/workspace","transcript":"user: use pnpm\nassistant: ok\n","transcriptOffset":0}`,
		HookStop:         `{"sessionID":"ses_1","cwd":"/workspace","transcript":"user: more\n","transcriptOffset":30}`,
		HookSessionEnd:   `{"sessionID":"ses_1","cwd":"/workspace"}`,
	},
}

// TestHookGolden parses each harness's payload for every event, renders the answer with
// and without context, and compares with testdata/hooks/<harness>.golden. Run with -update
// after a deliberate change.
func TestHookGolden(t *testing.T) {
	for _, h := range all {
		t.Run(h.Name(), func(t *testing.T) {
			var b strings.Builder
			for _, event := range agenthook.Events {
				in := hookInputs[h.Name()][event]
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
				os.MkdirAll(filepath.Dir(golden), 0o755)
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
	long := `{"prompt":"` + strings.Repeat("x", agenthook.MaxPrompt+10) + `","hook_event_name":"UserPromptSubmit","persona":"other","sandbox":"other"}`
	ev, err := (Claude{}).ParseHook(HookUserPrompt, []byte(long))
	if err != nil || len(ev.Prompt) != agenthook.MaxPrompt {
		t.Fatalf("prompt: %v %d", err, len(ev.Prompt))
	}
}
