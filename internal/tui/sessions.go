package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Home lists under each sandbox what can be entered in it: its agent terminals, its chats and its
// other terminals, each with the state of its agent (docs/design/tui-sessions.md). The first nine
// get the numbers 1 to 9, which enter them from Home.

// Timing of the sessions: they are read again every sessionsEvery while Home shows, and an agent
// terminal that printed within workingFor of a read counts as working.
const (
	sessionsEvery = 3 * time.Second
	workingFor    = 5 * time.Second
	newestChats   = 3 // the chats a sandbox lists before its "+ n older" row
)

// sessionKind is what a session row enters.
type sessionKind int

const (
	sessionAgent    sessionKind = iota // a terminal that runs a harness
	sessionChat                        // a chat with the sandbox's teammate
	sessionTerminal                    // any other terminal
	sessionOlder                       // the row that shows a sandbox's older chats
)

// session is a row under a sandbox: a terminal by its tmux name, or a chat.
type session struct {
	kind    sessionKind
	sandbox string
	term    agentproto.Session
	chat    store.Chat
	older   int // the chats the older row hides
}

// id names a session across reads: its sandbox and terminal, or its chat.
func (s session) id() string {
	if s.kind == sessionChat {
		return "chat/" + s.chat.ID
	}
	return "term/" + s.sandbox + "/" + s.term.Name
}

// title is the session's name on its row.
func (s session) title() string {
	switch s.kind {
	case sessionChat:
		return chatTitle(s.chat)
	case sessionOlder:
		return fmt.Sprintf("+ %d older", s.older)
	}
	return s.term.Name
}

// agentState is a session's agent as herdr words it.
type agentState int

const (
	stateNone    agentState = iota // a plain terminal, which has no agent
	stateIdle                      // nothing runs and nothing waits
	stateWorking                   // a turn runs, or the terminal prints
	stateBlocked                   // an approval waits for the user
	stateDone                      // it worked, and the user hasn't entered it since
	stateUnknown                   // the guest doesn't say when the terminal printed
)

// sessionsState is what Home knows of the sessions of the environment's sandboxes.
type sessionsState struct {
	env     string
	terms   map[string][]agentproto.Session // by sandbox ID, for the running ones
	chats   map[string][]store.Chat         // by sandbox ID, the newest activity first
	working map[string]bool                 // the sessions that worked at the last read, by id
	done    map[string]bool                 // the sessions that stopped working since they were entered
	older   map[string]bool                 // the sandboxes whose older chats show
	busy    bool                            // a read, or the wait for the next one, is under way
	gen     int                             // counts the environments read, so a read for an earlier one is dropped
}

type (
	sessionsLoaded struct {
		gen   int
		at    time.Time
		terms map[string][]agentproto.Session
		chats map[string][]store.Chat
	}
	sessionsTick  struct{ gen int }
	agentStarted  struct {
		sandbox sandboxes.View
		name    string
		err     error
	}
)

// watchSessions reads the sessions of the environment's sandboxes while Home shows, again every
// sessionsEvery. A model without a Studio to ask (a test's) reads none.
func (m *app) watchSessions() tea.Cmd {
	s := &m.sess
	if m.client == nil || m.screen != screenHome || m.env.ID == "" || !m.loaded || s.busy {
		return nil
	}
	if s.env != m.env.ID {
		*s = sessionsState{env: m.env.ID, gen: s.gen + 1}
	}
	s.busy = true
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, s.gen
	list := slices.Clone(m.sandboxes)
	return func() tea.Msg {
		out := sessionsLoaded{gen: gen, terms: map[string][]agentproto.Session{}, chats: map[string][]store.Chat{}}
		for _, sb := range list {
			rctx, cancel := requestContext(ctx)
			if phaseOf(sb) == phaseReady {
				if terms, err := client.Terminals(rctx, env, sb.ID); err == nil {
					out.terms[sb.ID] = terms
				}
			}
			if sb.TeammateID != "" {
				if chats, err := client.SandboxChats(rctx, env, sb.ID); err == nil {
					out.chats[sb.ID] = byActivity(chats)
				}
			}
			cancel()
		}
		out.at = time.Now()
		return out
	}
}

// byActivity sorts chats by their latest activity, the newest first, as their rows show it. A
// real Studio's list was seen out of that order, so it isn't relied on.
func byActivity(list []store.Chat) []store.Chat {
	slices.SortStableFunc(list, func(a, b store.Chat) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return list
}

// sessionsUpdate applies the messages of the sessions. ok is false for other messages.
func (m *app) sessionsUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	s := &m.sess
	switch msg := msg.(type) {
	case sessionsLoaded:
		if msg.gen != s.gen {
			return nil, true
		}
		m.sessionsRead(msg)
		gen := s.gen
		return tea.Tick(sessionsEvery, func(time.Time) tea.Msg { return sessionsTick{gen} }), true
	case sessionsTick:
		if msg.gen == s.gen {
			s.busy = false // watchSessions reads again if Home still shows
		}
		return nil, true
	case agentStarted:
		if msg.err != nil {
			m.report("Could not start the agent: "+msg.err.Error(), true)
			return nil, true
		}
		return m.attachTo(&msg.sandbox, msg.name), true
	}
	return nil, false
}

// sessionsRead takes a read in. A session that worked at the last read and no longer does is
// done until the user enters it, and says so.
func (m *app) sessionsRead(msg sessionsLoaded) {
	s := &m.sess
	s.terms, s.chats = msg.terms, msg.chats
	working := map[string]bool{}
	for _, sb := range m.sandboxes {
		for _, x := range m.sessionsOf(sb) {
			if x.kind == sessionOlder {
				continue
			}
			busy := x.kind == sessionChat && x.chat.State == store.ChatRunning ||
				x.kind == sessionAgent && x.term.Activity > 0 && msg.at.Sub(time.Unix(x.term.Activity, 0)) < workingFor
			if busy {
				working[x.id()] = true
				delete(s.done, x.id())
				continue
			}
			if s.working[x.id()] {
				if s.done == nil {
					s.done = map[string]bool{}
				}
				s.done[x.id()] = true
				m.report(x.title()+" in "+sb.Name+" is done.", false)
			}
		}
	}
	s.working = working
}

// sessionsOf are the rows under a sandbox: its agent terminals, its newest chats (or all of
// them), the row that shows the older ones, and its other terminals. A chat continued in a
// terminal is listed once, as its terminal.
func (m *app) sessionsOf(sb sandboxes.View) []session {
	s := &m.sess
	var agents, plain []session
	inTerminal := map[string]bool{}
	for _, t := range s.terms[sb.ID] {
		x := session{kind: sessionTerminal, sandbox: sb.ID, term: t}
		if t.Harness != "" {
			x.kind = sessionAgent
			agents = append(agents, x)
		} else {
			plain = append(plain, x)
		}
		if t.Chat != "" {
			inTerminal[t.Chat] = true
		}
	}
	out := agents
	var chats []store.Chat
	for _, c := range s.chats[sb.ID] {
		if !inTerminal[c.ID] {
			chats = append(chats, c)
		}
	}
	for i, c := range chats {
		if i == newestChats && !s.older[sb.ID] {
			out = append(out, session{kind: sessionOlder, sandbox: sb.ID, older: len(chats) - i})
			break
		}
		out = append(out, session{kind: sessionChat, sandbox: sb.ID, chat: c})
	}
	return append(out, plain...)
}

// stateOf is a session's state. A chat is blocked while one of its permission requests waits;
// an agent terminal while any other approval waits in its sandbox, since an approval doesn't say
// which process asked.
func (m *app) stateOf(x session) agentState {
	switch x.kind {
	case sessionChat:
		for _, a := range m.approvals {
			if a.ChatPermission != nil && a.ChatPermission.ChatID == x.chat.ID {
				return stateBlocked
			}
		}
	case sessionAgent:
		for _, a := range m.approvals {
			if a.SandboxID == x.sandbox && a.ChatPermission == nil {
				return stateBlocked
			}
		}
	default:
		return stateNone
	}
	switch {
	case m.sess.working[x.id()]:
		return stateWorking
	case m.sess.done[x.id()]:
		return stateDone
	case x.kind == sessionAgent && x.term.Activity == 0:
		return stateUnknown
	}
	return stateIdle
}

// homeRow is a row of Home's list: a sandbox, by its index among the visible ones, or one of its
// sessions, by its index under it (sub); sub is -1 for the sandbox's own row.
type homeRow struct {
	sb, sub int
	num     int // the session's number, 1 to 9, or 0
}

// homeRows are the rows of Home's list, in order.
func (m *app) homeRows() []homeRow {
	var rows []homeRow
	num := 0
	for i, sb := range m.visible() {
		rows = append(rows, homeRow{sb: i, sub: -1})
		for j, x := range m.sessionsOf(sb) {
			r := homeRow{sb: i, sub: j}
			if x.kind != sessionOlder && num < 9 {
				num++
				r.num = num
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// cursorRow is the index of the cursor's row among homeRows.
func (m *app) cursorRow(rows []homeRow) int {
	sub := m.subRow()
	for i, r := range rows {
		if r.sb == m.cursor && r.sub == sub {
			return i
		}
	}
	return 0
}

// subRow is the session under the cursor's sandbox that the cursor is on, -1 for the sandbox
// itself. A session that went is replaced by the last one left.
func (m *app) subRow() int {
	sb := m.selected()
	if sb == nil {
		return -1
	}
	m.sub = min(m.sub, len(m.sessionsOf(*sb))-1)
	return m.sub
}

// selectedSession is the session under the cursor, if the cursor is on one.
func (m *app) selectedSession() (session, bool) {
	sb := m.selected()
	if sb == nil {
		return session{}, false
	}
	list := m.sessionsOf(*sb)
	if sub := m.subRow(); sub >= 0 && sub < len(list) {
		return list[sub], true
	}
	return session{}, false
}

// moveRow moves the cursor by dir rows of Home's list.
func (m *app) moveRow(dir int) {
	rows := m.homeRows()
	if len(rows) == 0 {
		return
	}
	i := min(max(m.cursorRow(rows)+dir, 0), len(rows)-1)
	m.cursor, m.sub = rows[i].sb, rows[i].sub
}

// numbered enters the session with the number n.
func (m *app) numbered(n int) tea.Cmd {
	for _, r := range m.homeRows() {
		if r.num == n {
			m.cursor, m.sub = r.sb, r.sub
			return m.enterSession()
		}
	}
	return nil
}

// nextWaiting moves the cursor to the next session that waits for the user, blocked ones first,
// then done ones, after the cursor and around.
func (m *app) nextWaiting() {
	rows := m.homeRows()
	visible := m.visible()
	at := m.cursorRow(rows)
	for _, want := range []agentState{stateBlocked, stateDone} {
		for k := 1; k <= len(rows); k++ {
			r := rows[(at+k)%len(rows)]
			if r.sub < 0 {
				continue
			}
			if m.stateOf(m.sessionsOf(visible[r.sb])[r.sub]) == want {
				m.cursor, m.sub = r.sb, r.sub
				return
			}
		}
	}
	m.setNotice("No session waits for you.", false)
}

// enterSession enters the session under the cursor: a terminal is attached, a chat opened (an
// idle one with the composer ready), and the older row shows the older chats.
func (m *app) enterSession() tea.Cmd {
	x, ok := m.selectedSession()
	sb := m.selected()
	if !ok || sb == nil {
		return nil
	}
	// What it does from now on is seen: only a change after the user left it is news.
	delete(m.sess.done, x.id())
	delete(m.sess.working, x.id())
	switch x.kind {
	case sessionOlder:
		if m.sess.older == nil {
			m.sess.older = map[string]bool{}
		}
		m.sess.older[x.sandbox] = true
		return nil
	case sessionChat:
		m.chats = chatState{sandbox: *sb, back: m.screen, fold: -1}
		cmd := m.openChat(x.chat.ID)
		if x.chat.State == store.ChatIdle && m.stateOf(x) != stateBlocked {
			m.chats.composing = true
		}
		return cmd
	}
	return m.attachTo(sb, x.term.Name)
}

// startAgent starts the sandbox teammate's harness in a new terminal and attaches it.
func (m *app) startAgent(sb *sandboxes.View) tea.Cmd {
	if sb == nil {
		return nil
	}
	if sb.TeammateID == "" {
		m.setNotice(sb.Name+" has no teammate whose agent could run. Give it one in the browser.", true)
		return nil
	}
	if phaseOf(*sb) != phaseReady {
		m.setNotice("Start "+sb.Name+" first, with s.", false)
		return nil
	}
	m.setNotice("Starting "+m.teammates[sb.TeammateID]+"'s agent in "+sb.Name+"…", false)
	ctx, client, env, view := m.ctx, m.client, m.env.ID, *sb
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		started, err := client.StartSession(rctx, env, view.ID)
		return agentStarted{sandbox: view, name: started.Name, err: err}
	}
}

// stateMark is a state's mark, styled: ● working, ▲ blocked, ✓ done, ○ idle, · unknown.
func (m *app) stateMark(st agentState) string {
	switch st {
	case stateWorking:
		if !m.look.plain {
			return m.pal.running.Render(m.spinner())
		}
		return styleAccent.Render("●")
	case stateBlocked:
		if !m.look.plain {
			return m.pal.waiting.Render("▲")
		}
		return styleAccent.Render("▲")
	case stateDone:
		return styleAccent.Render("✓")
	case stateIdle:
		return styleDim.Render("○")
	case stateUnknown:
		return styleDim.Render("·")
	}
	return " "
}

// sessionWords say what a session is and how it is.
func (m *app) sessionWords(x session, st agentState) string {
	kind := map[sessionKind]string{sessionAgent: "agent", sessionChat: "chat", sessionTerminal: "terminal"}[x.kind]
	if x.kind == sessionChat && x.chat.Heartbeat {
		kind = "check-in"
	}
	switch {
	case x.kind == sessionOlder:
		return ""
	case st == stateBlocked:
		return kind + " · blocked"
	case st == stateWorking:
		return kind + " · working"
	case st == stateDone:
		return kind + " · done"
	case x.kind == sessionChat && x.chat.State != store.ChatIdle:
		return kind + " · " + strings.ToLower(chatStateWords[x.chat.State])
	case x.kind == sessionChat:
		return kind + " · " + age(m.now.Sub(x.chat.UpdatedAt))
	case x.term.Attached > 0:
		return kind + " · attached"
	}
	return kind
}

// sessionRow is a session's row in w cells: its number and state where the sandbox's status
// is, its title in nameW cells where the sandbox's name is, then what it is.
func (m *app) sessionRow(x session, num int, selected bool, nameW, w int) string {
	n := " "
	if num > 0 {
		n = fmt.Sprint(num)
	}
	st := m.stateOf(x)
	title := fit(x.title(), nameW)
	switch {
	case x.kind == sessionOlder:
		title = styleDim.Render(title)
	case selected:
		title = styleBold.Render(title)
	}
	lead := m.cursorMark(selected) + fitStyled("  "+styleDim.Render(n)+"  "+m.stateMark(st), colStatus) + " "
	return lead + title + " " + styleDim.Render(clip(m.sessionWords(x, st), max(w-2-colStatus-1-nameW-1, 0)))
}

// sessionCounts count the sessions of the environment that work and that are blocked, for the
// header.
func (m *app) sessionCounts() (working, blocked int) {
	for _, sb := range m.sandboxes {
		for _, x := range m.sessionsOf(sb) {
			switch m.stateOf(x) {
			case stateWorking:
				working++
			case stateBlocked:
				blocked++
			}
		}
	}
	return working, blocked
}

// sessionsHeader is the header's word on the sessions: "2 working · 1 blocked · ", or nothing.
func (m *app) sessionsHeader() string {
	working, blocked := m.sessionCounts()
	var parts []string
	if working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", working))
	}
	if blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", blocked))
	}
	if len(parts) == 0 {
		return ""
	}
	return styleDim.Render(strings.Join(parts, " · ") + " · ")
}
