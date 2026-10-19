package teammates

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultTeammateFieldsPassTheTeammateRules(t *testing.T) {
	for _, err := range []error{
		CheckName("teammate", DefaultTeammateName), CheckRole(DefaultTeammateRole), CheckSoul(DefaultTeammateSoul),
		CheckGitName(DefaultTeammateGitName), CheckGitEmail(DefaultTeammateGitEmail),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := DefaultGitEmail(DefaultTeammateName, ""); got != DefaultTeammateGitEmail {
		t.Fatalf("a teammate named %s gets git email %s; the default is %s", DefaultTeammateName, got, DefaultTeammateGitEmail)
	}
}

func TestDefaultTeammateHarnessFitsEveryProviderKind(t *testing.T) {
	want := map[string]string{
		KindAnthropicAPI:        HarnessClaude,
		KindClaudeSubscription:  HarnessClaude,
		KindOpenAIAPI:           HarnessCodex,
		KindChatGPTSubscription: HarnessCodex,
		KindOpenAICompatible:    HarnessOpenCode,
	}
	for kind, harness := range want {
		got, model := DefaultTeammateHarness(kind)
		if got != harness {
			t.Errorf("%s: harness %q, want %q", kind, got, harness)
		}
		if !Supports(kind, got) {
			t.Errorf("%s does not support its default harness %q", kind, got)
		}
		if model != "" {
			if err := CheckModel(model); err != nil {
				t.Errorf("%s: default model: %v", kind, err)
			}
		}
		if k, _ := LookupKind(kind); k.APIKey && model == "" && kind != KindOpenAICompatible {
			t.Errorf("%s is an API-key kind without a default model", kind)
		}
	}
	if harness, _ := DefaultTeammateHarness("unknown"); harness != "" {
		t.Errorf("unknown kind has default harness %q", harness)
	}
}

// Migration 042 gives teammates that kept the earlier default soul this one.
func TestDefaultSoulMigrationWritesTheDefault(t *testing.T) {
	b, err := os.ReadFile("../store/migrations/042_default_soul.sql")
	if err != nil {
		t.Fatal(err)
	}
	if want := "SET soul = '" + strings.ReplaceAll(DefaultTeammateSoul, "'", "''") + "'"; !strings.Contains(string(b), want) {
		t.Errorf("migration 042 doesn't set the default soul:\n%s", b)
	}
}
