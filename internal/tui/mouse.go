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
	rowSandbox    rowTarget = iota + 1 // a sandbox of Home's list
	rowCard                            // an approval's card
	rowEntry                           // an entry of the Activity log
	rowSession                         // a session under a sandbox of Home's list
	rowCheckpoint                      // a checkpoint
	rowEnv                             // an environment of the picker
	rowFold                            // a folded tool call or thought of a transcript
	rowFile                            // a file of the git review's file list
	rowPush                            // a push or pull request of the Git screen
)

// rowHit is a row of the screen, or the part of it from x0 to x1, and what it leads to.
type rowHit struct {
	y, x0, x1 int
	target    rowTarget
	index     int
	sub       int // a session's index under its sandbox
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
	tabs  []tabSpan
	last  time.Time
	lastX int
	lastY int
}

// hitRow records that line of the body, from x0 to x1, leads to the target's index-th item.
func (m *app) hitRow(line, x0, x1 int, target rowTarget, index int) {
	m.mouse.rows = append(m.mouse.rows, rowHit{y: bodyTop + line, x0: x0, x1: x1, target: target, index: index})
}

// hitSession records that line of the body, from x0 to x1, leads to the sub-th session of the
// index-th sandbox.
func (m *app) hitSession(line, x0, x1, index, sub int) {
	m.mouse.rows = append(m.mouse.rows, rowHit{y: bodyTop + line, x0: x0, x1: x1, target: rowSession, index: index, sub: sub})
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
	return m.filtering || m.typing != nil || m.confirm != nil ||
		(m.checkpoints.restoring != nil && m.screen == screenCheckpoints) ||
		(m.chatShows() && m.chats.composing)
}

// enterKey is the key a double click presses.
var enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}

// click answers a click: on a tab or a key it acts as that key, on a row it selects it, and a
// double click opens it as enter does.
func (m *app) click(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
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
		for _, s := range m.mouse.tabs {
			if msg.X >= s.x0 && msg.X < s.x1 {
				return m, m.openTab(s)
			}
		}
		return m, nil
	}
	for _, k := range m.mouse.keys {
		if k.y == msg.Y && msg.X >= k.x0 && msg.X < k.x1 {
			m.mouse.last = time.Time{} // a key is pressed once per click
			return m.key(pressOf(k.key))
		}
	}
	for _, r := range m.mouse.rows {
		if r.y == msg.Y && msg.X >= r.x0 && msg.X < r.x1 {
			return m.clickRow(r, double)
		}
	}
	// A click in Home's pane gives it the keys.
	if m.paneShows() && msg.X >= m.paneX() && msg.Y >= bodyTop && msg.Y < bodyTop+m.bodyRows() {
		return m, m.focusPane()
	}
	return m, nil
}

// clickRow selects what a row leads to; a double click opens it, as enter would.
func (m *app) clickRow(r rowHit, double bool) (tea.Model, tea.Cmd) {
	switch r.target {
	case rowSandbox:
		m.cursor, m.sub, m.pane.focus = r.index, -1, false
	case rowCard:
		m.approvalCursor = r.index
	case rowEntry:
		m.activity.cursor = r.index
	case rowSession:
		m.cursor, m.sub, m.pane.focus = r.index, r.sub, false
	case rowCheckpoint:
		m.checkpoints.cursor = r.index
		return m, nil // enter does nothing here
	case rowEnv:
		m.picker = r.index
	case rowPush:
		m.git.cursor = r.index
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
	switch m.screen {
	case screenPager:
		m.pager.scroll = max(m.pager.scroll+dir*wheelLines, 0)
	case screenChat:
		m.wheelChat(dir)
	case screenHome:
		switch {
		case m.chats.inPane && m.paneShows() && msg.X >= m.paneX():
			m.wheelChat(dir)
		case m.paneFocused() || (m.paneShows() && msg.X >= m.paneX()):
			// a terminal in the pane scrolls itself: its program reads the keys
		default:
			k := tea.KeyPressMsg{Code: tea.KeyDown}
			if dir < 0 {
				k = tea.KeyPressMsg{Code: tea.KeyUp}
			}
			m.key(k)
		}
	case screenApprovals, screenActivity, screenCheckpoints, screenEnvs:
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

// openTab shows a tab's screen, as its key does: Sandboxes is Home. The environment's name opens the picker from Home, as e does.
func (m *app) openTab(s tabSpan) tea.Cmd {
	if s.env {
		if m.screen == screenHome {
			m.openEnvs()
		}
		return nil
	}
	leave := func() {
		if m.screen == screenChat {
			m.stopChat()
		}
	}
	switch s.tab {
	case tabSandboxes:
		leave()
		m.screen = screenHome
	case tabApprovals:
		if m.screen == screenPager {
			m.screen = m.pager.back // the review's way back
		}
		m.openApprovals()
	case tabGit:
		if m.screen == screenGit {
			return nil
		}
		leave()
		return m.openGit()
	case tabActivity:
		if m.screen == screenActivity {
			return nil
		}
		leave()
		m.screen = screenHome // a tab is a way in from the top: esc returns to Home
		return m.openActivity("")
	}
	return nil
}
