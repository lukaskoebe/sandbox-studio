package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Home's sidebar lists under each teammate its threads, as the web's sidebar does: its chats, its
// agents in a terminal with no chat behind them, and its computers' other terminals; under each
// computer of yours, its terminals (docs/tui.md). The first nine get the numbers 1 to 9, which
// enter them from Home.

// Timing of the sessions: they are read again every sessionsEvery while Home shows, and on every
// refresh. Besides the threads that run or wait, a teammate lists the recentThreads latest others
// of the last recentFor.
const (
	sessionsEvery = 10 * time.Second
	recentFor     = 24 * time.Hour
	recentThreads = 2
)

// sessionKind is what a session row enters.
type sessionKind int

const (
	sessionAgent    sessionKind = iota // a terminal that runs a harness
	sessionChat                        // a thread with a teammate
	sessionTerminal                    // any other terminal
	sessionOlder                       // the row that shows a teammate's older threads
	sessionReview                      // a git push or pull request that waits for your review
)

// session is a row under a teammate or a computer: a terminal by its tmux name, or a chat.
type session struct {
	kind    sessionKind
	sandbox string // the computer it runs on; the older row's is the teammate's first computer
	mate    string // the teammate whose row it is under, if any
	term    sandboxes.Terminal
	chat    store.Chat
	older   int              // the threads the older row hides
	review  api.ApprovalView // the push or pull request a review row opens
}

// id names a session across reads: its computer and terminal, or its chat.
func (s session) id() string {
	switch s.kind {
	case sessionChat:
		return "chat/" + s.chat.ID
	case sessionReview:
		return "review/" + s.review.ID
	case sessionOlder:
		return "older/" + s.mate
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
	case sessionReview:
		return reviewTitle(s.review)
	}
	return firstNonEmpty(s.term.Label, s.term.Name)
}

// agentState is a session's agent as herdr words it.
type agentState int

const (
	stateNone    agentState = iota // a plain terminal, which has no agent
	stateIdle                      // nothing runs and nothing waits
	stateWorking                   // a turn runs, or the terminal prints
	stateBlocked                   // a request waits for the user
	stateDone                      // it worked, and the user hasn't entered it since
	stateUnknown                   // the guest doesn't say when the terminal printed
)

// sessionsState is what Home knows of the threads and terminals of the environment.
type sessionsState struct {
	env     string
	terms   map[string][]sandboxes.Terminal // by computer ID, for the running ones
	chats   map[string][]store.Chat         // by computer ID, the newest activity first
	working map[string]bool                 // the sessions that worked at the last read, by id
	done    map[string]bool                 // the sessions that stopped working since they were entered
	older   map[string]bool                 // the teammates whose older threads show
	busy    bool                            // the watch's read, or its wait for the next one, is under way
	gen     int                             // counts the environments read, so a read for an earlier one is dropped
}

type (
	sessionsLoaded struct {
		gen   int
		tick  bool // the watch's read, which the next one follows
		at    time.Time
		terms map[string][]sandboxes.Terminal // nil when the read failed
		chats map[string][]store.Chat         // nil when the read failed
	}
	sessionsTick struct{ gen int }
	agentStarted struct {
		sandbox sandboxes.View
		name    string
		err     error
	}
)

// watchSessions reads the threads and terminals of the environment while Home shows, again every
// sessionsEvery. A model without a Studio to ask (a test's) reads none.
func (m *app) watchSessions() tea.Cmd {
	if m.client == nil || m.screen != screenHome || m.env.ID == "" || !m.loaded || m.sess.busy {
		return nil
	}
	cmd := m.readSessions(true)
	m.sess.busy = true
	return cmd
}

// readSessions reads every chat and terminal of the environment, two requests whatever the
// number of computers. tick marks the watch's read.
func (m *app) readSessions(tick bool) tea.Cmd {
	s := &m.sess
	if m.client == nil || m.env.ID == "" {
		return nil
	}
	if s.env != m.env.ID {
		*s = sessionsState{env: m.env.ID, gen: s.gen + 1}
	}
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, s.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		out := sessionsLoaded{gen: gen, tick: tick}
		if list, err := client.EnvironmentTerminals(rctx, env); err == nil {
			out.terms = map[string][]sandboxes.Terminal{}
			for _, st := range list {
				out.terms[st.SandboxID] = st.Terminals
			}
		}
		if list, err := client.EnvironmentChats(rctx, env); err == nil {
			out.chats = map[string][]store.Chat{}
			for _, c := range byActivity(list) {
				out.chats[c.SandboxID] = append(out.chats[c.SandboxID], c)
			}
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
		if !msg.tick {
			return nil, true
		}
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
// done until the user enters it, and says so. The cursor stays on its session, wherever the read
// puts it, so that the pane keeps showing it.
func (m *app) sessionsRead(msg sessionsLoaded) {
	s := &m.sess
	at, onSession := m.selectedSession()
	if msg.terms != nil {
		s.terms = msg.terms
	}
	if msg.chats != nil {
		s.chats = msg.chats
	}
	if list, ok := m.rowsUnder(); ok && onSession {
		for i, x := range list {
			if x.id() == at.id() {
				m.sub = i
			}
		}
	}
	working := map[string]bool{}
	for _, x := range m.allSessions() {
		if x.kind == sessionOlder {
			continue
		}
		busy := x.kind == sessionChat && x.chat.State == store.ChatRunning ||
			x.kind == sessionAgent && x.term.State == sandboxes.TerminalWorking
		if busy {
			working[x.id()] = true
			delete(s.done, x.id())
			continue
		}
		if s.working[x.id()] && !m.onShow(x.id()) { // the pane shows its end
			if s.done == nil {
				s.done = map[string]bool{}
			}
			s.done[x.id()] = true
			where := ""
			if sb, ok := m.sandboxByID(x.sandbox); ok {
				where = " in " + sb.Name
			}
			m.report(x.title()+where+" is done.", false)
		}
	}
	s.working = working
}

// yourComputers are the visible computers without a teammate, in the list's order, by their
// index among the visible ones. A computer whose teammate is gone is yours too.
func (m *app) yourComputers() []int {
	known := map[string]bool{}
	for _, t := range m.team {
		known[t.ID] = true
	}
	var out []int
	for i, sb := range m.visible() {
		if !known[sb.TeammateID] {
			out = append(out, i)
		}
	}
	return out
}

// allSessions are the rows under every teammate and every computer of yours.
func (m *app) allSessions() []session {
	var out []session
	for _, t := range m.team {
		out = append(out, m.threadsOf(t.ID)...)
	}
	visible := m.visible()
	for _, i := range m.yourComputers() {
		out = append(out, m.sessionsOf(visible[i])...)
	}
	return out
}

// sessionsOf are a computer's terminals: the ones that run an agent first, then the others.
func (m *app) sessionsOf(sb sandboxes.View) []session {
	var agents, plain []session
	for _, t := range m.sess.terms[sb.ID] {
		x := session{kind: sessionTerminal, sandbox: sb.ID, term: t}
		if t.Harness != "" {
			x.kind = sessionAgent
			agents = append(agents, x)
		} else {
			plain = append(plain, x)
		}
	}
	return append(agents, plain...)
}

// threadsOf are the rows under a teammate: its agents in a terminal, its threads as the web's
// sidebar lists them (web/src/lib/threads.ts sidebarChats: every one that runs, waits or is in
// the terminal, the two latest others of the last day, and the one on show), a "+ n older" row
// for the rest, and its computers' other terminals. Its threads are the chats with it on any
// computer, as the web's are, and the chats of its own computers that name no teammate. A chat
// continued in a terminal is listed once, as its terminal.
func (m *app) threadsOf(mate string) []session {
	s := &m.sess
	var agents, plain []session
	var chats []store.Chat
	inTerminal := map[string]bool{}
	own := map[string]bool{}
	first := ""
	for _, sb := range m.sandboxesOf(mate) {
		own[sb.ID] = true
		if first == "" {
			first = sb.ID
		}
		for _, x := range m.sessionsOf(sb) {
			x.mate = mate
			if x.kind == sessionAgent {
				agents = append(agents, x)
			} else {
				plain = append(plain, x)
			}
			if x.term.Chat != "" {
				inTerminal[x.term.Chat] = true
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(s.chats)) { // in one order, for equal times
		for _, c := range s.chats[id] {
			if c.TeammateID == mate || c.TeammateID == "" && own[id] {
				chats = append(chats, c)
			}
		}
	}
	byActivity(chats)
	out := agents
	open := ""
	if m.chats.id != "" && (m.screen == screenChat || m.chats.inPane) {
		open = m.chats.id
	}
	recent, hidden := 0, 0
	for _, c := range chats {
		if inTerminal[c.ID] {
			continue
		}
		live := c.State == store.ChatRunning || c.State == store.ChatTerminal || len(m.threadWaiting(c)) > 0
		isRecent := !live && recent < recentThreads && m.now.Sub(c.UpdatedAt) < recentFor
		if isRecent {
			recent++
		}
		if live || isRecent || c.ID == open || s.older[mate] {
			out = append(out, session{kind: sessionChat, sandbox: c.SandboxID, mate: mate, chat: c})
		} else {
			hidden++
		}
	}
	if hidden > 0 {
		out = append(out, session{kind: sessionOlder, sandbox: first, mate: mate, older: hidden})
	}
	return append(out, plain...)
}

// threadWaiting are the requests a thread waits on: its own permission asks, and its computer's
// other requests while it is the teammate's latest thread there, as a push it asks for is
// (web/src/lib/threads.ts chatApprovals).
func (m *app) threadWaiting(c store.Chat) []api.ApprovalView {
	latest := ""
	for _, other := range m.sess.chats[c.SandboxID] { // the newest first
		if other.TeammateID == c.TeammateID {
			latest = other.ID
			break
		}
	}
	var out []api.ApprovalView
	for _, a := range m.approvals {
		switch {
		case a.ChatPermission != nil:
			if a.ChatPermission.ChatID == c.ID {
				out = append(out, a)
			}
		case latest == c.ID && a.SandboxID == c.SandboxID && (a.Teammate == nil || c.TeammateID == "" || a.Teammate.ID == c.TeammateID):
			out = append(out, a)
		}
	}
	return out
}

// reviewTitle names a review by its branch, or its pull request's.
func reviewTitle(a api.ApprovalView) string {
	if a.Git == nil {
		return a.Subject
	}
	if pr := a.Git.PullRequest; pr != nil {
		return pr.Head + " → " + pr.Base
	}
	if r := a.Git.Review; r != nil && r.Branch != "" {
		return r.Branch
	}
	return strings.TrimPrefix(a.Git.Push.Ref, "refs/heads/")
}

// stateOf is a session's state. A thread waits while a request of its own waits (threadWaiting);
// an agent terminal while any other request waits on its computer, since a request doesn't say
// which process asked.
func (m *app) stateOf(x session) agentState {
	switch x.kind {
	case sessionReview:
		return stateBlocked
	case sessionChat:
		if len(m.threadWaiting(x.chat)) > 0 {
			return stateBlocked
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

// rowsUnder are the rows the cursor's sub indexes: the threads of the teammate whose thread it is
// on, or the terminals of the computer of yours it is on. ok is false elsewhere.
func (m *app) rowsUnder() ([]session, bool) {
	switch m.at {
	case atThread:
		return m.threadsOf(m.mate), true
	case atSandbox:
		if sb := m.selected(); sb != nil {
			return m.sessionsOf(*sb), true
		}
	}
	return nil, false
}

// subRow is the row under the cursor's teammate or computer that the cursor is on, -1 for the
// computer itself. A session that went is replaced by the last one left.
func (m *app) subRow() int {
	list, ok := m.rowsUnder()
	if !ok {
		return -1
	}
	m.sub = min(m.sub, len(list)-1)
	return m.sub
}

// selectedSession is the session under the cursor, if the cursor is on one.
func (m *app) selectedSession() (session, bool) {
	list, ok := m.rowsUnder()
	if !ok {
		return session{}, false
	}
	if sub := m.subRow(); sub >= 0 && sub < len(list) {
		return list[sub], true
	}
	return session{}, false
}

// rowSession is the session a row of Home's list is, if it is one.
func (m *app) rowSession(r homeRow, visible []sandboxes.View) (session, bool) {
	var list []session
	switch {
	case r.kind == atThread:
		list = m.threadsOf(r.mate)
	case r.kind == atSandbox && r.sb >= 0 && r.sb < len(visible):
		list = m.sessionsOf(visible[r.sb])
	}
	if r.sub < 0 || r.sub >= len(list) {
		return session{}, false
	}
	return list[r.sub], true
}

// moveRow moves the cursor by dir rows of Home's list.
func (m *app) moveRow(dir int) {
	rows := m.homeRows()
	if len(rows) == 0 {
		return
	}
	i := min(max(m.cursorRow(rows)+dir, 0), len(rows)-1)
	m.setCursor(rows[i])
	m.blurButtons()
	m.pane.answer, m.inbox.sidebar = false, true
}

// numbered enters the session with the number n: Home's pane takes the keys for it, or, on a
// narrow terminal, its terminal or thread takes the screen.
func (m *app) numbered(n int) tea.Cmd {
	for _, r := range m.homeRows() {
		if r.num == n {
			m.setCursor(r)
			if _, ok := m.paneTarget(); ok {
				return m.focusPane()
			}
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
			if x, ok := m.rowSession(r, visible); ok && m.stateOf(x) == want {
				m.setCursor(r)
				return
			}
		}
	}
	if len(m.cards()) > 0 {
		m.goHome() // what else waits is on Home's list, first
		m.inbox.cursor, m.inbox.at = "", 0
		return
	}
	m.setNotice("Nothing waits for you.", false)
}

// enterSession enters the session under the cursor: a terminal is attached, a thread opened (an
// idle one with the composer ready), and the older row shows the older threads.
func (m *app) enterSession() tea.Cmd {
	x, ok := m.selectedSession()
	if !ok || m.selected() == nil {
		return nil
	}
	return m.enterOf(x)
}

// enterOf enters the session x, from the screen on show.
func (m *app) enterOf(x session) tea.Cmd {
	sb, ok := m.sandboxByID(x.sandbox)
	if x.kind == sessionChat {
		sb, ok = m.sandboxByID(x.chat.SandboxID)
	}
	if !ok && x.kind != sessionOlder {
		return nil
	}
	// What it does from now on is seen: only a change after the user left it is news.
	delete(m.sess.done, x.id())
	delete(m.sess.working, x.id())
	switch x.kind {
	case sessionOlder:
		m.showOlder(x.mate, true)
		return nil
	case sessionReview:
		m.openPager(x.review)
		m.pager.back = screenHome
		return nil
	case sessionChat:
		m.chats = chatState{sandbox: sb, back: m.screen, fold: -1, gen: m.chats.gen}
		cmd := m.openChat(x.chat.ID)
		if x.chat.State == store.ChatIdle && m.stateOf(x) != stateBlocked {
			m.chats.composing = true
		}
		return cmd
	}
	return m.attachTo(&sb, x.term.Name)
}

// showOlder shows or hides a teammate's older threads.
func (m *app) showOlder(mate string, show bool) {
	if m.sess.older == nil {
		m.sess.older = map[string]bool{}
	}
	m.sess.older[mate] = show
}

// startAgent starts the computer teammate's harness in a new terminal and attaches it.
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

// stateMark is a state's mark, styled: the spinner working, ▲ waiting, ✓ done, ○ idle, · unknown.
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

// sessionMark is a session's mark on its row: its state's, and ◷ for an idle scheduled run or
// check-in.
func (m *app) sessionMark(x session, st agentState) string {
	if st == stateIdle && x.kind == sessionChat && (x.chat.TaskID != "" || x.chat.Heartbeat) {
		return styleDim.Render("◷")
	}
	return m.stateMark(st)
}

// sessionWords say what a session is and how it is.
func (m *app) sessionWords(x session, st agentState) string {
	kind := map[sessionKind]string{sessionAgent: "in the terminal", sessionChat: "thread", sessionTerminal: "terminal", sessionReview: "review"}[x.kind]
	switch {
	case x.kind == sessionChat && x.chat.Heartbeat:
		kind = "check-in"
	case x.kind == sessionChat && x.chat.TaskID != "":
		kind = "scheduled"
	}
	switch {
	case x.kind == sessionOlder:
		return ""
	case x.kind == sessionReview:
		return "review · waiting"
	case st == stateBlocked:
		return kind + " · waiting"
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

// sessionKindWord is what a session is, in a word: thread, agent, term, sched or check.
func sessionKindWord(x session) string {
	switch {
	case x.kind == sessionChat && x.chat.Heartbeat:
		return "check"
	case x.kind == sessionChat && x.chat.TaskID != "":
		return "sched"
	}
	return map[sessionKind]string{sessionAgent: "agent", sessionChat: "thread", sessionTerminal: "term", sessionReview: "review"}[x.kind]
}

// sessionRow is a session's row in w cells, indent cells in: its number, its mark and its
// title, and on the right what it is when there's room; a wide list says how it is too.
func (m *app) sessionRow(x session, num int, selected bool, indent, w int) string {
	pad := strings.Repeat(" ", indent)
	w -= indent
	if x.kind == sessionOlder {
		return m.cursorMark(selected) + pad + "    " + styleDim.Render(clip(x.title(), max(w-6, 0)))
	}
	n := " "
	if num > 0 {
		n = fmt.Sprint(num)
	}
	st := m.stateOf(x)
	lead := m.cursorMark(selected) + pad + styleDim.Render(n) + " " + m.sessionMark(x, st) + " "
	right := ""
	switch {
	case w >= 64:
		right = m.sessionWords(x, st)
	case w >= 40:
		right = sessionKindWord(x)
	}
	titleW := w - 6 - lipgloss.Width(right) - 1
	if titleW < 8 {
		right, titleW = "", w-6
	}
	title := fit(x.title(), max(titleW, 0))
	if right == "" {
		title = clip(x.title(), max(w-6, 0))
	}
	if selected {
		title = styleBold.Render(title)
	}
	if right == "" {
		return lead + title
	}
	return lead + title + " " + styleDim.Render(right)
}

// sessionCounts count the sessions of the environment that work and that wait, for the header.
func (m *app) sessionCounts() (working, blocked int) {
	for _, x := range m.allSessions() {
		switch m.stateOf(x) {
		case stateWorking:
			working++
		case stateBlocked:
			blocked++
		}
	}
	return working, blocked
}

// sessionsHeader is the header's word on the sessions: "2 working · ", or nothing. What waits
// is the "▲ n waiting for you" beside it.
func (m *app) sessionsHeader() string {
	working, _ := m.sessionCounts()
	if working == 0 {
		return ""
	}
	return styleDim.Render(fmt.Sprintf("%d working · ", working))
}
