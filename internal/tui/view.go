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
	case screenApprovals:
		return m.approvalsBody(w, h)
	case screenChats:
		return m.chatsBody(w, h)
	case screenChat:
		return m.chatBody(w, h)
	case screenActivity:
		return m.activityBody(w, h)
	case screenEntry:
		return m.entryBody(w, h)
	case screenPager:
		return m.pagerBody(w, h)
	case screenNew:
		return m.newBody(w, h)
	case screenProgress:
		return m.progressBody(w, h)
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
		return styleBold.Render("/ ") + *m.filterTarget() + styleAccent.Render("▏")
	case m.screen == screenChat && m.chats.composing:
		return m.chats.draft.line("› ", w)
	case m.screen == screenChat && m.chats.sending && m.notice.text == "":
		return styleDim.Render("Sending…")
	case m.confirm != nil:
		return styleBold.Render("Delete "+m.confirm.Name+"?") + styleDim.Render(" This removes its disks.")
	case m.notice.text != "" && m.notice.failed:
		return styleFailed.Render(m.notice.text)
	case m.notice.text != "":
		return m.notice.text
	case m.filter != "" && m.screen == screenHome:
		return styleDim.Render("filtered by “" + m.filter + "”")
	case m.offline:
		return styleDim.Render("Connection to Studio lost. Reconnecting…")
	}
	return ""
}

// helpBody lists the keys of the screen help was opened from, and says where the rest of
// Studio lives.
func (m *app) helpBody() []string {
	keys := [][2]string{
		{"enter", "open the sandbox"},
		{"s", "start, stop or resume the sandbox"},
		{"d", "delete the sandbox, after asking"},
		{"t", "the sandbox's terminal; Ctrl-] returns here"},
		{"c", "the chats with the sandbox's teammate"},
		{"n", "new sandbox: a teammate, a template and a name"},
		{"e", "switch environment"},
		{"/", "filter by name, teammate or template"},
		{"a", "approvals, from every environment"},
		{"l", "Activity: the connections and decisions log"},
		{"↑/↓, j/k", "move the cursor"},
		{"esc", "clear the filter, or go back"},
		{"q", "go back, or quit from the list"},
		{"?", "this help"},
	}
	title := "Keys"
	footer := []string{
		"Settings, providers, teammates, templates, rules, memory and the browser",
		"stay in the browser, at " + m.browserURL("/") + ".",
	}
	switch m.helpBack {
	case screenActivity, screenEntry:
		title = "Activity"
		keys = [][2]string{
			{"↑/↓, j/k", "move the cursor"},
			{"enter", "the entry, and the rule that decided it"},
			{"/", "filter by sandbox; esc clears it"},
			{"r", "print where the rules are edited, in the browser"},
			{"a", "approvals, from every environment"},
			{"esc / q", "back"},
		}
		footer = []string{
			"The log shows each sandbox's latest connections, which Studio keeps in",
			"memory, and the decisions on its requests. It updates every two seconds.",
		}
	case screenChats, screenChat:
		title = "Chat"
		keys = [][2]string{
			{"p", "write a prompt; enter sends it, ctrl+j starts a new line"},
			{"esc", "put the prompt aside, or go back"},
			{"↑/↓, j/k", "move between the tool calls and thoughts"},
			{"enter", "expand or fold the one under the cursor, or write"},
			{"pgup/pgdn", "scroll a page; b and space too"},
			{"g / G", "the start or the end of the chat"},
			{"x", "stop the running turn"},
			{"n", "start a new chat"},
			{"a", "approvals, from every environment"},
			{"esc / q", "back to the chats, then to the sandbox"},
		}
		footer = []string{
			"Permissions and questions of a chat are answered in the browser, and the",
			"approvals screen (a) prints where.",
		}
	case screenApprovals:
		title = "Approvals"
		keys = [][2]string{
			{"y / n", "allow or deny: a rule for the pattern and scope"},
			{"o / O", "allow or deny this connection once, with no rule"},
			{"1", "allow for 1 hour"},
			{"i", "ignore: refuse this time, with no rule"},
			{"p", "cycle the host pattern, then a browser request's method and path"},
			{"s", "scope: the environment, the teammate's sandboxes or this sandbox"},
			{"", "for a browser request: its browser, all browsers or sandboxes too"},
			{"x", "any port, instead of the default ports"},
			{"w", "allow the navigation's site for this browser session"},
			{"A / D", "allow or deny every host of a group"},
			{"r", "review a group's hosts one by one"},
			{"enter", "review a git push's diff, or open another kind in the browser"},
			{"esc / q", "back"},
		}
	case screenPager:
		title = "Git review"
		keys = [][2]string{
			{"y", "approve and push, or open the pull request"},
			{"n", "reject: the agent sees it on its next fetch"},
			{"i", "ignore: decide nothing, and drop the card"},
			{"↑/↓, j/k", "scroll a line"},
			{"pgup/pgdn", "scroll a page; b and space too"},
			{"g / G", "the start or the end"},
			{"esc / q", "back to the approvals"},
		}
		footer = []string{
			"A note for the agent with a rejection is written in the browser.",
		}
	}
	lines := []string{styleBold.Render(title)}
	for _, k := range keys {
		lines = append(lines, "  "+styleBold.Render(fit(k[0], 10))+" "+k[1])
	}
	lines = append(lines, "")
	for _, f := range footer {
		lines = append(lines, styleDim.Render(f))
	}
	return lines
}
