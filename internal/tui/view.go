package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The size the frame falls back to before the terminal reports its own.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

// View draws the screen. The TUI owns the alternate screen while it runs. The visual look takes
// the mouse's clicks and wheel (cell motion, so holding shift keeps the terminal's selection).
func (m *app) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	v.Cursor = m.paneCursor()
	if !m.look.plain {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// bodyTop is the row the body starts on: under the crumb row and its rule.
const bodyTop = 2

// frame lays the screen out in the terminal's size: the crumb row and a rule, the sidebar and
// the body, a rule, the key bar and the notice line (frame.go). Drawing it also records where
// the rows lead, for the mouse.
func (m *app) frame() string {
	w, h := m.size()
	m.keys.SetWidth(w)
	m.mouse.rows = m.mouse.rows[:0]
	m.mouse.keys = m.mouse.keys[:0]
	lines := m.mainFrame(w, h)
	if !m.shellZoomed() {
		lines = append(lines, m.keyBar(w, len(lines)), m.noticeLine(w))
	}
	for i, line := range lines {
		lines[i] = lipgloss.NewStyle().MaxWidth(w).Render(line)
	}
	return strings.Join(lines, "\n")
}

// body is the screen's lines, at most h of them.
func (m *app) body(w, h int) []string {
	switch m.screen {
	case screenEnvs:
		return m.envsBody(w, h)
	case screenHelp:
		return m.helpBody()
	case screenActivity:
		return m.activityBody(w, h)
	case screenEntry:
		return m.entryBody(w, h)
	case screenNew:
		return m.newBody(w, h)
	case screenProgress:
		return m.progressBody(w, h)
	case screenCheckpoints:
		return m.checkpointsBody(w, h)
	case screenSettings:
		return m.settingsBody(w, h)
	case screenComputers:
		return m.computersBody(w, h)
	}
	return m.homeBody(w, h)
}

// keyBar is the keys that apply here, in the key bar's style: grouped in the visual look, where
// it records them on row y for the mouse.
func (m *app) keyBar(w, y int) string {
	if !m.look.plain {
		line, spans := m.groupedKeys(m.bindings(), w)
		m.hitKeys(y, 0, spans)
		return line
	}
	return m.keys.ShortHelpView(m.bindings())
}

// noticeLine is the line under the key bar: the filter while it is typed, a question that is
// waiting for an answer, the last result, or the state of the connection.
func (m *app) noticeLine(w int) string {
	switch {
	case m.filtering:
		return styleBold.Render("/ ") + *m.filterTarget() + styleAccent.Render("▏")
	case m.typing != nil:
		return m.typing.view(w)
	case m.confirm != nil:
		return styleBold.Render("Delete "+m.confirm.Name+"?") + styleDim.Render(" This removes its disks.  ") +
			m.confirmRow("delete:"+m.confirm.ID, "Delete")
	case m.checkpoints.restoring != nil && m.screen == screenCheckpoints:
		return styleBold.Render("Restore "+m.checkpointSandbox().Name+" to "+m.checkpoints.restoring.Name+"?") +
			styleDim.Render(" What changed since is lost.  ") + m.confirmRow("restore:"+m.checkpoints.restoring.ID, "Restore")
	case m.inbox.pushing != "":
		a, _ := m.approvalByID(m.inbox.pushing)
		verb := "Push"
		if a.Git != nil && a.Git.PullRequest != nil {
			verb = "Open"
		}
		row := m.confirmRow(pushOwner(a.ID), verb)
		return styleBold.Render(clip(pushQuestion(a), max(w-lipgloss.Width(row)-2, 10))) + "  " + row
	case m.notice.text != "" && m.notice.failed:
		return styleFailed.Render(m.notice.text)
	case m.notice.text != "":
		return m.notice.text
	case m.paneFocused():
		x, _ := m.paneTarget()
		return styleDim.Render(clip("Typing into "+x.title()+". ctrl+] gives the keys back.", w))
	case m.filter != "" && m.screen == screenMain:
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
		{"↑/↓, j/k", "move the cursor"},
		{"enter", "open the row: Home, a teammate, a thread, a computer, a place"},
		{"\\", "the sidebar and back; below 100 columns it slides over the body"},
		{"tab", "a thread and its computer; on Home, the next request"},
		{"alt+z", "the pane with the keys takes the whole screen, and back"},
		{"+", "a new thread with the teammate under the cursor, or a computer"},
		{"→ ←", "show or hide a teammate's older threads"},
		{"u", "undo the last network decision, for a minute after it"},
		{"e / /", "switch environment; filter by name, teammate or template"},
		{"l / ,", "Activity; Settings"},
		{"esc", "back a place, Home last; it clears the filter first"},
		{"? / q", "this help; quit, from Home and the sidebar"},
	}
	title := "Keys"
	footer := []string{
		"Providers, teammates, templates, rules, memory and the browser are",
		"edited in the browser, at " + m.browserURL("/") + "; Settings (,) says where.",
	}
	switch {
	case m.helpBack == screenMain && m.helpPart == "home":
		title = "Home"
		keys = [][2]string{
			{"↑/↓, j/k", "move between the rows; tab: the next request, around the end"},
			{"enter", "review a push; open a thread, a computer, or a request's source"},
			{"y / n / i", "allow, deny or ignore; a network rule takes the pattern and scope"},
			{"o / O / 1", "allow or deny once, with no rule; 1 allows for an hour"},
			{"p / s / x", "cycle the host pattern; the scope; any port, not the default ones"},
			{"w", "allow a navigation's site for this browser session"},
			{"A / D / r", "a group: allow or deny every host; r reviews them one by one"},
			{"y / a / n", "an agent's permission: allow once, always or deny; 1-9 its choices"},
			{"y / F", "a push: y asks once more, then pushes; F force-pushes a rewrite"},
			{"r / n / i", "a push: ask for changes in a note; reject it; ignore it"},
			{"u", "undo the last network decision, for a minute after it"},
			{"←/→ space", "move over the request's choices; space or enter presses one"},
			{"pgup/pgdn", "scroll the reading pane"},
			{"\\", "the sidebar: teammates, their threads and computers; \\ comes back"},
			{"+ / e / /", "new computer; switch environment; filter the sidebar"},
			{"l / ,", "Activity; Settings"},
			{"? / q", "this help; quit"},
		}
	case m.helpBack == screenMain && m.helpPart == "thread":
		title = "Thread"
		keys = [][2]string{
			{"p / enter", "write a prompt; enter sends it, ctrl+j starts a new line"},
			{"esc", "put the prompt aside, or go back a place"},
			{"↑/↓, j/k", "move between the tool calls and thoughts"},
			{"enter", "expand or fold the one under the cursor"},
			{"pgup/pgdn", "scroll a page; b and space too"},
			{"g / G", "the start or the end of the thread"},
			{"x", "stop the running turn"},
			{"y / a / n", "the permission it waits on: allow once, always or reject"},
			{"1-9", "answer the permission or question waiting: a numbered choice"},
			{"i / r", "ignore the permission or skip the question; r types an answer"},
			{"card", "a request above the composer takes its own keys, as on Home"},
			{"+", "start a new thread with the teammate"},
			{"t / c / m", "the computer's terminal with the keys; its changes; its next computer"},
			{"tab", "the computer, beside the thread or in its place"},
		}
		footer = []string{
			"A question that is a form of several fields is answered in the browser.",
		}
	case m.helpBack == screenMain && m.helpPart == "mate":
		title = "Teammate"
		keys = [][2]string{
			{"enter", "write to the teammate; the first prompt starts a thread"},
			{"+", "give the teammate another computer"},
			{"t / c / m", "the computer's terminal with the keys; its changes; its next computer"},
			{"tab", "the computer, beside the teammate or in its place"},
			{"esc / q", "back a place"},
		}
	case m.helpBack == screenMain && m.helpPart == "computer":
		title = "Computer"
		keys = [][2]string{
			{"1-5", "a tab: Terminal, Files, Changes, Browser on a teammate's computer, Details"},
			{"enter", "Terminal: type into the terminal; ctrl+] gives the keys back"},
			{"alt+1-9", "Terminal: another of the computer's terminals"},
			{"+ / T", "Terminal: a new shell; start the teammate's agent"},
			{"y / o / n", "Terminal: the banner's allow, allow once or deny; a: on Home"},
			{"alt+z", "the terminal, or the pane, in full screen"},
			{"s / z / d", "Details: start, stop or resume; suspend; delete, after asking"},
			{"f / h / l", "Details: fork; its checkpoints; its Activity"},
			{"t", "Details: the Terminal tab, with the keys"},
			{"m", "the teammate's next computer"},
			{"tab / esc", "back to the thread; esc back a place"},
		}
	case m.helpBack == screenCheckpoints:
		title = "Checkpoints"
		keys = [][2]string{
			{"↑/↓, j/k", "move the cursor"},
			{"n", "save a checkpoint; enter alone names it after the time"},
			{"r", "restore the checkpoint under the cursor, after asking"},
			{"esc / q", "back"},
		}
		footer = []string{
			"A checkpoint saves the disks, not running processes. A computer saves one",
			"while it runs or is stopped, and goes back to one only while stopped.",
		}
	case m.helpBack == screenComputers:
		title = "Computers"
		keys = [][2]string{
			{"↑/↓, j/k", "move the cursor"},
			{"enter", "open the computer: its Terminal and Details tabs"},
			{"t", "its Terminal tab, with the keys"},
			{"s / z / d", "start, stop or resume; z suspends; d deletes, after asking"},
			{"f / h", "fork it; its checkpoints, to save or restore one"},
			{"+", "a new computer: a teammate, a template and a name"},
			{"esc / q", "back"},
		}
		footer = []string{
			"Every computer of the environment, the teammates' and yours. From 100",
			"columns the dashboard of the one under the cursor shows beside the list.",
		}
	case m.helpBack == screenActivity, m.helpBack == screenEntry:
		title = "Activity"
		keys = [][2]string{
			{"↑/↓, j/k", "move the cursor"},
			{"←/→", "the kind: all, network, git, agents or memory"},
			{"enter", "the entry, and the rule that decided it"},
			{"/", "filter by computer; esc clears it"},
			{"r", "print where the rules are edited, in the browser"},
			{"esc / q", "back"},
		}
		footer = []string{
			"The log shows each computer's latest connections, which Studio keeps in",
			"memory, and every decision. It updates every two seconds.",
		}
	case m.helpBack == screenSettings:
		title = "Settings"
		keys = [][2]string{
			{"↑/↓, j/k", "move the cursor"},
			{"enter", "Activity opens here; the rest print their address in the browser"},
			{"l", "Activity"},
			{"esc / q", "back"},
		}
		footer = []string{"The sections are the web's, and say what each is set to."}
	case m.helpBack == screenMain && m.helpPart == "changes":
		title = "Changes"
		keys = [][2]string{
			{"j / k", "the rail's next or previous row; main shows it"},
			{"↑/↓", "scroll main a line"},
			{"pgup/pgdn", "scroll a page; b and space too"},
			{"g / G", "the start or the end"},
			{"[ / ]", "the previous or the next file"},
			{"enter", "fold or open a repository's changes"},
			{"v", "side by side or unified, from 160 columns"},
			{"y / F", "a push: approve and push, or open its pull request; F force-pushes"},
			{"p / o", "whether approving opens a pull request too; open it later"},
			{"r / n / i", "ask for changes in a note; reject; ignore"},
			{"c", "a cursor on main's lines: ↑/↓ move it, enter comments, esc lets go"},
			{"L", "leave the comments on a push for its agent, deciding nothing"},
			{"1-5 / m", "another tab; the teammate's next computer"},
			{"tab / esc", "back to the thread; esc back a place"},
		}
		footer = []string{
			"Git is read every 5 seconds while the tab shows. Comments on a push go",
			"with r's note, under it; on a change, into the thread's composer as",
			"@path:line.",
		}
	case m.helpBack == screenMain && m.helpPart == "files":
		title = "Files"
		keys = [][2]string{
			{"j / k", "the list's next or previous entry; the arrows too, but in a file"},
			{"enter / →", "open a folder, or a file beside the list"},
			{"← / backspace", "the folder above, or back to the list from a file alone"},
			{"↑/↓", "scroll the file opened a line"},
			{"pgup/pgdn", "scroll a page; b and space too"},
			{"g / G", "the start or the end"},
			{"r", "the next root: /workspace, /tmp, the whole computer"},
			{"o", "print where the browser edits and uploads"},
			{"1-5 / m", "another tab; the teammate's next computer"},
			{"tab / esc", "back to the thread; esc back a place"},
		}
		footer = []string{
			"The folder is read every 10 seconds while the tab shows, and a file again",
			"when it changes. Files of up to 1 MiB show; links aren't followed.",
		}
	case m.helpBack == screenMain && m.helpPart == "review":
		title = "Review"
		keys = [][2]string{
			{"y", "approve and push, or open the pull request"},
			{"F", "force-push a push that rewrites its branch"},
			{"p", "whether approving opens a pull request too"},
			{"o", "open the pull request of a branch pushed before"},
			{"r", "request changes: a note to the teammate, in a new thread"},
			{"n", "reject: the agent sees it on its next fetch"},
			{"i", "ignore: decide nothing, and drop the card"},
			{"↑/↓, j/k", "scroll a line"},
			{"pgup/pgdn", "scroll a page; b and space too"},
			{"g / G", "the start or the end"},
			{"[ / ]", "the previous or the next file"},
			{"v", "side by side or unified, from 160 columns"},
			{"esc / q", "back"},
		}
		footer = []string{
			"A push of a computer in another environment is reviewed here, alone.",
			"Comments on lines of the diff are written in the browser.",
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
