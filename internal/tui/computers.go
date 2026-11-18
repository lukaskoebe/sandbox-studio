package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// The Computers screen lists every computer of the environment, the teammates' first and then
// yours, one row each, and takes the keys of a computer: its detail, its terminal, start and
// stop, suspend, fork, checkpoints and delete. It moves Home's cursor while it shows, so every
// screen it opens works on the row's computer, and puts it back on esc.

// computersState is the Computers screen: its cursor, among computerRows, and Home's cursor as
// it was.
type computersState struct {
	cursor int
	saved  homeCursor
}

var keyNewComputer = key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "new computer"))

// computerRows are the computers of the environment by their index among the visible ones, in
// the screen's order, and the index of the first of yours.
func (m *app) computerRows() (rows []int, yours int) {
	visible := m.visible()
	for _, t := range m.team {
		for i, sb := range visible {
			if sb.TeammateID == t.ID {
				rows = append(rows, i)
			}
		}
	}
	yours = len(rows)
	return append(rows, m.yourComputers()...), yours
}

// openComputers shows the Computers screen, its cursor on the computer under Home's.
func (m *app) openComputers() {
	c := &m.computers
	c.saved = m.saveCursor()
	m.filter = ""
	rows, _ := m.computerRows()
	c.cursor = 0
	for i, sb := range rows {
		if sb == m.cursor && m.at != atHome && m.at != atPlace && m.at != atAction {
			c.cursor = i
		}
	}
	m.screen = screenComputers
	m.computersMove(0)
}

// computersMove moves the screen's cursor by dir rows, and Home's cursor to its computer.
func (m *app) computersMove(dir int) {
	c := &m.computers
	rows, _ := m.computerRows()
	if len(rows) == 0 {
		c.cursor = 0
		m.at, m.sub = atSandbox, -1
		m.cursor = -1
		return
	}
	c.cursor = min(max(c.cursor+dir, 0), len(rows)-1)
	m.at, m.mate, m.sub, m.cursor = atSandbox, "", -1, rows[c.cursor]
}

// computersFollow keeps the screen's cursor on Home's cursor's computer after the list changed,
// or on a row near it when the computer went.
func (m *app) computersFollow() {
	rows, _ := m.computerRows()
	for k, i := range rows {
		if i == m.cursor && m.at == atSandbox {
			m.computers.cursor = k
			return
		}
	}
	m.computersMove(0)
}

// closeComputers goes back to Home, with Home's cursor where it was.
func (m *app) closeComputers() {
	c := &m.computers
	m.restoreCursor(c.saved)
	m.screen = screenHome
}

// computersKey answers the keys of the Computers screen.
func (m *app) computersKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sb := m.selected()
	switch {
	case key.Matches(msg, keyBack):
		m.closeComputers()
	case key.Matches(msg, keyUp):
		m.computersMove(-1)
	case key.Matches(msg, keyDown):
		m.computersMove(1)
	case key.Matches(msg, keyOpen):
		if sb != nil {
			m.screen, m.detailBack, m.detailHome = screenDetail, screenComputers, nil
		}
	case key.Matches(msg, keyNewComputer):
		if m.env.ID != "" {
			return m, m.openNew()
		}
	case key.Matches(msg, keyTerminal):
		return m, m.attachTo(sb, terminalName)
	case key.Matches(msg, keyLifecycle):
		return m, m.toggle(sb)
	case key.Matches(msg, keySuspend):
		return m, m.suspend(sb)
	case key.Matches(msg, keyFork):
		m.askFork(sb)
	case key.Matches(msg, keyCheckpoints):
		return m, m.openCheckpoints(sb)
	case key.Matches(msg, keyDelete):
		m.askDelete(sb)
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// computersBindings are the keys the key bar shows on the Computers screen.
func (m *app) computersBindings() []key.Binding {
	return []key.Binding{keyOpen, keyTerminal, keyLifecycle, keyNewComputer, keyBack, keyHelp, keyDelete, keySuspend, keyFork, keyCheckpoints}
}

// computersBody is the Computers screen in w cells and h rows, the cursor's row in view. From
// paneWidth the list is a column as wide as Home's sidebar, with the dashboard of the computer
// under the cursor beside it, as Home showed it beside a computer's row.
func (m *app) computersBody(w, h int) []string {
	rows, _ := m.computerRows()
	if !m.computersSplit(w) {
		return m.computersList(w, h)
	}
	listW := sidebarWidth(w)
	left := pad(m.computersList(listW-1, h), h)
	right := pad(m.dashboard(m.visible()[rows[m.computers.cursor]], w-listW-2, h), h)
	out := make([]string, h)
	for i := range out {
		out[i] = fitStyled(left[i], listW) + styleDim.Render("│") + " " + right[i]
	}
	return out
}

// computersSplit reports whether the Computers screen shows a dashboard beside its list.
func (m *app) computersSplit(w int) bool {
	rows, _ := m.computerRows()
	return w >= paneWidth && len(rows) > 0
}

// computersList is the Computers screen's list in w cells and h rows: a header, and a row a
// computer under the teammates' and your headings.
func (m *app) computersList(w, h int) []string {
	visible := m.visible()
	rows, yours := m.computerRows()
	lines := []string{styleBold.Render("Computers") + styleDim.Render(fmt.Sprintf(" · %d", len(rows))), styleDim.Render(strings.Repeat("─", w))}
	if len(rows) == 0 {
		return append(lines, "", styleDim.Render(clip("No computer yet. + makes one: a teammate, a template and a name.", w)))
	}
	type line struct {
		text string
		row  int // the row it draws, or -1
	}
	var all []line
	cursorLine := 0
	for k, i := range rows {
		switch {
		case k == 0 && yours > 0:
			all = append(all, line{styleDim.Render("TEAMMATES' COMPUTERS"), -1})
		case k == yours && k > 0:
			all = append(all, line{"", -1})
		}
		if k == yours {
			all = append(all, line{styleDim.Render("YOUR COMPUTERS"), -1})
		}
		if k == m.computers.cursor {
			cursorLine = len(all)
		}
		text := m.groupHeader(visible[i], k == m.computers.cursor, 0, w)
		if w < 64 {
			text = m.computerLine(visible[i], k == m.computers.cursor, w)
		}
		all = append(all, line{text, k})
	}
	room := h - len(lines)
	offset := 0
	if cursorLine >= room {
		offset = cursorLine - room + 1
	}
	for _, l := range all[offset:] {
		if len(lines) >= h {
			break
		}
		lines = append(lines, l.text)
	}
	return lines
}
