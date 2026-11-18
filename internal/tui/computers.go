package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The Computers screen lists every computer of the environment, the teammates' first and then
// yours, one row each, and takes the keys of a computer: open it, its terminal, start and stop,
// suspend, fork, checkpoints and delete. Its cursor is its own, by the computer's ID, so a
// refresh keeps it on the computer and the sidebar's stays where it was.

// computersState is the Computers screen's cursor, among computerRows, and its computer.
type computersState struct {
	cursor int
	id     string
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

// openComputers shows the Computers screen, its cursor on the computer on show.
func (m *app) openComputers() {
	id := ""
	if sb := m.computerOn(); sb != nil {
		id = sb.ID
	}
	m.openComputersAt(id)
}

// openComputersAt shows the Computers screen, its cursor on the computer id, or the first.
func (m *app) openComputersAt(id string) {
	m.computers.id, m.computers.cursor = id, 0
	m.screen = screenComputers
	m.computersFollow()
}

// computerAt is the computer under the Computers screen's cursor.
func (m *app) computerAt() *sandboxes.View {
	rows, _ := m.computerRows()
	c := &m.computers
	if c.cursor < 0 || c.cursor >= len(rows) {
		return nil
	}
	return &m.visible()[rows[c.cursor]]
}

// computersMove moves the screen's cursor by dir rows.
func (m *app) computersMove(dir int) {
	c := &m.computers
	rows, _ := m.computerRows()
	if len(rows) == 0 {
		c.cursor, c.id = 0, ""
		return
	}
	c.cursor = min(max(c.cursor+dir, 0), len(rows)-1)
	c.id = m.visible()[rows[c.cursor]].ID
}

// computersFollow keeps the screen's cursor on its computer after the list changed, or on a row
// near it when the computer went.
func (m *app) computersFollow() {
	rows, _ := m.computerRows()
	visible := m.visible()
	for k, i := range rows {
		if visible[i].ID == m.computers.id {
			m.computers.cursor = k
			return
		}
	}
	m.computersMove(0)
}

// computersKey answers the keys of the Computers screen.
func (m *app) computersKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sb := m.computerAt()
	switch {
	case key.Matches(msg, keyBack):
		m.screen = screenMain
	case key.Matches(msg, keyUp):
		m.computersMove(-1)
	case key.Matches(msg, keyDown):
		m.computersMove(1)
	case key.Matches(msg, keyOpen):
		if sb != nil {
			return m, m.open(place{kind: placeComputer, sandbox: sb.ID})
		}
	case key.Matches(msg, keyNewComputer):
		if m.env.ID != "" {
			return m, m.openNew()
		}
	case key.Matches(msg, keyShellTab):
		if sb != nil {
			cmd := m.open(place{kind: placeComputer, sandbox: sb.ID})
			return m, tea.Batch(cmd, m.shellTab())
		}
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
	return []key.Binding{keyOpen, keyShellTab, keyLifecycle, keyNewComputer, keyBack, keyHelp, keyDelete, keySuspend, keyFork, keyCheckpoints}
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
	right := pad(m.dashboard(m.visible()[rows[min(m.computers.cursor, len(rows)-1)]], w-listW-2, h), h)
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
