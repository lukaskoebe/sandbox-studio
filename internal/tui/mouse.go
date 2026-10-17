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
	rowChat                            // a chat of the chats list
	rowCheckpoint                      // a checkpoint
	rowEnv                             // an environment of the picker
	rowFold                            // a folded tool call or thought of a transcript
	rowFile                            // a file of the git review's file list
)

// rowHit is a row of the screen, or the part of it from x0 to x1, and what it leads to.
type rowHit struct {
	y, x0, x1 int
	target    rowTarget
	index     int
}

// mouseState is where the rows of the frame on show lead, and the last click, to tell a double
// click.
type mouseState struct {
	rows  []rowHit
	tabs  []tabSpan
	last  time.Time
	lastX int
	lastY int
}

// hitRow records that line of the body, from x0 to x1, leads to the target's index-th item.
func (m *app) hitRow(line, x0, x1 int, target rowTarget, index int) {
	m.mouse.rows = append(m.mouse.rows, rowHit{y: bodyTop + line, x0: x0, x1: x1, target: target, index: index})
}

// typingKeys reports whether a field takes the keys, so a click would land in the middle of it.
func (m *app) typingKeys() bool {
	return m.filtering || m.typing != nil || m.confirm != nil ||
		(m.checkpoints.restoring != nil && m.screen == screenCheckpoints) ||
		(m.screen == screenChat && m.chats.composing)
}

// enterKey is the key a double click presses.
var enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}

// click answers a click: on a tab it acts as the tab's key, on a row it selects it, and a double
// click opens it as enter does.
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
	for _, r := range m.mouse.rows {
		if r.y == msg.Y && msg.X >= r.x0 && msg.X < r.x1 {
			return m.clickRow(r, double)
		}
	}
	return m, nil
}

// clickRow selects what a row leads to; a double click opens it, as enter would.
func (m *app) clickRow(r rowHit, double bool) (tea.Model, tea.Cmd) {
	switch r.target {
	case rowSandbox:
		m.cursor = r.index
	case rowCard:
		m.approvalCursor = r.index
	case rowEntry:
		m.activity.cursor = r.index
	case rowChat:
		m.chats.cursor = r.index
	case rowCheckpoint:
		m.checkpoints.cursor = r.index
		return m, nil // enter does nothing here
	case rowEnv:
		m.picker = r.index
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
		c := &m.chats
		if c.fold >= 0 {
			c.scroll, c.fold = m.foldScroll(c.fold), -1
		}
		total := len(m.transcript(m.width).lines)
		c.scroll = min(max(c.scroll-dir*wheelLines, 0), max(total-m.transcriptRows(), 0))
	case screenHome, screenApprovals, screenActivity, screenChats, screenCheckpoints, screenEnvs:
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

// openTab shows a tab's screen, as its key does: Sandboxes is Home, Git the first push waiting
// for a review. The environment's name opens the picker from Home, as e does.
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
	case tabApprovals, tabGit:
		if m.screen == screenPager {
			m.screen = screenApprovals // the review's way back
		}
		m.openApprovals()
		if s.tab == tabGit {
			for i, c := range m.cards() {
				if isGit(c.first()) {
					m.approvalCursor = i
					break
				}
			}
		}
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
