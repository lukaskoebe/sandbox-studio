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
	claudeToken      = regexp.MustCompile(`sk-ant-oat\d{2}-[A-Za-z0-9_-]*`)
	claudeTokenExact = regexp.MustCompile(`^sk-ant-oat\d{2}-[A-Za-z0-9_-]{20,}$`)
	deviceURL        = regexp.MustCompile(`https://auth\.openai\.com/[^\s]*`)
	deviceCode       = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,6}\b`)
	anyURL           = regexp.MustCompile(`https://[^\s]+`)
	// errorLine is a line in which a CLI reports a failure, such as Claude Code's
	// "OAuth error: …" after a wrong code. verify (S8): the wording of the pinned Claude Code.
	errorLine = regexp.MustCompile(`(?im)^.*\b(error|invalid|failed)\b.*$`)
)

// errorLines counts the lines of text that report a failure.
func errorLines(text string) int {
	return len(errorLine.FindAllStringIndex(text, -1))
}

// urlChars is a line that can continue a URL or a token Claude Code broke over lines.
var urlChars = regexp.MustCompile(`^[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+$`)

// unwrap returns the match at loc in text with the lines that continue it. Claude Code breaks
// long lines itself, with newlines tmux can't join, so a URL or a token that reaches the end of
// its line goes on in the next lines that consist of URL characters only.
func unwrap(text string, loc []int) string {
	out := text[loc[0]:loc[1]]
	rest := text[loc[1]:]
	for {
		line, after, found := strings.Cut(rest, "\n")
		if strings.TrimSpace(line) != "" || !found {
			return out
		}
		next, _, _ := strings.Cut(after, "\n")
		next = strings.TrimSpace(next)
		if !urlChars.MatchString(next) {
			return out
		}
		out += next
		rest = after[strings.Index(after, next)+len(next):]
	}
}

// ClaudeToken returns the long-lived OAuth token `claude setup-token` printed in text, if it
// printed one. The first match wins: the token is shown once.
func ClaudeToken(text string) (string, bool) {
	for _, loc := range claudeToken.FindAllStringIndex(text, -1) {
		if tok := unwrap(text, loc); claudeTokenExact.MatchString(tok) {
			return tok, true
		}
	}
	return "", false
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
	loc := anyURL.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	return strings.TrimRight(unwrap(text, loc), ".,;:)")
}
