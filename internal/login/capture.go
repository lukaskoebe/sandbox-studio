// Package login runs the subscription logins of Claude Pro/Max and ChatGPT (docs/teammates.md,
// subscription logins). A short-lived system VM runs the vendor's CLI login in a terminal, the
// user completes it in their own browser, and Studio keeps what the login yields in the vault.
// Sessions and one-shot calls get placeholders only; the gateway swaps the real values in on
// the vendor's hosts.
package login

import (
	"regexp"
	"strings"
)

// The patterns read what the CLIs print. They are matched on the terminal's text as tmux
// captures it with its lines joined (capture-pane -J), so a token wrapped over lines is whole.
// verify (S8): the token prefix and length, the device login's URL and code format, of the
// pinned Claude Code and Codex versions.
var (
	claudeToken      = regexp.MustCompile(`sk-ant-oat\d{2}-[A-Za-z0-9_-]{20,}`)
	claudeTokenExact = regexp.MustCompile(`^sk-ant-oat\d{2}-[A-Za-z0-9_-]{20,}$`)
	deviceURL        = regexp.MustCompile(`https://auth\.openai\.com/[^\s]*`)
	deviceCode       = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,6}\b`)
	anyURL           = regexp.MustCompile(`https://[^\s]+`)
)

// ClaudeToken returns the long-lived OAuth token `claude setup-token` printed in text, if it
// printed one. The first match wins: the token is shown once.
func ClaudeToken(text string) (string, bool) {
	tok := claudeToken.FindString(text)
	return tok, tok != ""
}

// ValidClaudeToken reports whether s is a long-lived token as a user pastes it: the whole of
// s, with the surrounding space trimmed.
func ValidClaudeToken(s string) bool {
	return claudeTokenExact.MatchString(strings.TrimSpace(s))
}

// DeviceLogin returns the page and the one-time code of a device login that text shows, for the
// dialog to display. Either is empty if text doesn't show it yet.
func DeviceLogin(text string) (url, code string) {
	url = strings.TrimRight(deviceURL.FindString(text), ".,;:)")
	code = deviceCode.FindString(text)
	return url, code
}

// FirstURL returns the first https address text shows, trailing punctuation cut: the page a
// Claude login asks the user to open.
func FirstURL(text string) string {
	return strings.TrimRight(anyURL.FindString(text), ".,;:)")
}
