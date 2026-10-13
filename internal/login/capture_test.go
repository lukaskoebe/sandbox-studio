package login

import "testing"

const tokenText = "sk-ant-oat01-" + "AbCdEfGhIjKlMnOpQrStUvWx_-123456"

// The token is found in terminal text wherever it is, and nothing else counts as one.
func TestClaudeToken(t *testing.T) {
	text := "Opening https://claude.ai/oauth/authorize?code=true&client_id=x in your browser.\n" +
		"Paste code here > \n\n✓ Long-lived authentication token created successfully!\n\n" +
		"Your OAuth token (valid for 1 year):\n\n" + tokenText + "\n\nStore this token securely.\n"
	if tok, ok := ClaudeToken(text); !ok || tok != tokenText {
		t.Fatalf("token %q, %v", tok, ok)
	}
	for name, text := range map[string]string{
		"none":        "Paste code here >",
		"too short":   "sk-ant-oat01-short",
		"other key":   "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz123456",
		"wrong words": "oat01-AbCdEfGhIjKlMnOpQrStUvWx",
	} {
		if tok, ok := ClaudeToken(text); ok {
			t.Errorf("%s: matched %q", name, tok)
		}
	}
}

func TestValidClaudeToken(t *testing.T) {
	if !ValidClaudeToken("  " + tokenText + "\n") {
		t.Fatal("a pasted token was refused")
	}
	for _, s := range []string{"", tokenText + " extra", "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz123456", "sk-ant-oat01-x"} {
		if ValidClaudeToken(s) {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestDeviceLoginAndFirstURL(t *testing.T) {
	text := "Welcome to Codex.\nOpen https://auth.openai.com/codex/device and enter this code:\n  ABCD-EFGH.\n"
	url, code := DeviceLogin(text)
	if url != "https://auth.openai.com/codex/device" || code != "ABCD-EFGH" {
		t.Fatalf("device login %q %q", url, code)
	}
	if url, code := DeviceLogin("Starting login…"); url != "" || code != "" {
		t.Fatalf("nothing shown, got %q %q", url, code)
	}
	if got := FirstURL("Visit (https://claude.ai/oauth/authorize?x=1)."); got != "https://claude.ai/oauth/authorize?x=1" {
		t.Fatalf("first URL %q", got)
	}
}
