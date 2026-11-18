package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The pane is the one live session the frame shows: the terminal of a thread that is an agent in
// a terminal, the Terminal tab's shell, or, on Home, the thread under the cursor in the reading
// pane. Enter or a click gives a terminal the keys, and Ctrl-] gives them back to Studio.

// paneWidth is the width from which the sidebar shows beside the place on show.
const paneWidth = 100

// paneDelay is how long the cursor stays on a session before the pane connects to it, so that
// moving the cursor over the list doesn't open every terminal on the way.
const paneDelay = 150 * time.Millisecond

// paneState is what the pane shows and whether it has the keys.
type paneState struct {
	key     string    // the session on show, by its id; "" when the pane shows no session
	term    *liveTerm // the terminal on show, once connected
	err     error     // why the terminal could not be shown
	ended   bool      // the sandbox ended the terminal
	dialing bool      // the terminal's connection is being made
	gen     int       // counts the sessions shown, so a message for an earlier one is dropped
	focus   bool      // the pane has the keys
	answer  bool      // the pane gave the keys to its banner, and gets them back after
}

// The keys of a pane that has the keys.
var (
	keySidebar    = key.NewBinding(key.WithKeys("ctrl+]"), key.WithHelp("ctrl+]", "keys back"))
	keySwitch     = key.NewBinding(key.WithKeys("alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9"), key.WithHelp("alt+1-9", "session"))
	keyZoom       = key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "full screen"))
	keyFocus      = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "type in"))
	keyAnswerIt   = key.NewBinding(key.WithKeys("alt+a"), key.WithHelp("alt+a", "answer the banner"))
	keyMore       = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "more"))
	keyReviewIt   = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "review it"))
	keyBannerBack = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
)

// sidebarWidth is the width of the sidebar, and of the Computers screen's list.
func sidebarWidth(int) int {
	return sidebarW
}

// bodyRows is how many rows the body gets.
func (m *app) bodyRows() int {
	return max(m.height-5, 1)
}

// termSession is the terminal of a computer by its name, as the last read lists it, or as it
// will be once the attach starts it.
func (m *app) termSession(sandbox, name string) session {
	if sb, ok := m.sandboxByID(sandbox); ok {
		for _, x := range m.sessionsOf(sb) {
			if x.term.Name == name {
				return x
			}
		}
	}
	t := sandboxes.Terminal{}
	t.Name = name
	return session{kind: sessionTerminal, sandbox: sandbox, term: t}
}

// terminalTabShows reports whether the computer pane shows its Terminal tab.
func (m *app) terminalTabShows() bool {
	if m.screen != screenMain || m.comp.tab != tabTerminal || m.computerOn() == nil {
		return false
	}
	if !slices.Contains(m.computerTabs(), tabTerminal) {
		return false
	}
	return m.mainPane() == paneComputer || m.sidePane() == paneComputer
}

// threadTermShows reports whether the main column shows a thread that is an agent in a terminal.
func (m *app) threadTermShows() bool {
	return m.screen == screenMain && m.here.kind == placeThread && m.here.term != "" && m.mainPane() == paneThread
}

// paneTarget is the session the pane shows: the reading pane's thread on Home, the terminal of
// a thread that is one, or the Terminal tab's.
func (m *app) paneTarget() (session, bool) {
	switch {
	case m.screen != screenMain:
		return session{}, false
	case m.here.kind == placeHome:
		if _, ok := m.readingRect(); !ok {
			return session{}, false
		}
		if r, ok := m.inboxRow(); ok && r.thread != nil && r.thread.kind != sessionOlder {
			return *r.thread, true
		}
		return session{}, false
	case m.threadTermShows():
		return m.termSession(m.here.sandbox, m.here.term), true
	case m.terminalTabShows():
		return m.termSession(m.comp.sandbox, m.comp.term), true
	}
	return session{}, false
}

// paneRect is where the pane's session is drawn on screen, ok false when none is.
func (m *app) paneRect() (rect, bool) {
	if _, ok := m.paneTarget(); !ok {
		return rect{}, false
	}
	switch {
	case m.here.kind == placeHome:
		return m.readingRect()
	case m.shellZoomed():
		return rect{0, 0, m.width, max(m.height-1, 1)}, true
	case m.threadTermShows():
		main, _ := m.frameColumns()
		return main, true
	}
	return m.paneRectOf(paneComputer)
}

// termRect is where the terminal's own screen is: the pane less the lines above it, the title
// and rule on Home, the chips and rule of a thread, and the tab row, its rule and the chips on
// the Terminal tab.
func (m *app) termRect() rect {
	rc, ok := m.paneRect()
	if !ok {
		return rect{0, bodyTop, max(m.width, 1), m.bodyRows()}
	}
	top := 3
	switch {
	case m.shellZoomed():
		top = 0
	case m.here.kind == placeHome, m.threadTermShows():
		top = 2
	}
	return rect{rc.x, rc.y + top, max(rc.w, 1), max(rc.h-top, 1)}
}

// paneW is the width of the pane's terminal.
func (m *app) paneW() int {
	return m.termRect().w
}

// termRows is how many rows the pane's terminal gets.
func (m *app) termRows() int {
	return m.termRect().h
}

// paneOn reports whether a session of the pane is on screen.
func (m *app) paneOn() bool {
	_, ok := m.paneTarget()
	return ok
}

// dashShows reports whether a computer's dashboard or detail is on show, whose numbers the
// watchers read.
func (m *app) dashShows() bool {
	return m.dashSandbox() != nil
}

// dashSandbox is the computer whose dashboard or detail is on show: the reading pane's, the
// Details tab's, or the Computers screen's.
func (m *app) dashSandbox() *sandboxes.View {
	find := func(id string) *sandboxes.View {
		for i := range m.sandboxes {
			if m.sandboxes[i].ID == id {
				return &m.sandboxes[i]
			}
		}
		return nil
	}
	switch {
	case m.screen == screenComputers:
		if m.computersSplit(m.mainW()) {
			return m.computerAt()
		}
		return nil
	case m.screen != screenMain:
		return nil
	case m.here.kind == placeHome:
		if _, shown := m.readingRect(); !shown {
			return nil
		}
		if r, ok := m.inboxRow(); ok && r.sb != "" {
			return find(r.sb)
		}
		return nil
	case m.comp.tab == tabDetails && (m.mainPane() == paneComputer || m.sidePane() == paneComputer):
		return m.computerOn()
	}
	return nil
}

// dashID is the ID of dashSandbox, or "".
func (m *app) dashID() string {
	if sb := m.dashSandbox(); sb != nil {
		return sb.ID
	}
	return ""
}

// onShow reports whether the pane shows the session with the id.
func (m *app) onShow(id string) bool {
	return m.paneOn() && m.pane.key == id
}

// paneFocused reports whether the pane has the keys.
func (m *app) paneFocused() bool {
	return m.pane.focus && m.pane.key != "" && m.paneOn()
}

// watchPane follows what the frame shows: when it is another session, the pane lets the last one
// go and connects to the new one after paneDelay. The pane's terminal follows its size.
func (m *app) watchPane() tea.Cmd {
	p := &m.pane
	want := ""
	if x, ok := m.paneTarget(); ok {
		want = x.id()
	}
	if want != p.key {
		m.closePane()
		p.key = want
		p.gen++
		if want == "" {
			p.focus = false
			return nil
		}
		gen := p.gen
		return tea.Tick(paneDelay, func(time.Time) tea.Msg { return paneDue{gen} })
	}
	if p.term != nil {
		p.term.resize(m.paneW(), m.termRows())
	}
	return nil
}

// closePane lets the session on show go: its terminal is closed, and the chat Home's reading pane
// had open is ended.
func (m *app) closePane() {
	p := &m.pane
	if p.term != nil {
		p.term.close()
	}
	p.term, p.err, p.ended, p.dialing = nil, nil, false, false
	if m.chats.reading {
		m.stopChat()
		m.chats = chatState{fold: -1, gen: m.chats.gen} // the count goes on, so the last chat's messages are dropped
	}
}

// openPane connects the pane to the session on show: a terminal over the attach websocket, or,
// in Home's reading pane, a chat by its log and stream.
func (m *app) openPane() tea.Cmd {
	p := &m.pane
	x, ok := m.paneTarget()
	if !ok || x.id() != p.key || p.term != nil || p.dialing || (m.chats.reading && x.kind == sessionChat) {
		return nil
	}
	sb, found := m.sandboxByID(x.sandbox)
	if x.kind == sessionChat {
		sb, found = m.sandboxByID(x.chat.SandboxID)
	}
	if m.client == nil || !found {
		return nil // a test's model, which has no Studio to ask
	}
	if x.kind == sessionChat {
		if m.chats.on && m.chats.id == x.chat.ID {
			return nil // the thread on show has it open
		}
		m.stopChat()
		m.chats = chatState{sandbox: sb, fold: -1, on: true, reading: true, gen: m.chats.gen}
		return m.openChat(x.chat.ID)
	}
	if phaseOf(sb) != phaseReady {
		return nil
	}
	p.dialing = true
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, p.gen
	cols, rows, light := m.paneW(), m.termRows(), m.look.light
	return func() tea.Msg {
		term, err := openTerm(ctx, client, env, x.sandbox, x.term.Name, cols, rows, light)
		return paneOpened{gen: gen, term: term, err: err}
	}
}

// paneUpdate applies the pane's messages. ok is false for other messages.
func (m *app) paneUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	p := &m.pane
	switch msg := msg.(type) {
	case paneDue:
		if msg.gen != p.gen {
			return nil, true
		}
		return m.openPane(), true
	case paneOpened:
		if msg.gen != p.gen {
			if msg.term != nil {
				msg.term.close() // the frame moved on while it connected
			}
			return nil, true
		}
		p.dialing = false
		if msg.err != nil {
			p.err = msg.err
			return nil, true
		}
		p.term = msg.term
		return p.term.waitOutput(p.gen), true
	case paneOutput:
		if msg.gen != p.gen || p.term == nil {
			return nil, true
		}
		return p.term.waitOutput(p.gen), true
	case paneEnded:
		if msg.gen == p.gen {
			p.ended, p.focus = true, false
		}
		return nil, true
	}
	return nil, false
}

// focusPane gives the pane's terminal the keys, connecting it at once if the wait isn't over, or
// again if it ended. A thread in Home's reading pane opens instead, which is where it is typed to.
func (m *app) focusPane() tea.Cmd {
	p := &m.pane
	x, ok := m.paneTarget()
	if !ok {
		return nil
	}
	if x.kind == sessionChat || m.here.kind == placeHome {
		return m.open(placeOf(x))
	}
	if p.key != x.id() {
		m.watchPane()
	}
	if p.ended || p.err != nil {
		m.closePane()
		p.gen++
	}
	delete(m.sess.done, x.id()) // seen now
	p.focus = true
	return m.openPane()
}

// paneKey routes a key to the terminal that has it: Ctrl-] gives the keys back to Studio,
// alt+1 to alt+9 show another of the computer's terminals, alt+z puts it in full screen, alt+a
// answers the banner over it, and the rest go to the terminal.
func (m *app) paneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.pane
	switch {
	case key.Matches(msg, keySidebar) || msg.Code == detachKey:
		p.focus = false
		return m, nil
	case key.Matches(msg, keySwitch):
		return m, m.pickChip(int(msg.Code - '0'))
	case key.Matches(msg, keyZoom):
		m.zoom = !m.zoom
		return m, nil
	case key.Matches(msg, keyAnswerIt):
		m.answerBanner()
		return m, nil
	}
	if p.term != nil && !p.ended {
		p.term.key(msg)
	}
	return m, nil
}

// paneBindings are the keys the key bar shows while a terminal has them: alt+a first while a
// banner waits.
func (m *app) paneBindings() []key.Binding {
	var bindings []key.Binding
	if x, ok := m.paneTarget(); ok {
		if _, waiting := m.paneCard(x); waiting {
			bindings = append(bindings, keyAnswerIt)
		}
	}
	return append(bindings, keySidebar, keySwitch, keyZoom)
}

// sessionPane is a thread or a terminal in Home's reading pane, in the part rc of its column: the
// chat as it streams, or the terminal's screen under its title.
func (m *app) sessionPane(x session, sb sandboxes.View, rc rect) []string {
	w, h := rc.w, rc.h
	if x.kind == sessionChat {
		if !m.chats.reading || m.chats.id != x.chat.ID {
			return []string{m.paneTitle(x, sb, w), styleDim.Render("Loading…")}
		}
		rows, keys := len(m.mouse.rows), len(m.mouse.keys)
		lines := m.chatBody(w, h)
		m.shiftHits(rows, keys, rc.x, rc.y-bodyTop)
		return lines
	}
	lines := []string{m.paneTitle(x, sb, w), m.paneRule(w)}
	return append(lines, m.termScreen(x, sb, w, h-2)...)
}

// shiftHits moves the mouse's rows and keys recorded since rows and keys by dx cells and dy rows.
func (m *app) shiftHits(rows, keys, dx, dy int) {
	for i := rows; i < len(m.mouse.rows); i++ {
		r := &m.mouse.rows[i]
		r.x0, r.x1, r.y = r.x0+dx, r.x1+dx, r.y+dy
	}
	for i := keys; i < len(m.mouse.keys); i++ {
		k := &m.mouse.keys[i]
		k.x0, k.x1, k.y = k.x0+dx, k.x1+dx, k.y+dy
	}
}

// termScreen is the screen of the terminal x of sb in w cells and h rows, as it runs, with the
// approval waiting in its computer over its top.
func (m *app) termScreen(x session, sb sandboxes.View, w, h int) []string {
	p := &m.pane
	var screen []string
	switch {
	case phaseOf(sb) != phaseReady:
		screen = []string{styleDim.Render(clip("Start "+sb.Name+" first: Details, then s.", w))}
	case p.err != nil && p.key == x.id():
		screen = []string{styleFailed.Render(clip("Could not open the terminal: "+p.err.Error(), w)), styleDim.Render("Enter tries again.")}
	case p.term == nil || p.key != x.id():
		screen = []string{styleDim.Render("Connecting…")}
	default:
		screen = p.term.lines(w, h)
		if p.ended {
			screen = append(pad(screen, max(h-1, 0))[:max(h-1, 0)], styleDim.Render(clip("The terminal ended. Enter opens it again.", w)))
		}
	}
	screen = pad(screen, h)
	if banner := m.paneBanner(x, w); banner != nil && len(banner) < len(screen) {
		copy(screen, banner)
	}
	return screen
}

// paneTitle is the pane's first line: the session and its sandbox, and its state.
func (m *app) paneTitle(x session, sb sandboxes.View, w int) string {
	st := m.stateOf(x)
	return spread(styleBold.Render(clip(x.title(), w/2))+styleDim.Render(" · "+sb.Name), m.stateMark(st)+" "+styleDim.Render(m.sessionWords(x, st)), w)
}

// paneRule is the rule under the pane's title: the accent's while the pane has the keys.
func (m *app) paneRule(w int) string {
	if m.paneFocused() {
		return styleAccent.Render(strings.Repeat("━", w))
	}
	return styleDim.Render(strings.Repeat("─", w))
}

// paneCard is the approval waiting in the sandbox of the session in the pane: the first of its
// sandbox's that isn't a chat's permission request, which the chat shows itself. Approvals don't
// say which process asked, so every session of the sandbox shows it.
func (m *app) paneCard(x session) (card, bool) {
	if x.kind == sessionOlder {
		return card{}, false
	}
	return m.sandboxCard(x.sandbox)
}

// sandboxCard is the first card waiting in a sandbox that isn't a chat's permission request.
func (m *app) sandboxCard(id string) (card, bool) {
	for _, c := range m.cards() {
		if c.first().SandboxID == id && c.first().ChatPermission == nil {
			return c, true
		}
	}
	return card{}, false
}

// bannerKeys reports whether y, o and n decide the card from the sidebar: a single network
// request's do.
func bannerKeys(c card) bool {
	return !c.group && isNetwork(c.first())
}

// bannerOwner names a banner's row of choices for the arrows.
func bannerOwner(c card) string {
	return "banner:" + c.first().ID
}

// bannerCaps are a banner's choices: allow once, allow and deny for a network request, and
// Home's list, on the request, for more or for any other kind.
func bannerCaps(c card) []choiceCap {
	if !bannerKeys(c) {
		return []choiceCap{{"a", "Review it"}}
	}
	return []choiceCap{{"o", "Allow once"}, {"y", "Allow"}, {"n", "Deny"}, {"a", "More…"}}
}

// paneBanner is the approval waiting in the sandbox on show, drawn over the top of its session
// in w cells, with its choices; nil when none waits. While the pane has the keys, they go to
// the session, and the banner says how to answer instead.
func (m *app) paneBanner(x session, w int) []string {
	c, ok := m.paneCard(x)
	if !ok || w < 24 || m.here.kind == placeHome { // Home's list shows the request itself
		return nil
	}
	return m.banner(c, x.sandbox, m.paneFocused(), w)
}

// banner draws card c as a banner of w cells over a session of the sandbox. typing is whether
// the session has the keys.
func (m *app) banner(c card, sandbox string, typing bool, w int) []string {
	a := c.first()
	what := kindTitle(a.Kind) + ": " + summary(a)
	if isNetwork(a) {
		what = firstNonEmpty(approvalSource(a), "The computer") + " wants " + hostOf(a)
	}
	n := len(c.approvals)
	if more := m.pendingFor(sandbox) - n; more > 0 {
		what += fmt.Sprintf(" · %d more", more)
	}
	style := styleAccent
	if !m.look.plain {
		style = m.pal.waiting
	}
	inner := w - 4
	choices := styleDim.Render("alt+a answers it")
	choices += styleDim.Render(" · ctrl+] gives the keys back")
	if !typing {
		choices, _ = m.buttonsLine(bannerCaps(c), m.buttonAt(bannerOwner(c)))
		choices += styleDim.Render("   ←/→ space")
	}
	edge := func(l, r string, label string) string {
		return style.Render(l + label + strings.Repeat("─", max(inner+2-lipgloss.Width(label), 0)) + r)
	}
	row := func(s string) string {
		return style.Render("│") + " " + fitStyled(s, inner) + " " + style.Render("│")
	}
	return []string{
		edge("┌", "┐", " Studio "),
		row(style.Render("▲") + " " + clip(what, inner-2)),
		row(choices),
		edge("└", "┘", ""),
	}
}

// answerBanner gives the banner over the pane's session the keys, from the pane: the arrows are
// on its first choice, and a decision gives the keys back to the pane.
func (m *app) answerBanner() bool {
	x, ok := m.paneTarget()
	if !ok {
		return false
	}
	c, waiting := m.paneCard(x)
	if !waiting {
		m.setNotice("Nothing waits in "+m.sandboxName(x.sandbox)+".", false)
		return false
	}
	m.pane.focus, m.pane.answer = false, true
	m.focusButton(bannerOwner(c), 0)
	return true
}

// bannerKey answers the banner over the session under the cursor, from the sidebar: y, o and n
// decide a network request, a reviews it on Home's list, and the arrows, space and
// enter press its choices. esc lets go of them, back to the pane when it gave the keys. ok is
// false when no banner shows, or the key isn't one of its.
func (m *app) bannerKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	x, shown := m.paneTarget()
	if !shown {
		return nil, false
	}
	c, waiting := m.paneCard(x)
	if !waiting {
		return nil, false
	}
	owner := bannerOwner(c)
	if k, ok := m.buttonKey(msg, owner, bannerCaps(c), 0); ok {
		if k == "" {
			return nil, true
		}
		msg = pressOf(k)
	}
	p := &m.pane
	back := func() { // after a decision, or esc: the keys go back where they were
		m.blurButtons()
		if p.answer {
			p.focus, p.answer = true, false
		}
	}
	a := c.first()
	ch := m.choiceOf(c)
	switch {
	case key.Matches(msg, keyEsc) && (m.buttonAt(owner) >= 0 || p.answer):
		back()
		return nil, true
	case key.Matches(msg, keyMore):
		r := &resumeState{approval: a.ID, pane: p.answer}
		m.blurButtons()
		p.answer = false
		m.goWaiting(a.ID, r)
		return nil, true
	case !bannerKeys(c):
		return nil, false
	case key.Matches(msg, keyAllow):
		back()
		return m.decide(c, decisionFor(a, *ch, "allow"), "Allowed", ""), true
	case key.Matches(msg, keyOnce):
		back()
		return m.decide(c, decisionFor(a, *ch, "allow_once"), "Allowed", hostOf(a)+" once"), true
	case key.Matches(msg, keyDeny):
		back()
		return m.decide(c, decisionFor(a, *ch, "deny"), "Denied", ""), true
	}
	return nil, false
}

// bannerBindings are the keys of the banner while the arrows are on it, or nil.
func (m *app) bannerBindings() []key.Binding {
	x, ok := m.paneTarget()
	if !ok {
		return nil
	}
	c, waiting := m.paneCard(x)
	if !waiting || (m.buttonAt(bannerOwner(c)) < 0 && !m.pane.answer) {
		return nil
	}
	if !bannerKeys(c) {
		return []key.Binding{keyReviewIt, keyLeft, keyPress, keyBannerBack}
	}
	return []key.Binding{keyOnce, keyAllow, keyDeny, keyMore, keyLeft, keyPress, keyBannerBack}
}

// paneCursor is where the terminal's cursor is on the screen while the pane has the keys, or nil.
func (m *app) paneCursor() *tea.Cursor {
	p := &m.pane
	if !m.paneFocused() || p.term == nil || p.ended {
		return nil
	}
	x, y, shown := p.term.cursor()
	rc := m.termRect()
	if !shown || x >= rc.w || y >= rc.h {
		return nil
	}
	return tea.NewCursor(rc.x+x, rc.y+y)
}
