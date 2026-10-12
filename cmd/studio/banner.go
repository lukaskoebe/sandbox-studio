package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// printLoginBanner prints the one-time login link as a framed, colored block on stderr, so
// it stands out among the log lines. It prints nothing unless stderr is a terminal and
// NO_COLOR is unset; the slog line with the link is logged either way.
func printLoginBanner(url string) {
	if !bannerWanted(term.IsTerminal(int(os.Stderr.Fd())), os.Getenv("NO_COLOR")) {
		return
	}
	writeLoginBanner(os.Stderr, url)
}

// bannerWanted decides whether the banner is printed. NO_COLOR follows no-color.org: any
// non-empty value turns color off.
func bannerWanted(terminal bool, noColor string) bool {
	return terminal && noColor == ""
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
	ansiGreen = "\x1b[32m"
)

// writeLoginBanner writes the banner. The link stays on one line, unbroken, so terminals
// can still open it with a click.
func writeLoginBanner(w io.Writer, url string) {
	type line struct{ text, style string }
	lines := []line{
		{"Sandbox Studio is running", ansiBold},
		{"", ""},
		{"Open this one-time login link (valid for ten minutes):", ""},
		{url, ansiBold + ansiGreen},
		{"", ""},
		{"Need another? Run: studio login-url", ansiDim},
	}
	width := 0
	for _, l := range lines {
		width = max(width, utf8.RuneCountInString(l.text))
	}
	border := func(left, right string) string {
		return ansiCyan + left + strings.Repeat("─", width+4) + right + ansiReset
	}
	var b strings.Builder
	b.WriteString("\n" + border("╭", "╮") + "\n")
	for _, l := range lines {
		text := l.text
		if l.style != "" && text != "" {
			text = l.style + text + ansiReset
		}
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(l.text))
		fmt.Fprintf(&b, "%s│%s  %s%s  %s│%s\n", ansiCyan, ansiReset, text, pad, ansiCyan, ansiReset)
	}
	b.WriteString(border("╰", "╯") + "\n\n")
	_, _ = io.WriteString(w, b.String())
}
