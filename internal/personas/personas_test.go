package personas

import (
	"errors"
	"testing"
)

func TestBaseURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://llm.example.com/v1/":     "llm.example.com",
		"https://LLM.Example.com:8443/v1": "llm.example.com",
		"https://8.8.8.8/v1":              "8.8.8.8",
	} {
		if _, host, err := BaseURL(raw); err != nil || host != want {
			t.Errorf("%s: %q %v", raw, host, err)
		}
	}
	for _, raw := range []string{
		"http://llm.example.com", "https://localhost/v1", "https://a.localhost", "https://intranet",
		"https://10.0.0.1", "https://127.0.0.1", "https://[::1]/v1", "https://169.254.169.254",
		"https://u:p@llm.example.com", "https://llm.example.com/?q=1", "https://llm.example.com/#x", "ftp://llm.example.com",
	} {
		if _, _, err := BaseURL(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted", raw)
		}
	}
}

func TestGitIdentity(t *testing.T) {
	if got := DefaultGitEmail("Ada Lovelace", "id1"); got != "ada-lovelace@agents.invalid" {
		t.Errorf("default email %q", got)
	}
	if err := CheckGitEmail(DefaultGitEmail("日本", "id1")); err != nil {
		t.Errorf("fallback email: %v", err)
	}
	for _, bad := range []string{"a\nb", "a\x00", "<x>", "a\"b", ""} {
		if CheckGitName(bad) == nil {
			t.Errorf("git name %q accepted", bad)
		}
	}
	for _, bad := range []string{"nobody", "a@b", "a b@c.com", "a@b.com\n", "a@@b.com", "ä@b.com"} {
		if CheckGitEmail(bad) == nil {
			t.Errorf("git email %q accepted", bad)
		}
	}
}

func TestHarnessTable(t *testing.T) {
	for kind, harnesses := range map[string][]string{
		KindClaudeSubscription:  {"claude"},
		KindAnthropicAPI:        {"claude", "opencode"},
		KindOpenAIAPI:           {"codex", "opencode"},
		KindOpenAICompatible:    {"opencode", "codex"},
		KindChatGPTSubscription: {"codex"},
	} {
		for _, h := range []string{HarnessClaude, HarnessOpenCode, HarnessCodex} {
			want := false
			for _, x := range harnesses {
				want = want || x == h
			}
			if Supports(kind, h) != want {
				t.Errorf("%s/%s: want %v", kind, h, want)
			}
		}
	}
	if SecretName("My Provider!", "x") != "PROVIDER_MY_PROVIDER_KEY" {
		t.Errorf("secret name %q", SecretName("My Provider!", "x"))
	}
}
