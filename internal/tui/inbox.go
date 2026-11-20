package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/lead"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Home is one list of everything, the rows that need you first (docs/tui.md): WAITING FOR YOU,
// the requests of every environment, each answered with a key from its row; WORKING, the
// threads that work now; QUIET, the threads done or ended today, each teammate's next scheduled
// run and your own computers. A reading pane beside the list, or under it on a narrow terminal,
// shows the row under the cursor: a push's review, a request in full, a thread as it streams, a
// computer's dashboard. \ gives the keys to the list of teammates and computers, and back.

// inboxSection is a section of Home's list.
type inboxSection int

const (
	inboxWaiting inboxSection = iota // WAITING FOR YOU
	inboxWorking                     // WORKING
	inboxQuiet                       // QUIET
)

var inboxLabels = [...]string{"WAITING FOR YOU", "WORKING", "QUIET"}

// quietThreads is how many threads QUIET lists at most, and quietWithin how recent they are.
const (
	quietThreads = 5
	quietWithin  = 24 * time.Hour
)

// tasksEvery is how often Home reads the teammates' tasks, for their next scheduled runs.
const tasksEvery = 30 * time.Second

// inboxRow is one row of Home's list.
type inboxRow struct {
	section inboxSection
	id      string      // "card/"+its first approval's ID, a session's id(), "pc/"+a computer's ID, "task/"+a task's ID
	card    *card       // waiting: a request, or a group of them
	thread  *session    // working and quiet: a thread, or an agent in a terminal
	sb      string      // quiet: a computer of yours
	task    *store.Task // quiet: a teammate's next scheduled run
	lead    bool        // quiet: Lead and today's day
}

// inboxState is Home's list: the cursor, by its row's id and index, the reading pane's scroll,
// and the push whose y waits for a second y.
type inboxState struct {
	cursor  string // the row's id, so a refresh keeps the cursor on it
	at      int    // the row's index, where the cursor goes when its row went: the next request
	scroll  int    // the reading pane's, in lines
	pushing string // the push whose y asked first
	tasks   map[string][]store.Task
	lead    *lead.LeadView    // the environment's Lead, nil before it is read or without one
	leadEnv string            // the environment lead and day are of
	day     *lead.LeadDayView // today's day, when Lead shows
	busy    bool              // a read of the tasks, or the wait for the next one, is under way
	gen     int
	review  struct {
		id    string
		w     int
		lines []string
	}
}

type (
	inboxTasksLoaded struct {
		gen   int
		tasks map[string][]store.Task
		lead  leadLoaded
	}
	inboxTasksTick struct{ gen int }
)

var (
	keyNextRow  = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next"))
	keyReadDn   = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll"))
	keyReadUp   = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll"))
	keyPushYes  = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "push"))
	keyGoThread = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "thread"))
)

// inboxShows reports whether Home's list is on show.
func (m *app) inboxShows() bool {
	return m.screen == screenMain && m.here.kind == placeHome && m.env.ID != "" && m.loaded
}

// inboxKeys reports whether Home's list takes the keys.
func (m *app) inboxKeys() bool {
	return m.inboxShows() && m.focus == focusMain && !m.drawer && !m.paneFocused()
}

// inboxThreads are the threads Home lists: the chats with each teammate, as threadsOf has them
// but older ones too, and the agents in a terminal, a teammate's and yours. A chat continued in
// a terminal is the terminal.
func (m *app) inboxThreads() []session {
	var out []session
	ids := slices.Sorted(maps.Keys(m.sess.chats)) // in one order, for equal times
	for _, t := range m.team {
		own := map[string]bool{}
		inTerminal := map[string]bool{}
		for _, sb := range m.sandboxesOf(t.ID) {
			own[sb.ID] = true
			for _, x := range m.sessionsOf(sb) {
				if x.term.Chat != "" {
					inTerminal[x.term.Chat] = true
				}
				if x.kind == sessionAgent {
					x.mate = t.ID
					out = append(out, x)
				}
			}
		}
		for _, id := range ids {
			for _, c := range m.sess.chats[id] {
				if (c.TeammateID == t.ID || c.TeammateID == "" && own[id]) && !inTerminal[c.ID] {
					out = append(out, session{kind: sessionChat, sandbox: c.SandboxID, mate: t.ID, chat: c})
				}
			}
		}
	}
	visible := m.visible()
	for _, i := range m.yourComputers() {
		for _, x := range m.sessionsOf(visible[i]) {
			if x.kind == sessionAgent {
				out = append(out, x)
			}
		}
	}
	return out
}

// sessionTime is when a thread last changed: a chat's update, a terminal's latest output.
func sessionTime(x session) time.Time {
	if x.kind == sessionChat {
		return x.chat.UpdatedAt
	}
	if x.term.Activity > 0 {
		return time.Unix(x.term.Activity, 0)
	}
	return time.Time{}
}

// nextRuns are each teammate's next scheduled run, in the teammates' order: the open task with
// the earliest next run.
func (m *app) nextRuns() []store.Task {
	var out []store.Task
	for _, t := range m.team {
		var next *store.Task
		for _, task := range m.inbox.tasks[t.ID] {
			if task.NextRunAt == nil || task.Status == "done" {
				continue
			}
			if next == nil || task.NextRunAt.Before(*next.NextRunAt) {
				next = &task
			}
		}
		if next != nil {
			out = append(out, *next)
		}
	}
	return out
}

// inboxRows are the rows of Home's list, in order: the requests, the threads at work, and the
// quiet ones, the next runs and your computers.
func (m *app) inboxRows() []inboxRow {
	var rows []inboxRow
	for _, c := range m.cards() {
		rows = append(rows, inboxRow{section: inboxWaiting, id: "card/" + c.first().ID, card: &c})
	}
	runs := m.nextRuns()
	listed := map[string]bool{} // the tasks with a row: their runs' threads don't get one
	for _, t := range runs {
		listed[t.ID] = true
	}
	var quiet []session
	for _, x := range m.inboxThreads() {
		st := m.stateOf(x)
		switch {
		case st == stateWorking:
			rows = append(rows, inboxRow{section: inboxWorking, id: x.id(), thread: &x})
		case st == stateBlocked: // its request waits above
		case x.kind == sessionChat && listed[x.chat.TaskID]:
		case x.kind == sessionChat && m.now.Sub(x.chat.UpdatedAt) < quietWithin, st == stateDone:
			quiet = append(quiet, x)
		}
	}
	if v := m.inbox.lead; leadShows(v) && m.inbox.leadEnv == m.env.ID {
		rows = append(rows, inboxRow{section: inboxQuiet, id: "lead", lead: true})
	}
	slices.SortStableFunc(quiet, func(a, b session) int { return sessionTime(b).Compare(sessionTime(a)) })
	for _, x := range quiet[:min(len(quiet), quietThreads)] {
		rows = append(rows, inboxRow{section: inboxQuiet, id: x.id(), thread: &x})
	}
	for _, t := range runs {
		rows = append(rows, inboxRow{section: inboxQuiet, id: "task/" + t.ID, task: &t})
	}
	visible := m.visible()
	for _, i := range m.yourComputers() {
		rows = append(rows, inboxRow{section: inboxQuiet, id: "pc/" + visible[i].ID, sb: visible[i].ID})
	}
	return rows
}

// inboxCursor is the index of the cursor's row among rows, or -1 for none. A row that went
// leaves the cursor at its index, on the row after it.
func (m *app) inboxCursor(rows []inboxRow) int {
	in := &m.inbox
	if len(rows) == 0 {
		return -1
	}
	if in.cursor == "" { // not moved yet: the top row, whatever arrives above it
		in.at = 0
		return 0
	}
	for i, r := range rows {
		if r.id == in.cursor {
			in.at = i
			return i
		}
	}
	in.at = min(max(in.at, 0), len(rows)-1)
	in.cursor = rows[in.at].id
	in.scroll = 0
	return in.at
}

// inboxRow is the row under the cursor.
func (m *app) inboxRow() (inboxRow, bool) {
	rows := m.inboxRows()
	if i := m.inboxCursor(rows); i >= 0 {
		return rows[i], true
	}
	return inboxRow{}, false
}

// inboxTo puts the cursor on the row at index i.
func (m *app) inboxTo(rows []inboxRow, i int) {
	in := &m.inbox
	in.at, in.cursor, in.scroll, in.pushing = i, rows[i].id, 0, ""
	m.blurButtons()
}

// inboxMove moves the cursor by dir rows.
func (m *app) inboxMove(dir int) {
	rows := m.inboxRows()
	if i := m.inboxCursor(rows); i >= 0 {
		m.inboxTo(rows, min(max(i+dir, 0), len(rows)-1))
	}
}

// inboxNext moves the cursor to the next request, around the end.
func (m *app) inboxNext() {
	rows := m.inboxRows()
	at := m.inboxCursor(rows)
	for k := 1; k <= len(rows); k++ {
		if i := (at + k) % len(rows); rows[i].section == inboxWaiting {
			m.inboxTo(rows, i)
			return
		}
	}
	m.setNotice("Nothing waits for you.", false)
}

// inboxOn puts the cursor on the request's row, from wherever the user is.
func (m *app) inboxOn(id string) {
	for i, c := range m.cards() {
		for _, a := range c.approvals {
			if a.ID == id {
				m.inbox.cursor, m.inbox.at, m.inbox.scroll, m.inbox.pushing = "card/"+c.first().ID, i, 0, ""
				return
			}
		}
	}
	m.inbox.cursor, m.inbox.at = "", 0
}

// watchInbox reads the teammates' tasks while Home's list shows, again every tasksEvery.
func (m *app) watchInbox() tea.Cmd {
	in := &m.inbox
	if m.client == nil || !m.inboxShows() || in.busy || len(m.team) == 0 {
		return nil
	}
	in.busy = true
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, in.gen
	team := make([]string, 0, len(m.team))
	for _, t := range m.team {
		team = append(team, t.ID)
	}
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		out := inboxTasksLoaded{gen: gen, tasks: map[string][]store.Task{}}
		for _, id := range team {
			var list []store.Task
			if err := client.Do(rctx, "GET", "/api/environments/"+env+"/teammates/"+id+"/tasks", nil, &list); err != nil {
				return inboxTasksLoaded{gen: gen}
			}
			out.tasks[id] = list
		}
		out.lead = fetchLead(rctx, client, env)
		return out
	}
}

// inboxUpdate applies the messages of Home's list. ok is false for other messages.
func (m *app) inboxUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	in := &m.inbox
	switch msg := msg.(type) {
	case inboxTasksLoaded:
		if msg.gen != in.gen {
			return nil, true
		}
		if msg.tasks != nil {
			in.tasks = msg.tasks
			m.takeLead(msg.lead)
		}
		gen := in.gen
		return tea.Tick(tasksEvery, func(time.Time) tea.Msg { return inboxTasksTick{gen} }), true
	case leadLoaded:
		m.takeLead(msg)
		return nil, true
	case inboxTasksTick:
		if msg.gen == in.gen {
			in.busy = false
		}
		return nil, true
	}
	return nil, false
}

// inboxKey answers the keys of Home's list: the rows, the reading pane, the ways about Studio,
// and the answers of the request under the cursor.
func (m *app) inboxKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	in := &m.inbox
	if in.pushing != "" {
		return m, m.pushAskKey(msg)
	}
	rows := m.inboxRows()
	i := m.inboxCursor(rows)
	var r inboxRow
	if i >= 0 {
		r = rows[i]
	}
	if r.card != nil {
		c := *r.card
		if k, ok := m.buttonKey(msg, cardOwner(c), cardControls(c, *m.choiceOf(c)), len(cardToggles(c))); ok {
			if k == "" {
				return m, nil
			}
			msg = pressOf(k) // the focused control's key
		}
	}
	switch {
	case key.Matches(msg, keyUndo) && m.undoable() != nil:
		return m, m.undo()
	case key.Matches(msg, keyUp):
		m.inboxMove(-1)
	case key.Matches(msg, keyDown):
		m.inboxMove(1)
	case key.Matches(msg, keyNextRow):
		m.inboxNext()
	case key.Matches(msg, keyEsc):
		m.blurButtons()
		if m.resuming != nil {
			return m, m.leaveWaiting()
		}
	case key.Matches(msg, keyQuit):
		return m, tea.Quit
	case key.Matches(msg, keyHelp):
		m.openHelp()
	case key.Matches(msg, keySettings):
		return m, m.openSettings()
	case key.Matches(msg, keyActivity):
		return m, m.openActivity("")
	case key.Matches(msg, keyEnv):
		m.openEnvs()
	case key.Matches(msg, keyNewComputer):
		if m.env.ID != "" {
			return m, m.openNew()
		}
	case key.Matches(msg, keyReadDn):
		in.scroll += max(m.readingRows()-1, 1)
	case key.Matches(msg, keyReadUp):
		in.scroll = max(in.scroll-max(m.readingRows()-1, 1), 0)
	case i < 0:
	case r.card != nil:
		return m, m.inboxCardKey(msg, *r.card)
	case r.lead && key.Matches(msg, keyWriteDay):
		return m, m.writeDay()
	case key.Matches(msg, keyOpen):
		return m, m.inboxOpen(r)
	}
	return m, nil
}

// readingRows is how many rows the reading pane has, for a page of it.
func (m *app) readingRows() int {
	if rc, ok := m.readingRect(); ok {
		return rc.h
	}
	return m.bodyRows()
}

// inboxCardKey answers the keys of the request under the cursor: a push's from its row, enter on
// another opens where it came from, and the rest decide it as its card's keys do.
func (m *app) inboxCardKey(msg tea.KeyPressMsg, c card) tea.Cmd {
	a := c.first()
	switch {
	case isGit(a):
		return m.pushRowKey(msg, a)
	case key.Matches(msg, keyOpen):
		return m.openCard(c)
	}
	if a.ChatPermission != nil && !c.group {
		if n := permissionAlias(msg, permissionPicks(a.ChatPermission.Options)); n > 0 {
			msg = pressOf(fmt.Sprint(n))
		}
	}
	return m.cardKey(msg, c)
}

// pushRowKey answers the keys of a push or pull request from its row: enter reviews it, y asks
// first and then approves it, F force-pushes, r asks for changes, n rejects and i ignores it.
func (m *app) pushRowKey(msg tea.KeyPressMsg, a api.ApprovalView) tea.Cmd {
	switch {
	case key.Matches(msg, keyOpen):
		return m.openReview(a)
	case key.Matches(msg, keyPush, keyForcePush, keyReject, keyIgnore, keyChanges) && !gitPending(a):
		m.setNotice(gitDecidedText(a), false)
	case key.Matches(msg, keyPush) && forced(a):
		m.setNotice("The push rewrites "+gitBranch(a)+": F force-pushes it.", false)
	case key.Matches(msg, keyPush):
		m.inbox.pushing = a.ID
	case key.Matches(msg, keyForcePush) && forced(a):
		return m.approvePush(a, defaultOpenPR(a))
	case key.Matches(msg, keyChanges) && a.Git.PullRequest == nil:
		m.askChanges(a)
	case key.Matches(msg, keyReject):
		return m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "deny", Scope: "sandbox"}, "Rejected", gitWhat(a))
	case key.Matches(msg, keyIgnore):
		return m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "dismiss", Scope: "sandbox"}, "Ignored", gitWhat(a))
	}
	return nil
}

// pushOwner names the confirmation of a push from its row, for the arrows.
func pushOwner(id string) string {
	return "push:" + id
}

// pushQuestion is what y on a push's row asks before it approves the push unread.
func pushQuestion(a api.ApprovalView) string {
	if pr := a.Git.PullRequest; pr != nil {
		return "Open the pull request " + pr.Head + " → " + pr.Base + " without opening the diff?"
	}
	commits := 0
	if a.Git.Review != nil {
		commits = len(a.Git.Review.Commits) + a.Git.Review.MoreCommits
	}
	return fmt.Sprintf("Push %d %s to %s without opening the diff?", commits, plural(commits, "commit", "commits"), gitBranch(a))
}

// pushAskKey answers the question y on a push's row asked: y approves the push, any other key
// keeps it.
func (m *app) pushAskKey(msg tea.KeyPressMsg) tea.Cmd {
	id := m.inbox.pushing
	if k, ok := m.confirmButtons(msg, pushOwner(id)); ok {
		if k == "" {
			return nil
		}
		msg = pressOf(k)
	}
	m.inbox.pushing = ""
	m.blurButtons()
	a, ok := m.approvalByID(id)
	if !ok {
		return nil
	}
	if key.Matches(msg, keyYes) {
		return m.approvePush(a, defaultOpenPR(a))
	}
	m.setNotice("Kept "+gitWhat(a)+". enter reviews it.", false)
	return nil
}

// approvalByID is the pending approval with the ID.
func (m *app) approvalByID(id string) (api.ApprovalView, bool) {
	for _, a := range m.approvals {
		if a.ID == id {
			return a, true
		}
	}
	return api.ApprovalView{}, false
}

// openCard opens where a request came from: an agent's permission its thread, a network request
// the latest thread of its teammate on the computer, or the computer. Other kinds print the page
// that decides them.
func (m *app) openCard(c card) tea.Cmd {
	a := c.first()
	switch {
	case a.ChatPermission != nil:
		for _, x := range m.inboxThreads() {
			if x.kind == sessionChat && x.chat.ID == a.ChatPermission.ChatID {
				return m.open(placeOf(x))
			}
		}
	case isNetwork(a):
		var latest *session
		for _, x := range m.inboxThreads() {
			if x.kind == sessionChat && x.sandbox == a.SandboxID && (latest == nil || sessionTime(x).After(sessionTime(*latest))) {
				latest = &x
			}
		}
		if latest != nil {
			return m.open(placeOf(*latest))
		}
		if sb, ok := m.sandboxByID(a.SandboxID); ok {
			return m.open(place{kind: placeComputer, sandbox: sb.ID})
		}
		return nil
	}
	return m.kindKey(enterKey, c)
}

// inboxOpen opens the row under the cursor: a thread, a computer, or a run's teammate.
func (m *app) inboxOpen(r inboxRow) tea.Cmd {
	switch {
	case r.thread != nil:
		return m.enterOf(*r.thread)
	case r.sb != "":
		return m.open(place{kind: placeComputer, sandbox: r.sb})
	case r.task != nil:
		return m.open(place{kind: placeMate, mate: r.task.TeammateID})
	}
	return nil
}

// leaveWaiting goes back to what a request interrupted without deciding it, the terminal it
// came from without the keys: esc stays in Studio.
func (m *app) leaveWaiting() tea.Cmd {
	r := m.resuming
	m.resuming = nil
	if r == nil {
		return nil
	}
	r.pane = false
	return m.returnTo(r)
}

// goWaiting shows Home with the cursor on the request's row, and returns to r once it is
// decided: the place, the screen and the terminal it came from.
func (m *app) goWaiting(id string, r *resumeState) {
	if r != nil {
		r.place, r.history, r.focus, r.screen = m.here, slices.Clone(m.history), m.focus, m.screen
	}
	m.open(place{})
	m.inboxOn(id)
	m.resuming = r
}

// inboxBindings are the keys the key bar shows on Home's list: the row's own answers first, then
// the ways about.
func (m *app) inboxBindings() []key.Binding {
	if m.inbox.pushing != "" {
		return []key.Binding{keyPushYes, keyNo}
	}
	var bindings []key.Binding
	r, ok := m.inboxRow()
	if ok {
		bindings = m.rowBindings(r)
	}
	if m.undoable() != nil {
		bindings = append(bindings, keyUndo)
	}
	bindings = append(bindings, keyNextRow)
	if ok && !r.lead && !slices.ContainsFunc(bindings, func(b key.Binding) bool { return b.Help().Key == "enter" }) {
		bindings = append(bindings, keyOpen)
	}
	return append(bindings, keyHelp, keyQuit, keyNewComputer, keyActivity, keySettings, keyEnv)
}

// rowBindings are a row's own keys.
func (m *app) rowBindings(r inboxRow) []key.Binding {
	switch {
	case r.card != nil:
		c := *r.card
		a := c.first()
		switch {
		case c.group:
			return []key.Binding{keyAllowAll, keyDenyAll, keySplit, keyScope}
		case isNetwork(a):
			return []key.Binding{keyAllow, keyDeny, keyOnce, keyPattern, keyScope}
		case isGit(a):
			approve := keyPush
			if a.Git.PullRequest != nil {
				approve = keyOpenPR
			} else if forced(a) {
				approve = keyForcePush
			}
			bindings := []key.Binding{approve}
			if a.Git.PullRequest == nil {
				bindings = append(bindings, keyChanges)
			}
			return append(bindings, keyReject, keyReview)
		case a.ChatPermission != nil:
			return []key.Binding{keyOnceY, keyAlways, keyDeny, keyGoThread}
		}
		var bindings []key.Binding
		for _, k := range kindBindings(a) {
			if k.Help().Desc != "back" {
				bindings = append(bindings, k)
			}
		}
		return bindings
	case r.thread != nil:
		return []key.Binding{keyGoThread}
	case r.lead:
		return []key.Binding{keyWriteDay, keyReadDn}
	case r.sb != "", r.task != nil:
		return []key.Binding{keyOpen}
	}
	return nil
}

// inboxGeometry is where Home's list and its reading pane go in a column of w cells and h rows:
// side by side from 90 cells, the pane under the list below, and no pane below 16 rows or
// without a row to read.
type inboxGeometry struct {
	side  bool
	listW int // side by side: the list's width
	listH int // stacked: the list's rows
	pane  rect
	shown bool // the reading pane is drawn
}

// rect is a part of the screen: its column and row, its width and height.
type rect struct {
	x, y, w, h int
}

// inboxLayout lays out Home's list for rows in a column of w cells and h rows at column x0.
func (m *app) inboxLayout(rows []inboxRow, w, h, x0 int) inboxGeometry {
	var g inboxGeometry
	if len(rows) == 0 {
		return g
	}
	if w >= 90 {
		g.side, g.shown = true, true
		g.listW = min(max(w*42/100, 46), 60)
		g.pane = rect{x0 + g.listW + 2, bodyTop, w - g.listW - 2, h}
		return g
	}
	if h < 16 {
		return g
	}
	g.listH = min(len(m.inboxLines(rows, -1, w)), h-7)
	g.shown = true
	g.pane = rect{x0, bodyTop + g.listH + 1, w, h - g.listH - 1}
	return g
}

// readingRect is where the reading pane is on screen, while Home's list shows one.
func (m *app) readingRect() (rect, bool) {
	if !m.inboxShows() || m.mainPane() != paneHomeList {
		return rect{}, false
	}
	main, _ := m.frameColumns()
	g := m.inboxLayout(m.inboxRows(), main.w, main.h, 0)
	g.pane.x += main.x
	return g.pane, g.shown
}

// inboxColumn is the column of the line between Home's list and its reading pane, when they
// are side by side.
func (m *app) inboxColumn() (int, bool) {
	if !m.inboxShows() || m.mainPane() != paneHomeList {
		return 0, false
	}
	main, _ := m.frameColumns()
	g := m.inboxLayout(m.inboxRows(), main.w, main.h, 0)
	return main.x + g.listW, g.side
}

// inboxBody is Home's list in its column of w cells and h rows, with the reading pane beside it
// or under it.
func (m *app) inboxBody(w, h int) []string {
	rows := m.inboxRows()
	at := m.inboxCursor(rows)
	g := m.inboxLayout(rows, w, h, 0)
	if !g.shown {
		return m.inboxList(rows, at, w, h, 0, true)
	}
	if g.side {
		left := pad(m.inboxList(rows, at, g.listW, h, 0, true), h)
		right := pad(m.reading(rows[at], g.pane), h)
		out := make([]string, h)
		for i := range out {
			out[i] = fitStyled(left[i], g.listW) + styleDim.Render("│") + " " + right[i]
		}
		return out
	}
	lines := pad(m.inboxList(rows, at, w, g.listH, 0, false), g.listH)[:g.listH]
	lines = append(lines, styleDim.Render(strings.Repeat("─", w)))
	return append(lines, m.reading(rows[at], g.pane)...)
}

// inboxLine is a line of Home's list: a row by its index, or a section's label or a blank.
type inboxLine struct {
	text string
	row  int // the row it draws, or -1
}

// inboxLines are the lines of Home's list in w cells: the sections' labels over their rows, a
// blank line between them; at is the cursor's row.
func (m *app) inboxLines(rows []inboxRow, at, w int) []inboxLine {
	var out []inboxLine
	if len(rows) == 0 || rows[0].section != inboxWaiting {
		mark := styleDim.Render("✓")
		if !m.look.plain {
			mark = m.pal.running.Render("✓")
		}
		out = append(out, inboxLine{" " + mark + " " + styleDim.Render("Nothing waits for you."), -1})
	}
	whoW := 4
	for _, r := range rows {
		whoW = max(whoW, min(lipgloss.Width(m.rowWho(r)), 8))
	}
	n := 0
	for _, r := range rows {
		if r.section == inboxWaiting {
			n++
		}
	}
	section := inboxSection(-1)
	for i, r := range rows {
		if r.section != section {
			section = r.section
			if len(out) > 0 {
				out = append(out, inboxLine{"", -1})
			}
			label := styleDim.Render(inboxLabels[section])
			if section == inboxWaiting {
				label += m.waitingMark(fmt.Sprintf(" · %d", n))
			}
			out = append(out, inboxLine{" " + label, -1})
		}
		out = append(out, inboxLine{m.rowLine(r, i == at, whoW, w), i})
	}
	return out
}

// inboxList is Home's list in w cells and at most h rows at column x0, the cursor's row in view.
// earlier adds today's decisions under it while rows remain.
func (m *app) inboxList(rows []inboxRow, at, w, h, x0 int, earlier bool) []string {
	all := m.inboxLines(rows, at, w)
	cursorLine := 0
	for i, l := range all {
		if l.row == at && at >= 0 {
			cursorLine = i
		}
	}
	offset := 0
	if cursorLine >= h {
		offset = cursorLine - h + 1
	}
	var lines []string
	for _, l := range all[offset:] {
		if len(lines) >= h {
			break
		}
		if l.row >= 0 {
			m.hitRow(len(lines), x0, x0+w, rowInbox, l.row)
		}
		lines = append(lines, l.text)
	}
	if earlier && len(lines)+3 <= h {
		lines = append(lines, "", " "+styleDim.Render("EARLIER TODAY"))
		for _, l := range m.earlier(w-1, h-len(lines)) {
			lines = append(lines, " "+l)
		}
	}
	return lines
}

// rowLine is a row of Home's list in w cells: the cursor, a glyph, who, what, and on the right
// where and how long ago.
func (m *app) rowLine(r inboxRow, selected bool, whoW, w int) string {
	cursor := " "
	if selected {
		style := styleAccent
		if !m.inboxKeys() {
			style = styleDim
		}
		mark := "▌"
		if m.look.plain {
			mark = "▸"
		}
		cursor = style.Render(mark)
	}
	glyph, what, where, when := m.rowCells(r)
	left := cursor + glyph + " " + styleBold.Render(fit(m.rowWho(r), whoW)) + "  "
	whereW := min(lipgloss.Width(where), 24, max(w/3, 8))
	age := "   "
	if !when.IsZero() {
		age = fmt.Sprintf("%3s", shortAge(m.now.Sub(when)))
	}
	tail := "  " + styleDim.Render(age) + " "
	room := w - lipgloss.Width(left) - whereW - lipgloss.Width(tail) - 2
	text := left + clip(what, max(room, 0))
	gap := w - lipgloss.Width(text) - whereW - lipgloss.Width(tail)
	return text + strings.Repeat(" ", max(gap, 1)) + styleDim.Render(clip(where, whereW)) + tail
}

// rowWho is who a row is about: the teammate, or a computer of yours.
func (m *app) rowWho(r inboxRow) string {
	switch {
	case r.card != nil:
		return m.cardWho(r.card.first())
	case r.thread != nil:
		if name := m.teammates[r.thread.mate]; name != "" {
			return name
		}
		if sb, ok := m.sandboxByID(r.thread.sandbox); ok {
			return sb.Name
		}
	case r.task != nil:
		return m.teammates[r.task.TeammateID]
	case r.lead:
		return m.inbox.lead.Settings.Name
	case r.sb != "":
		if sb, ok := m.sandboxByID(r.sb); ok {
			return sb.Name
		}
	}
	return ""
}

// cardWho is the teammate a request is from, or its computer when it has none.
func (m *app) cardWho(a api.ApprovalView) string {
	switch {
	case a.Teammate != nil && a.Teammate.Name != "":
		return a.Teammate.Name
	case a.Git != nil && a.Git.Push.TeammateName != "":
		return a.Git.Push.TeammateName
	case a.ChatPermission != nil && a.ChatPermission.TeammateName != "":
		return a.ChatPermission.TeammateName
	case a.MemoryShare != nil && a.MemoryShare.TeammateName != "":
		return a.MemoryShare.TeammateName
	case a.BrowserControl != nil && a.BrowserControl.TeammateName != "":
		return a.BrowserControl.TeammateName
	}
	if sb, ok := m.sandboxByID(a.SandboxID); ok {
		return firstNonEmpty(m.teammates[sb.TeammateID], sb.Name)
	}
	return firstNonEmpty(approvalSource(a), "—")
}

// rowCells are a row's glyph, what it is, where, and when it last changed.
func (m *app) rowCells(r inboxRow) (glyph, what, where string, when time.Time) {
	switch {
	case r.card != nil:
		c := *r.card
		a := c.first()
		glyph = strings.SplitN(kindChip(a), " ", 2)[0]
		if a.ChatPermission != nil {
			glyph = "?"
		}
		if m.look.plain {
			glyph = styleAccent.Render(glyph)
		} else {
			glyph = m.pal.hue(cardArea(a)).Render(glyph)
		}
		what, where = cardWhat(c), m.cardWhere(c)
		return glyph, what, where, a.CreatedAt
	case r.thread != nil:
		x := *r.thread
		st := m.stateOf(x)
		where = m.sandboxName(x.sandbox)
		switch {
		case x.kind == sessionChat && x.chat.State == store.ChatOrphaned:
			where = "ended"
		case st == stateDone:
			where = "done"
		}
		return m.sessionMark(x, st), x.title(), where, sessionTime(x)
	case r.task != nil:
		return styleDim.Render("◷"), oneLine(r.task.Title), runWhen(*r.task.NextRunAt, m.now), time.Time{}
	case r.lead:
		return m.leadCells()
	case r.sb != "":
		sb, _ := m.sandboxByID(r.sb)
		ph := phaseOf(sb)
		return m.phaseDot(ph), "your computer · " + strings.ToLower(ph.label()), sb.TemplateName, time.Time{}
	}
	return " ", "", "", time.Time{}
}

// sandboxName is a computer's name, or "" when it is gone.
func (m *app) sandboxName(id string) string {
	sb, _ := m.sandboxByID(id)
	return sb.Name
}

// cardWhat says what a request asks, on its row.
func cardWhat(c card) string {
	a := c.first()
	who := firstNonEmpty(approvalSource(a), "A computer")
	switch {
	case c.group:
		return fmt.Sprintf("%s wants %d hosts", firstNonEmpty(a.Network.Site, who), len(c.approvals))
	case isNetwork(a):
		return who + " wants " + hostOf(a)
	case isGit(a):
		return gitSummary(a)
	case a.ChatPermission != nil:
		return permissionWhat(a.ChatPermission.ToolKind, oneLine(a.ChatPermission.Title))
	}
	return oneLine(summary(a))
}

// permissionWhat says what an agent asks to do, by its tool's kind.
func permissionWhat(kind, title string) string {
	verb := map[string]string{"execute": "run", "edit": "edit", "delete": "delete", "move": "move", "read": "read", "fetch": "fetch", "search": "search"}[kind]
	if verb == "" {
		return "Asks: " + title
	}
	return "Asks to " + verb + " " + title
}

// cardWhere is where a request comes from or what it would allow, on its row: a group's hosts,
// a network request's pattern, a push's computer, a permission's thread; another environment's
// name first.
func (m *app) cardWhere(c card) string {
	a := c.first()
	if a.EnvironmentID != "" && a.EnvironmentID != m.env.ID {
		if name := m.envName(a.EnvironmentID); name != "" {
			return name
		}
	}
	switch {
	case c.group:
		return fmt.Sprintf("%s +%d", a.Network.Host, len(c.approvals)-1)
	case isNetwork(a):
		return patternText(a, *m.choiceOf(c))
	case a.ChatPermission != nil:
		return oneLine(a.ChatPermission.ChatTitle)
	}
	return approvalSource(a)
}

// runWhen says when a run comes, from now: "today 14:00", "tomorrow 09:00", "Mon 09:00",
// "Jan 2 09:00".
func runWhen(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local) }
	days := int(day(at).Sub(day(now)).Hours()/24 + 0.5)
	switch {
	case days <= 0:
		return "today " + at.Format("15:04")
	case days == 1:
		return "tomorrow " + at.Format("15:04")
	case days < 7:
		return at.Format("Mon 15:04")
	}
	return at.Format("Jan 2 15:04")
}

// has reports whether the cell at column x and row y is in the rect.
func (r rect) has(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}
