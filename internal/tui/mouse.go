package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// doubleClick is how soon a second click on the same cell must follow the first to open it.
const doubleClick = 400 * time.Millisecond

// wheelLines is how far one turn of the wheel scrolls a pager or a transcript.
const wheelLines = 3

// rowTarget is what a row of the screen leads to when it is clicked.
type rowTarget int

const (
	rowInbox      rowTarget = iota + 1 // a row of Home's list, by its index among inboxRows
	rowEntry                           // an entry of the Activity log
	rowCheckpoint                      // a checkpoint
	rowEnv                             // an environment of the picker
	rowFold                            // a folded tool call or thought of a transcript
	rowFile                            // a file of a review place's file list
	rowChange                          // a row of the Changes tab's rail, by its index among changeRows
	rowFilesEntry                      // an entry of the Files tab's list, by its index
	rowTree                            // a row of the sidebar, by its index among homeRows
	rowSetting                         // a section of Settings
)

// rowHit is a row of the screen, or the part of it from x0 to x1, and what it leads to.
type rowHit struct {
	y, x0, x1 int
	target    rowTarget
	index     int
}

// keyHit is a key drawn on the screen: a keycap of a card or a key of the key bar, on row y from
// x0 to x1. A click on it presses the key.
type keyHit struct {
	y, x0, x1 int
	key       string
}

// mouseState is where the rows and keys of the frame on show lead, and the last click, to tell
// a double click.
type mouseState struct {
	rows  []rowHit
	keys  []keyHit
	crumb []crumbSpan // the parts of the crumb row, and the sidebar's environment, on row 0
	last  time.Time
	lastX int
	lastY int
}

// hitRow records that line of the body, from x0 to x1, leads to the target's index-th item.
func (m *app) hitRow(line, x0, x1 int, target rowTarget, index int) {
	m.mouse.rows = append(m.mouse.rows, rowHit{y: bodyTop + line, x0: x0, x1: x1, target: target, index: index})
}

// hitKeys records the keys of a line on row y of the screen, the line starting at column x.
func (m *app) hitKeys(y, x int, spans []keySpan) {
	for _, k := range spans {
		m.mouse.keys = append(m.mouse.keys, keyHit{y: y, x0: x + k.x0, x1: x + k.x1, key: k.key})
	}
}

// pressOf is the key press of a key's name as a binding has it, such as "enter" or "A".
func pressOf(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return enterKey
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	}
	return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
}

// typingKeys reports whether a field takes the keys, so a click would land in the middle of it.
func (m *app) typingKeys() bool {
	return m.search.open || m.filtering || m.typing != nil || m.confirm != nil ||
		(m.checkpoints.restoring != nil && m.screen == screenCheckpoints) ||
		m.composerKeys()
}

// enterKey is the key a double click presses.
var enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}

// click answers a click: on a part of the crumb row or a key it acts as that does, on a row it
// selects it, a double click opens it as enter does, and a click in a column gives it the cursor.
func (m *app) click(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseLeft && !m.look.plain && m.search.open {
		return m.searchClick(msg)
	}
	if msg.Button != tea.MouseLeft || m.look.plain || m.typingKeys() {
		return m, nil
	}
	now := time.Now()
	double := now.Sub(m.mouse.last) < doubleClick && msg.X == m.mouse.lastX && msg.Y == m.mouse.lastY
	m.mouse.last, m.mouse.lastX, m.mouse.lastY = now, msg.X, msg.Y
	if double {
		m.mouse.last = time.Time{} // a third click starts again
	}
	if msg.Y == 0 {
		for _, s := range m.mouse.crumb {
			if msg.X >= s.x0 && msg.X < s.x1 {
				return m, m.clickCrumb(s.part)
			}
		}
		return m, nil
	}
	for _, k := range m.mouse.keys {
		if k.y == msg.Y && msg.X >= k.x0 && msg.X < k.x1 {
			m.mouse.last = time.Time{} // a key is pressed once per click
			if k.key == "ctrl+k" {
				return m, m.openSearch("") // the sidebar's search row, a shell with the keys or not
			}
			return m.key(pressOf(k.key))
		}
	}
	for _, r := range m.mouse.rows {
		if r.y == msg.Y && msg.X >= r.x0 && msg.X < r.x1 {
			return m.clickRow(r, double)
		}
	}
	return m, m.clickColumn(msg.X, msg.Y)
}

// clickCrumb acts on a part of the crumb row: ≡ opens the drawer, the chip shows the teammate's
// next computer, what waits goes to Home, and the environment opens the picker.
func (m *app) clickCrumb(part crumbPart) tea.Cmd {
	switch part {
	case crumbMenu:
		m.toggleSidebar()
	case crumbChip:
		m.nextComputer()
	case crumbWaiting:
		if m.screen != screenMain || m.here.kind != placeHome {
			return m.open(place{})
		}
	case crumbEnv:
		m.openEnvs()
	}
	return nil
}

// clickColumn gives the column under a click of the frame the cursor; on a terminal that is on
// show, the keys.
func (m *app) clickColumn(x, y int) tea.Cmd {
	if m.screen != screenMain {
		return nil
	}
	l := m.layoutFor(m.width)
	if m.drawer && x <= sidebarW {
		return nil
	}
	if m.drawer {
		m.drawer, m.focus = false, focusMain
		return nil
	}
	if l.sidebar > 0 && x < l.sidebar {
		m.focus = focusSidebar
		m.chats.composing = false
		return nil
	}
	if rc, ok := m.paneRect(); ok && rc.has(x, y) {
		if t, on := m.paneTarget(); on && t.kind != sessionChat {
			if m.sidePane() == paneComputer {
				if _, side := m.frameColumns(); side.has(x, y) {
					m.focus = focusSide
				} else {
					m.focus = focusMain
				}
			}
			return m.focusPane()
		}
	}
	main, side := m.frameColumns()
	switch {
	case side.w > 0 && side.has(x, y) && m.sidePane() == paneComputer:
		m.focus = focusSide
		m.chats.composing = false
	case main.has(x, y):
		m.focus = focusMain
	}
	return nil
}

// clickRow selects what a row leads to; a double click opens it, as enter would.
func (m *app) clickRow(r rowHit, double bool) (tea.Model, tea.Cmd) {
	switch r.target {
	case rowInbox:
		if rows := m.inboxRows(); r.index < len(rows) {
			m.inboxTo(rows, r.index)
			m.focus, m.pane.focus = focusMain, false
		}
	case rowEntry:
		m.activity.cursor = r.index
	case rowTree:
		if rows := m.homeRows(); r.index < len(rows) {
			m.setCursor(rows[r.index])
			m.pane.focus = false
			if !m.drawer {
				m.focus = focusSidebar
			}
		}
	case rowCheckpoint:
		m.checkpoints.cursor = r.index
		return m, nil // enter does nothing here
	case rowEnv:
		m.picker = r.index
	case rowSetting:
		m.settings.cursor = r.index
	case rowChange:
		if rows := m.changeRows(); r.index < len(rows) && rows[r.index].pickable() {
			if rows[r.index].key != m.changes.cursor {
				m.changes.cursor, m.changes.scroll = rows[r.index].key, 0
			}
		}
		if double {
			return m, m.changesKey(enterKey)
		}
		return m, nil
	case rowFilesEntry:
		m.files.cursor = r.index
		if double {
			m.openEntry()
		}
		return m, nil
	case rowFold:
		m.chats.fold = r.index
		m.chats.scroll = m.foldScroll(r.index)
	case rowFile:
		if r.index < len(m.pagerFiles()) {
			m.pager.scroll = m.pagerFiles()[r.index].line
		}
		return m, nil
	}
	if double {
		return m.key(enterKey)
	}
	return m, nil
}

// wheel scrolls what the screen shows: a pager or transcript by a few lines, a list by its
// cursor.
func (m *app) wheel(msg tea.MouseWheelMsg) {
	if m.look.plain || m.filtering || m.typing != nil || m.confirm != nil {
		return
	}
	dir := 0
	switch msg.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	default:
		return
	}
	if m.search.open {
		m.searchMove(dir)
		return
	}
	switch m.screen {
	case screenMain:
		reading, readingOn := m.readingRect()
		chat := m.chatRect()
		main, _ := m.frameColumns()
		rc, termOn := m.paneRect()
		_, target := m.paneTarget()
		l := m.layoutFor(m.width)
		switch {
		case m.drawer && msg.X <= sidebarW, l.sidebar > 0 && msg.X < l.sidebar:
			k := tea.KeyPressMsg{Code: tea.KeyDown}
			if dir < 0 {
				k = tea.KeyPressMsg{Code: tea.KeyUp}
			}
			if m.drawer || m.focus == focusSidebar {
				m.key(k)
			} else {
				m.moveRow(dir)
			}
		case m.chats.on && chat.has(msg.X, msg.Y) && (m.chats.reading || m.mainPane() == paneThread):
			m.wheelChat(dir)
		case target && termOn && rc.has(msg.X, msg.Y):
			// a terminal scrolls itself: its program reads the keys
		case m.mainPane() == paneReview && main.has(msg.X, msg.Y):
			m.pager.scroll = max(m.pager.scroll+dir*wheelLines, 0) // drawing keeps it in the review
		case m.comp.tab == tabChanges && m.computerOn() != nil && m.overComputer(msg.X, msg.Y):
			m.wheelChanges(msg.X, dir)
		case m.comp.tab == tabFiles && m.computerOn() != nil && m.overComputer(msg.X, msg.Y):
			m.wheelFiles(msg.X, dir)
		case readingOn && reading.has(msg.X, msg.Y):
			m.inbox.scroll = max(m.inbox.scroll+dir*wheelLines, 0)
		case m.inboxShows() && main.has(msg.X, msg.Y):
			m.inboxMove(dir)
		}
	case screenActivity, screenCheckpoints, screenEnvs:
		if m.checkpoints.restoring != nil && m.screen == screenCheckpoints {
			return
		}
		k := tea.KeyPressMsg{Code: tea.KeyDown}
		if dir < 0 {
			k = tea.KeyPressMsg{Code: tea.KeyUp}
		}
		m.key(k)
	}
}

// wheelChat scrolls the transcript on show by a few lines.
func (m *app) wheelChat(dir int) {
	c := &m.chats
	if c.fold >= 0 {
		c.scroll, c.fold = m.foldScroll(c.fold), -1
	}
	total := len(m.transcript(m.chatWidth()).lines)
	c.scroll = min(max(c.scroll-dir*wheelLines, 0), max(total-m.transcriptRows(), 0))
}

// overComputer reports whether a cell is on the computer pane, in the main column or beside it.
func (m *app) overComputer(x, y int) bool {
	rc, ok := m.paneRectOf(paneComputer)
	return ok && rc.has(x, y)
}
