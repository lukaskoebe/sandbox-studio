package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The frame shows a place, as the web's pages are: Home, a teammate's home, a thread beside its
// computer, or a computer on its own. Enter on a row of the sidebar opens one; esc goes back
// through the places opened before it, last to Home.

// placeKind is what a place is.
type placeKind int

const (
	placeHome     placeKind = iota // Home's list: what waits for you, what works and what is quiet
	placeMate                      // a teammate's home: who it is, its tasks, and a composer that starts a thread
	placeThread                    // a thread: a chat, a new one (chat ""), or an agent in a terminal (term)
	placeComputer                  // a computer on its own
	placeReview                    // a push's review alone, its computer not in the environment on show
)

// place is a place to show.
type place struct {
	kind    placeKind
	mate    string // the teammate
	chat    string // the thread's chat
	term    string // the terminal of a thread that is an agent in a terminal
	sandbox string // the computer it shows: the thread's, the teammate's picked one, or the computer
	review  string // the approval a review place shows
	over    screen // the full screen it was left from, which going back to it shows again
}

// historyKeep is how many places esc can go back through.
const historyKeep = 32

// focusArea is the part of the frame whose keys Studio answers, when no shell or composer has them.
type focusArea int

const (
	focusMain    focusArea = iota // Home's list, a thread, a teammate's home, a computer on its own
	focusSidebar                  // the sidebar, or the drawer
	focusSide                     // the computer beside a thread or a teammate's home
)

// open shows p, the place on show going on the history first, so esc returns to it.
func (m *app) open(p place) tea.Cmd {
	if m.screen != screenMain || m.here != p {
		from := m.here
		if m.screen == screenComputers {
			from.over = screenComputers
		}
		m.history = append(m.history, from)
		if len(m.history) > historyKeep {
			m.history = m.history[len(m.history)-historyKeep:]
		}
	}
	return m.show(p)
}

// back goes back to the place shown before, skipping the ones that went, and to Home once there
// is none.
func (m *app) back() tea.Cmd {
	for len(m.history) > 0 {
		p := m.history[len(m.history)-1]
		m.history = m.history[:len(m.history)-1]
		if !m.placeLives(p) {
			continue
		}
		over := p.over
		cmd := m.show(p)
		if over == screenComputers {
			m.openComputersAt(m.computers.id)
		}
		return cmd
	}
	return m.show(place{})
}

// goHome shows Home, its cursor on the first row, with no way back.
func (m *app) goHome() {
	m.history = nil
	m.show(place{})
}

// placeLives reports whether what a place shows is still there.
func (m *app) placeLives(p place) bool {
	switch p.kind {
	case placeMate:
		_, ok := m.mateOf(p.mate)
		return ok
	case placeThread, placeComputer:
		_, ok := m.sandboxByID(p.sandbox)
		return ok
	case placeReview:
		_, ok := m.reviewByID(p.review)
		return ok
	}
	return true
}

// show makes p the place on show: the main column has the keys, the sidebar's cursor goes to its
// row, and its thread opens.
func (m *app) show(p place) tea.Cmd {
	p.over = screenMain
	m.here = p
	m.screen = screenMain
	m.drawer, m.flip, m.zoom = false, false, false
	m.focus = focusMain
	m.pane.focus, m.pane.answer = false, false
	m.blurButtons()
	if sb := m.hereSandbox(); sb != nil {
		if m.comp.sandbox != sb.ID {
			m.comp.sandbox, m.comp.term, m.comp.tab = sb.ID, terminalName, tabTerminal
		}
		if p.kind == placeMate {
			m.pick(p.mate, sb.ID)
		}
	}
	if p.kind == placeThread && p.term != "" {
		m.comp.term, m.comp.tab = p.term, tabDetails
	}
	if m.comp.tab == tabTerminal && !slices.Contains(m.computerTabs(), tabTerminal) {
		m.comp.tab = tabDetails
	}
	m.cursorToHere()
	return m.showChat()
}

// pick remembers the computer last picked of a teammate's, which its home shows next time.
func (m *app) pick(mate, id string) {
	if m.comp.picked == nil {
		m.comp.picked = map[string]string{}
	}
	m.comp.picked[mate] = id
}

// showChat opens the chat of the place on show: the thread's, or a new one on a teammate's
// home, whose composer starts it. Other places close the chat that was on show, unless Home's
// reading pane shows it.
func (m *app) showChat() tea.Cmd {
	p := m.here
	c := &m.chats
	switch {
	case p.kind == placeThread && p.term == "":
		sb, ok := m.sandboxByID(p.sandbox)
		if !ok {
			return nil
		}
		delete(m.sess.done, "chat/"+p.chat) // seen now
		delete(m.sess.working, "chat/"+p.chat)
		if c.on && p.chat != "" && c.id == p.chat {
			c.reading = false // Home's reading pane had it open: it stays
			return nil
		}
		m.stopChat()
		m.chats = chatState{sandbox: sb, on: true, fold: -1, gen: c.gen}
		cmd := m.openChat(p.chat)
		if ch, ok := m.chatByID(p.chat); ok && ch.State == store.ChatIdle && len(m.threadWaiting(ch)) == 0 {
			m.chats.composing = true
		}
		return cmd
	case p.kind == placeMate:
		m.stopChat()
		m.chats = chatState{fold: -1, gen: c.gen + 1}
		if sb := m.hereSandbox(); sb != nil {
			m.chats.sandbox, m.chats.on, m.chats.loaded = *sb, true, true
		}
		return nil
	}
	if p.kind == placeThread { // an agent's terminal
		delete(m.sess.done, "term/"+p.sandbox+"/"+p.term) // seen now
	}
	if c.on && !c.reading {
		m.stopChat()
		m.chats = chatState{fold: -1, gen: c.gen + 1}
	}
	return nil
}

// chatByID is a chat of the environment as the last read of the threads has it.
func (m *app) chatByID(id string) (store.Chat, bool) {
	for _, list := range m.sess.chats {
		for _, c := range list {
			if c.ID == id {
				return c, true
			}
		}
	}
	return store.Chat{}, false
}

// hereSandbox is the computer the place on show is about: a thread's own, the computer, or the
// one a teammate's home shows. Home has none.
func (m *app) hereSandbox() *sandboxes.View {
	p := m.here
	id := p.sandbox
	if p.kind == placeMate {
		id = m.mateComputer(p.mate, p.sandbox)
	}
	if p.kind == placeHome || p.kind == placeReview || id == "" {
		return nil
	}
	for i := range m.sandboxes {
		if m.sandboxes[i].ID == id {
			return &m.sandboxes[i]
		}
	}
	return nil
}

// mateComputer is the computer a teammate's home shows, as the web picks it
// (web/src/lib/computer.ts screenComputer): the one asked for, else the one picked last, else one
// where a thread works, else the latest thread's, else its first.
func (m *app) mateComputer(mate, asked string) string {
	list := m.sandboxesOf(mate)
	has := func(id string) bool {
		return slices.ContainsFunc(list, func(sb sandboxes.View) bool { return sb.ID == id })
	}
	if asked != "" && has(asked) {
		return asked
	}
	if id := m.comp.picked[mate]; id != "" && has(id) {
		return id
	}
	threads := m.threadsOf(mate)
	for _, x := range threads {
		if m.stateOf(x) == stateWorking && has(x.sandbox) {
			return x.sandbox
		}
	}
	for _, x := range threads {
		if x.kind == sessionChat && has(x.sandbox) {
			return x.sandbox // the newest activity first
		}
	}
	if len(list) > 0 {
		return list[0].ID
	}
	return ""
}

// computerOn is the computer the computer pane shows, while the place on show has one.
func (m *app) computerOn() *sandboxes.View {
	if m.here.kind == placeHome || m.here.kind == placeReview || m.comp.sandbox == "" {
		return nil
	}
	for i := range m.sandboxes {
		if m.sandboxes[i].ID == m.comp.sandbox {
			return &m.sandboxes[i]
		}
	}
	return nil
}

// nextComputer shows the teammate's next computer in the computer pane, for a teammate with
// more than one; a teammate's home remembers it.
func (m *app) nextComputer() {
	mate := m.here.mate
	if sb := m.computerOn(); mate == "" && sb != nil {
		mate = sb.TeammateID
	}
	list := m.sandboxesOf(mate)
	if len(list) < 2 {
		m.setNotice("Only one computer to show.", false)
		return
	}
	i := slices.IndexFunc(list, func(sb sandboxes.View) bool { return sb.ID == m.comp.sandbox })
	next := list[(i+1)%len(list)]
	m.closePane()
	m.comp.sandbox, m.comp.term = next.ID, terminalName
	m.pick(mate, next.ID)
	if m.here.kind == placeMate {
		m.here.sandbox = next.ID
		if m.chats.on && m.chats.id == "" {
			m.chats.sandbox = next
		}
	}
	m.setNotice("Showing "+next.Name+".", false)
}

// placeOf is the place a thread or terminal of the sidebar or Home's list opens.
func placeOf(x session) place {
	switch x.kind {
	case sessionChat:
		return place{kind: placeThread, mate: x.mate, chat: x.chat.ID, sandbox: x.chat.SandboxID}
	case sessionAgent, sessionTerminal:
		return place{kind: placeThread, mate: x.mate, term: x.term.Name, sandbox: x.sandbox}
	}
	return place{}
}

// newThread opens a new thread with the teammate in view, on the computer on show or its own;
// a teammate without a computer gets the form of a new one.
func (m *app) newThread(mate string) tea.Cmd {
	if mate == "" {
		if sb := m.computerOn(); sb != nil {
			mate = sb.TeammateID
		}
	}
	if _, ok := m.mateOf(mate); !ok {
		m.setNotice("A thread is with a teammate. + on Home or the sidebar makes one.", false)
		return nil
	}
	id := ""
	if sb := m.computerOn(); sb != nil && sb.TeammateID == mate {
		id = sb.ID
	}
	if id = m.mateComputer(mate, id); id == "" {
		return m.openNewFor(mate)
	}
	return m.open(place{kind: placeThread, mate: mate, sandbox: id})
}
