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

// Claude Code breaks a long URL or token over lines itself; the lines that continue it are
// joined, and the text after it is not.
func TestUnwrapsBrokenLines(t *testing.T) {
	text := "Open the page below on your computer and sign in.\n\n" +
		"https://claude.com/cai/oauth/authorize?cod  \n" +
		"  e=true&client_id=9d1c&response_type=code\n" +
		"&state=abc\n\nPaste code here if prompted >\n"
	if got := FirstURL(text); got != "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c&response_type=code&state=abc" {
		t.Fatalf("URL %q", got)
	}
	half := len(tokenText) / 2
	text = "Your OAuth token (valid for 1 year):\n\n" + tokenText[:half] + "\n" + tokenText[half:] + "\n\nStore this token securely.\n"
	if tok, ok := ClaudeToken(text); !ok || tok != tokenText {
		t.Fatalf("token %q, %v", tok, ok)
	}
	// A URL followed by words on the next line stays as it is.
	if got := FirstURL("Visit https://claude.ai/x\nand sign in"); got != "https://claude.ai/x" {
		t.Fatalf("URL %q", got)
	}
}
