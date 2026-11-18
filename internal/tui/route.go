package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Studio's keys in the frame go to the part of it that has the cursor: the sidebar, the main
// column (Home's list, a thread, a teammate's home, a computer on its own) or the computer beside
// it. A few are the frame's own wherever the cursor is: \ to the sidebar and back, alt+z full
// screen, and tab between a thread and its computer.

// The frame's keys.
var (
	keySidebarTo = key.NewBinding(key.WithKeys("\\"), key.WithHelp("\\", "sidebar"))
	keyNewAny    = key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "new"))
)

// mainKey answers Studio's keys in the frame.
func (m *app) mainKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keySidebarTo):
		m.toggleSidebar()
		return m, nil
	case key.Matches(msg, keyZoom):
		m.zoom = !m.zoom
		return m, nil
	}
	if m.sidebarKeys() {
		return m.sidebarKey(msg)
	}
	if m.focus == focusSide && m.sidePane() == paneComputer {
		return m.sideKey(msg)
	}
	switch m.mainPane() {
	case paneHomeList:
		return m.inboxKey(msg)
	case paneThread:
		if m.here.term != "" {
			return m.threadTermKey(msg)
		}
		if !m.chats.composing && key.Matches(msg, keyToSide) {
			m.flipTo(true)
			return m, nil
		}
		return m.threadKey(msg)
	case paneMateHome:
		if !m.chats.composing && key.Matches(msg, keyToSide) {
			m.flipTo(true)
			return m, nil
		}
		return m.mateKey(msg)
	case paneComputer:
		return m.sideKey(msg)
	case paneReview:
		return m.reviewPlaceKey(msg)
	}
	return m, nil
}

// sidebarKeys reports whether the sidebar has Studio's keys: it is drawn and has the cursor, or
// the drawer is open.
func (m *app) sidebarKeys() bool {
	if m.screen != screenMain {
		return false
	}
	if m.drawer {
		return true
	}
	return m.focus == focusSidebar && m.layoutFor(m.width).sidebar > 0
}

// toggleSidebar gives the sidebar the cursor, as a drawer over the body below paneWidth, and
// gives it back to the main column.
func (m *app) toggleSidebar() {
	m.blurButtons()
	if m.sidebarKeys() {
		m.drawer, m.focus = false, focusMain
		return
	}
	m.focus = focusSidebar
	if m.layoutFor(m.width).sidebar == 0 {
		m.drawer = true
		m.cursorToHere() // the drawer opens on the row of the place on show
	}
}

// flipTo gives the computer the cursor (side true) or the thread: beside it from splitWidth, in
// its place below.
func (m *app) flipTo(side bool) {
	if !m.hasSide() {
		return
	}
	m.chats.composing = false
	m.pane.focus = false
	if m.layoutFor(m.width).side > 0 {
		if side {
			m.focus = focusSide
		} else {
			m.focus = focusMain
		}
		return
	}
	m.flip, m.focus = side, focusMain
}

// sideKey answers the keys of the computer pane, beside a thread or in the main column: tab
// back to the thread, esc and q back a place, and the rest the pane's.
func (m *app) sideKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyFlip) && m.hasSide():
		m.flipTo(false)
		return m, nil
	case key.Matches(msg, keyBack) && m.buttonsIdle():
		return m, m.back()
	case key.Matches(msg, keyHelp):
		m.openHelp()
		return m, nil
	}
	return m.computerKey(msg)
}

// threadTermKey answers the keys of a thread that is an agent in a terminal, while Studio has
// them: the terminal's, tab to its computer, esc and q back a place.
func (m *app) threadTermKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sb, ok := m.sandboxByID(m.here.sandbox)
	if !ok {
		return m, m.back()
	}
	switch {
	case key.Matches(msg, keyToSide):
		m.flipTo(true)
		return m, nil
	case key.Matches(msg, keyBack) && m.buttonsIdle():
		return m, m.back()
	case key.Matches(msg, keyHelp):
		m.openHelp()
		return m, nil
	case key.Matches(msg, keyNewThread):
		return m, m.newThread(m.here.mate)
	case key.Matches(msg, keyComputer):
		m.nextComputer()
		return m, nil
	}
	return m, m.terminalKey(msg, sb)
}

// buttonsIdle reports whether no control has the arrows, so esc goes back rather than leaving
// the controls.
func (m *app) buttonsIdle() bool {
	return m.buttons.owner == "" && !m.pane.answer
}

// sidebarKey answers the keys of the sidebar: the rows, enter opens the row's place, and the
// ways about Studio.
func (m *app) sidebarKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keyUndo) && m.undoable() != nil {
		return m, m.undo()
	}
	switch {
	case key.Matches(msg, keyQuit):
		return m, tea.Quit
	case key.Matches(msg, keyUp):
		m.moveRow(-1)
	case key.Matches(msg, keyDown):
		m.moveRow(1)
	case key.Matches(msg, keyWaiting):
		m.nextWaiting()
	case key.Matches(msg, keyOlder) && (m.at == atMate || m.at == atThread) && m.mate != "":
		m.showOlder(m.mate, msg.String() == "right")
	case key.Matches(msg, keyOpen):
		return m, m.sidebarOpen()
	case key.Matches(msg, keyNewAny):
		return m, m.sidebarNew()
	case key.Matches(msg, keySettings):
		return m, m.openSettings()
	case key.Matches(msg, keyActivity):
		return m, m.openActivity("")
	case key.Matches(msg, keyEnv):
		m.openEnvs()
	case key.Matches(msg, keyFilter):
		m.startFilter()
	case key.Matches(msg, keyEsc):
		if m.filter != "" {
			m.filter = ""
			m.reselect(m.selectedID())
			return m, nil
		}
		m.drawer, m.focus = false, focusMain
		m.cursorToHere() // the sidebar shows where you are again
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// sidebarOpen opens the place of the sidebar's row under the cursor: Home, a teammate's home, a
// thread, a computer of yours, a place of the environment, or a new teammate or computer.
func (m *app) sidebarOpen() tea.Cmd {
	switch m.at {
	case atHome:
		return m.open(place{})
	case atMate:
		return m.open(place{kind: placeMate, mate: m.mate})
	case atPlace:
		m.drawer, m.focus = false, focusMain
		return m.openPlace(m.rowPlace)
	case atAction:
		m.drawer, m.focus = false, focusMain
		return m.rowActionDo(m.rowAction)
	case atThread:
		x, ok := m.selectedSession()
		if !ok {
			return nil
		}
		switch x.kind {
		case sessionOlder:
			m.showOlder(x.mate, true)
			return nil
		}
		return m.enterOf(x)
	}
	if sb := m.selected(); sb != nil {
		return m.open(place{kind: placeComputer, sandbox: sb.ID})
	}
	return nil
}

// sidebarNew makes something new from the sidebar's row: a thread with its teammate, or a
// computer.
func (m *app) sidebarNew() tea.Cmd {
	if m.env.ID == "" {
		return nil
	}
	if (m.at == atMate || m.at == atThread) && m.mate != "" {
		return m.newThread(m.mate)
	}
	return m.openNew()
}

// cursorToHere puts the sidebar's cursor on the row of the place on show, when it has one.
func (m *app) cursorToHere() {
	p := m.here
	rows := m.homeRows()
	visible := m.visible()
	want := func(r homeRow) bool {
		switch p.kind {
		case placeHome:
			return r.kind == atHome
		case placeMate:
			return r.kind == atMate && r.mate == p.mate
		case placeThread:
			x, ok := m.rowSession(r, visible)
			if !ok || r.kind != atThread {
				return false
			}
			if p.chat != "" {
				return x.kind == sessionChat && x.chat.ID == p.chat
			}
			return p.term != "" && x.sandbox == p.sandbox && x.term.Name == p.term
		case placeComputer:
			return r.kind == atSandbox && r.sb >= 0 && r.sb < len(visible) && visible[r.sb].ID == p.sandbox
		}
		return false
	}
	for _, r := range rows {
		if want(r) {
			m.setCursor(r)
			return
		}
	}
	if p.kind == placeThread && p.mate != "" { // a new thread, or one the sidebar doesn't list
		for _, r := range rows {
			if r.kind == atMate && r.mate == p.mate {
				m.setCursor(r)
				return
			}
		}
	}
}
