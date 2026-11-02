package teammates

import (
	"errors"
	"testing"
)

func TestBaseURL(t *testing.T) {
	for raw, want := range map[string]Endpoint{
		"https://llm.example.com/v1/":     {URL: "https://llm.example.com/v1", Host: "llm.example.com", Port: 443, Guest: "https://llm.example.com/v1"},
		"https://LLM.Example.com:8443/v1": {URL: "https://llm.example.com:8443/v1", Host: "llm.example.com", Port: 8443, Guest: "https://llm.example.com:8443/v1"},
		"https://8.8.8.8/v1":              {URL: "https://8.8.8.8/v1", Host: "8.8.8.8", Port: 443, Guest: "https://8.8.8.8/v1"},
		// This computer is host.studio.internal to sandboxes.
		"http://localhost:4532/v1": {URL: "http://localhost:4532/v1", Where: WhereHost, Host: "host.studio.internal", Port: 4532, Guest: "http://host.studio.internal:4532/v1"},
		"http://127.0.0.1/v1":      {URL: "http://127.0.0.1/v1", Where: WhereHost, Host: "host.studio.internal", Port: 80, Guest: "http://host.studio.internal:80/v1"},
		"http://[::1]:11434":       {URL: "http://[::1]:11434", Where: WhereHost, Host: "host.studio.internal", Port: 11434, Guest: "http://host.studio.internal:11434"},
		// A private address is a name under lan.studio.internal to them; a local name stays.
		"http://192.168.1.20:8080/v1": {URL: "http://192.168.1.20:8080/v1", Where: WhereLAN, Host: "192.168.1.20", Port: 8080, Guest: "http://192-168-1-20.lan.studio.internal:8080/v1"},
		"http://100.101.1.2:8000":     {URL: "http://100.101.1.2:8000", Where: WhereLAN, Host: "100.101.1.2", Port: 8000, Guest: "http://100-101-1-2.lan.studio.internal:8000"},
		"http://gpu.home.arpa:8000":   {URL: "http://gpu.home.arpa:8000", Where: WhereLAN, Host: "gpu.home.arpa", Port: 8000, Guest: "http://gpu.home.arpa:8000"},
	} {
		got, err := ParseBaseURL(raw)
		if err != nil || got != want {
			t.Errorf("%s: %+v %v", raw, got, err)
		}
		if got.TakesKey() != (got.Where != WhereLAN) {
			t.Errorf("%s: takes a key %v", raw, got.TakesKey())
		}
	}
	for _, raw := range []string{
		"http://llm.example.com", "https://localhost/v1", "https://a.localhost", "https://intranet",
		"https://10.0.0.1", "https://127.0.0.1", "https://[::1]/v1", "https://169.254.169.254",
		"https://u:p@llm.example.com", "https://llm.example.com/?q=1", "https://llm.example.com/#x", "ftp://llm.example.com",
		"http://169.254.169.254", "http://0.0.0.0:80", "http://8.8.8.8", "http://intranet:8000",
		"http://transfer.studio.internal", "http://a.localhost:80", "http://[fd00::1]:80", "http://localhost:99999",
	} {
		if _, err := ParseBaseURL(raw); !errors.Is(err, ErrInvalid) {
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

func TestCheckAvatar(t *testing.T) {
	for _, ok := range []string{"", "shape:hexagon", "🦊", "❤️", "👍🏽", "🧑🏽‍💻", "👨‍👩‍👧", "🇩🇪", "1️⃣", "©"} {
		if err := CheckAvatar(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"shape:blob", "shape:", "Ada", "1", "#", "🦊🦊", "🦊 ", " 🦊", "🦊\n", "<b>🦊</b>", "🇩", "🦊‍",
		"👍🏽🏽", "🏽", "\x00", "🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊🦊", "\xff",
	} {
		if err := CheckAvatar(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", bad)
		}
	}
}
