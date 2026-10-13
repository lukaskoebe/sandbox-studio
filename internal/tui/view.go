package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The size the frame falls back to before the terminal reports its own.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

// View draws the screen. The TUI owns the alternate screen while it runs.
func (m *app) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	return v
}

// frame lays the screen out in the terminal's size: a header and a rule, the body, a rule, the
// key bar and the notice line. The body gets the rows the others leave.
func (m *app) frame() string {
	w, h := m.size()
	m.keys.SetWidth(w)
	rule := styleDim.Render(strings.Repeat("─", w))
	lines := []string{m.header(w), rule}
	lines = append(lines, pad(m.body(w, max(h-5, 1)), max(h-5, 1))...)
	lines = append(lines, rule, m.keyBar(), m.noticeLine(w))
	for i, line := range lines {
		lines[i] = lipgloss.NewStyle().MaxWidth(w).Render(line)
	}
	return strings.Join(lines, "\n")
}

// header is the product, the environment on show, and the approvals waiting in every
// environment, which count from every screen.
func (m *app) header(w int) string {
	left := styleBold.Render("Sandbox Studio") + styleDim.Render(" · ") + m.env.Name
	var right string
	if n := len(m.approvals); n == 0 {
		right = styleDim.Render("no approvals")
	} else {
		right = styleAccent.Render(fmt.Sprintf("● %d %s", n, plural(n, "approval", "approvals")))
	}
	return spread(left, right, w)
}

// body is the screen's lines, at most h of them.
func (m *app) body(w, h int) []string {
	switch m.screen {
	case screenDetail:
		return m.detailBody(w, h)
	case screenEnvs:
		return m.envsBody(w, h)
	case screenHelp:
		return m.helpBody()
	}
	return m.homeBody(w, h)
}

// keyBar is the keys that apply here, in the key bar's style.
func (m *app) keyBar() string {
	return m.keys.ShortHelpView(m.bindings())
}

// noticeLine is the line under the key bar: the filter while it is typed, a question that is
// waiting for an answer, the last result, or the state of the connection.
func (m *app) noticeLine(w int) string {
	switch {
	case m.filtering:
		return styleBold.Render("/ ") + m.filter + styleAccent.Render("▏")
	case m.confirm != nil:
		return styleBold.Render("Delete "+m.confirm.Name+"?") + styleDim.Render(" This removes its disks.")
	case m.notice.text != "" && m.notice.failed:
		return styleFailed.Render(m.notice.text)
	case m.notice.text != "":
		return m.notice.text
	case m.filter != "":
		return styleDim.Render("filtered by “" + m.filter + "”")
	case m.offline:
		return styleDim.Render("Connection to Studio lost. Reconnecting…")
	}
	return ""
}

// helpBody lists the keys, and says where the rest of Studio lives.
func (m *app) helpBody() []string {
	keys := [][2]string{
		{"enter", "open the sandbox"},
		{"s", "start, stop or resume the sandbox"},
		{"d", "delete the sandbox, after asking"},
		{"c", "the sandbox's chats, in the browser"},
		{"n", "new sandbox, in the browser"},
		{"e", "switch environment"},
		{"/", "filter by name, teammate or template"},
		{"↑/↓, j/k", "move the cursor"},
		{"esc", "clear the filter, or go back"},
		{"q", "go back, or quit from the list"},
		{"?", "this help"},
	}
	lines := []string{styleBold.Render("Keys")}
	for _, k := range keys {
		lines = append(lines, "  "+styleBold.Render(fit(k[0], 10))+" "+k[1])
	}
	lines = append(lines, "",
		styleDim.Render("Settings, providers, teammates, templates, rules, memory, the browser and"),
		styleDim.Render("Activity stay in the browser, at "+m.browserURL("/")+"."))
	return lines
}
