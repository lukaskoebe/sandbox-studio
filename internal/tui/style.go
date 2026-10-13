package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"
)

// The look is the terminal's own: its foreground and background, one accent for focus and
// status, and dim text for secondary facts. Colors are ANSI, so the user's palette applies, and
// Bubble Tea's renderer drops them when NO_COLOR is set.
var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleFailed = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// helpStyles draws the key bar in the same palette: keys in bold, their words dim.
func helpStyles() help.Styles {
	s := help.DefaultStyles(true)
	s.ShortKey, s.ShortDesc, s.ShortSeparator, s.Ellipsis = styleBold, styleDim, styleDim, styleDim
	s.FullKey, s.FullDesc, s.FullSeparator = styleBold, styleDim, styleDim
	return s
}

// fit makes s exactly w cells wide: padded with spaces, or cut with an ellipsis.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s + strings.Repeat(" ", w-lipgloss.Width(s))
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…" + strings.Repeat(" ", max(w-lipgloss.Width(string(runes))-1, 0))
}

// spread puts left at the start of a line of w cells and right at its end.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return fit(left, max(w-lipgloss.Width(right)-1, 0)) + " " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// pad makes a body exactly n lines long, cutting or filling with blank lines.
func pad(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}

// uptime is how long a VM has been up, in the concept's words: "up 12m", "up 2h 14m", "up 3d 4h".
func uptime(d time.Duration) string {
	minutes := int(d / time.Minute)
	switch {
	case minutes < 1:
		return "up <1m"
	case minutes < 60:
		return fmt.Sprintf("up %dm", minutes)
	case minutes < 48*60:
		return fmt.Sprintf("up %dh %dm", minutes/60, minutes%60)
	default:
		return fmt.Sprintf("up %dd %dh", minutes/(24*60), minutes/60%24)
	}
}

// matches reports whether any of the fields contains the query, ignoring case.
func matches(query string, fields ...string) bool {
	query = strings.ToLower(query)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), query) {
			return true
		}
	}
	return false
}

// plural picks the word for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
