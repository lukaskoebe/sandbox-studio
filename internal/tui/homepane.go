package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// From paneWidth columns, Home is a sidebar and a pane (docs/design/tui-home-panes.md). The
// sidebar lists the sandboxes as groups, each with its sessions under it; the pane shows what the
// cursor is on: a sandbox's dashboard, an agent's or a terminal's screen as it runs, or a chat.
// Enter, a number or a click gives the pane the keys, and Ctrl-] gives them back.
const paneWidth = 100

// paneDelay is how long the cursor stays on a session before the pane connects to it, so that
// moving the cursor over the list doesn't open every terminal on the way.
const paneDelay = 150 * time.Millisecond

// paneState is what Home's pane shows and whether it has the keys.
type paneState struct {
	key   string    // the session on show, by its id; "" when the pane shows no session
	term  *liveTerm // the terminal on show, once connected
	err   error     // why the terminal could not be shown
	ended bool      // the sandbox ended the terminal
	dialing bool    // the terminal's connection is being made
	gen   int       // counts the sessions shown, so a message for an earlier one is dropped
	focus bool      // the pane has the keys
	zoom  bool      // the pane takes the sidebar's room too
}

// The keys of a pane that has the keys.
var (
	keySidebar = key.NewBinding(key.WithKeys("ctrl+]"), key.WithHelp("ctrl+]", "sidebar"))
	keySwitch  = key.NewBinding(key.WithKeys("alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9"), key.WithHelp("alt+1-9", "switch"))
	keyZoom    = key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "full screen"))
	keyFocus   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "type in"))
)

// homeSplit reports whether Home shows the sidebar and the pane.
func (m *app) homeSplit(w int) bool {
	return w >= paneWidth && m.env.ID != "" && m.loaded && len(m.visible()) > 0
}

// sidebarWidth is the width of Home's sidebar at w columns; the pane gets the rest.
func sidebarWidth(w int) int {
	return min(max(w/3, 32), 44)
}

// paneX is the column Home's pane starts at, and paneW its width.
func (m *app) paneX() int {
	if m.pane.zoom {
		return 0
	}
	return sidebarWidth(m.width) + 2
}

func (m *app) paneW() int {
	return max(m.width-m.paneX(), 1)
}

// bodyRows is how many rows the body gets.
func (m *app) bodyRows() int {
	return max(m.height-5, 1)
}

// termRows is how many rows a terminal gets in the pane: the body less the pane's title and rule.
func (m *app) termRows() int {
	return max(m.bodyRows()-2, 1)
}

// paneShows reports whether Home's pane is on screen.
func (m *app) paneShows() bool {
	return m.screen == screenHome && m.homeSplit(m.width)
}

// paneTarget is the session the pane shows: an agent, a terminal or a chat under the cursor.
func (m *app) paneTarget() (session, bool) {
	if !m.paneShows() {
		return session{}, false
	}
	x, ok := m.selectedSession()
	if !ok || x.kind == sessionOlder {
		return session{}, false
	}
	return x, true
}

// dashShows reports whether the pane shows the dashboard of the sandbox under the cursor.
func (m *app) dashShows() bool {
	if !m.paneShows() || m.selected() == nil {
		return false
	}
	_, ok := m.selectedSession()
	return !ok
}

// onShow reports whether the pane shows the session with the id.
func (m *app) onShow(id string) bool {
	return m.paneShows() && m.pane.key == id
}

// zoomed reports whether the pane is in full screen.
func (m *app) zoomed() bool {
	return m.pane.zoom && m.pane.key != ""
}

// paneFocused reports whether the pane has the keys.
func (m *app) paneFocused() bool {
	return m.pane.focus && m.pane.key != "" && m.paneShows()
}

// watchPane follows the cursor: when it moves to another session, the pane lets the last one go
// and connects to the new one after paneDelay. The pane's terminal follows the pane's size.
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
			p.focus, p.zoom = false, false
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

// closePane lets the session on show go: its terminal is closed, its chat's stream ended.
func (m *app) closePane() {
	p := &m.pane
	if p.term != nil {
		p.term.close()
	}
	p.term, p.err, p.ended, p.dialing = nil, nil, false, false
	if m.chats.inPane {
		m.stopChat()
		m.chats = chatState{fold: -1, gen: m.chats.gen} // the count goes on, so the last chat's messages are dropped
	}
}

// openPane connects the pane to the session on show: a terminal over the attach websocket, a
// chat by its log and stream, as the chat screen reads it.
func (m *app) openPane() tea.Cmd {
	p := &m.pane
	x, ok := m.paneTarget()
	if !ok || x.id() != p.key || p.term != nil || p.dialing || (m.chats.inPane && x.kind == sessionChat) {
		return nil
	}
	sb := m.selected()
	if m.client == nil {
		return nil // a test's model, which has no Studio to ask
	}
	if x.kind == sessionChat {
		m.chats = chatState{sandbox: *sb, back: screenHome, fold: -1, inPane: true, gen: m.chats.gen}
		return m.openChat(x.chat.ID)
	}
	if phaseOf(*sb) != phaseReady {
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
				msg.term.close() // the cursor moved on while it connected
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

// focusPane gives the pane the keys, connecting it at once if the wait isn't over, or again if
// its terminal ended. A chat that takes a prompt opens its composer.
func (m *app) focusPane() tea.Cmd {
	p := &m.pane
	x, ok := m.paneTarget()
	if !ok {
		return nil
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
	cmd := m.openPane()
	if x.kind == sessionChat && x.chat.State == store.ChatIdle && m.stateOf(x) != stateBlocked {
		m.chats.composing = true
	}
	return cmd
}

// paneKey routes a key to the pane that has it: Ctrl-] gives the keys back to the sidebar,
// alt+1 to alt+9 switch to that session, alt+z puts the pane in full screen, and the rest go to
// the terminal or the chat on show.
func (m *app) paneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.pane
	switch {
	case key.Matches(msg, keySidebar) || msg.Code == detachKey:
		p.focus = false
		if m.chats.inPane {
			m.chats.composing = false
		}
		return m, nil
	case key.Matches(msg, keySwitch):
		n := int(msg.Code - '0')
		for _, r := range m.homeRows() {
			if r.num == n {
				m.cursor, m.sub = r.sb, r.sub
				return m, m.focusPane()
			}
		}
		return m, nil
	case key.Matches(msg, keyZoom):
		p.zoom = !p.zoom
		return m, nil
	}
	if m.chats.inPane {
		c := &m.chats
		if !c.composing && key.Matches(msg, keyBack) {
			p.focus = false
			return m, nil
		}
		if !c.composing && key.Matches(msg, keyNewChat) {
			return m, m.newChat(m.selected())
		}
		if c.composing && key.Matches(msg, keyEsc) {
			c.composing = false
			return m, nil
		}
		return m.chatKey(msg)
	}
	if p.term != nil && !p.ended {
		p.term.key(msg)
	}
	return m, nil
}

// paneBindings are the keys the key bar shows while the pane has them.
func (m *app) paneBindings() []key.Binding {
	if m.chats.inPane {
		return append(m.chatBindings(), keySidebar, keySwitch, keyZoom)
	}
	return []key.Binding{keySidebar, keySwitch, keyZoom}
}

// paneBody is Home's pane in w cells and h rows: the dashboard of the sandbox under the cursor,
// or the session under it.
func (m *app) paneBody(w, h int) []string {
	sb := m.selected()
	if sb == nil {
		return nil
	}
	x, ok := m.selectedSession()
	switch {
	case !ok:
		return m.dashboard(*sb, w, h)
	case x.kind == sessionOlder:
		return []string{"", styleDim.Render(clip(fmt.Sprintf("%d older chats. Enter lists them.", x.older), w))}
	case x.kind == sessionChat:
		if !m.chats.inPane {
			return []string{m.paneTitle(x, *sb, w), styleDim.Render("Loading…")}
		}
		first := len(m.mouse.rows)
		lines := m.chatBody(w, h)
		for i := first; i < len(m.mouse.rows); i++ {
			m.mouse.rows[i].x0 += m.paneX()
			m.mouse.rows[i].x1 += m.paneX()
		}
		return lines
	}
	lines := []string{m.paneTitle(x, *sb, w), m.paneRule(w)}
	p := &m.pane
	var screen []string
	switch {
	case phaseOf(*sb) != phaseReady:
		screen = []string{styleDim.Render("Start " + sb.Name + " first, with s.")}
	case p.err != nil:
		screen = []string{styleFailed.Render(clip("Could not open the terminal: "+p.err.Error(), w)), styleDim.Render("Enter tries again.")}
	case p.term == nil:
		screen = []string{styleDim.Render("Connecting…")}
	default:
		screen = p.term.lines(w, h-2)
		if p.ended {
			screen = append(pad(screen, max(h-3, 0))[:max(h-3, 0)], styleDim.Render("The terminal ended. Enter opens it again."))
		}
	}
	screen = pad(screen, h-2)
	if banner := m.paneBanner(x, w); banner != nil && len(banner) < len(screen) {
		copy(screen, banner)
	}
	return append(lines, screen...)
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

// paneCard is the approval that blocks an agent in the pane: the first of its sandbox's that
// isn't a chat's permission request.
func (m *app) paneCard(x session) (card, bool) {
	if x.kind != sessionAgent || m.stateOf(x) != stateBlocked {
		return card{}, false
	}
	for _, c := range m.cards() {
		if c.first().SandboxID == x.sandbox && c.first().ChatPermission == nil {
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

// paneBanner is the approval that blocks the agent on show, drawn over the top of its screen in
// w cells, with the keys that decide it from the sidebar; nil when none blocks it.
func (m *app) paneBanner(x session, w int) []string {
	c, ok := m.paneCard(x)
	if !ok || w < 24 {
		return nil
	}
	a := c.first()
	what := kindTitle(a.Kind) + ": " + summary(a)
	if isNetwork(a) {
		what = firstNonEmpty(approvalSource(a), "The sandbox") + " wants " + hostOf(a)
	}
	n := len(c.approvals)
	if more := m.pendingFor(x.sandbox) - n; more > 0 {
		what += fmt.Sprintf(" · %d more", more)
	}
	keys := "a approvals"
	switch {
	case m.paneFocused():
		keys = "Ctrl-] then y allow · o once · n deny · a approvals"
		if !bannerKeys(c) {
			keys = "Ctrl-] then a approvals"
		}
	case bannerKeys(c):
		keys = "y allow · o once · n deny · a approvals"
	}
	style := styleAccent
	if !m.look.plain {
		style = m.pal.waiting
	}
	inner := w - 4
	edge := func(l, r string, label string) string {
		return style.Render(l + label + strings.Repeat("─", max(inner+2-lipgloss.Width(label), 0)) + r)
	}
	row := func(s string) string {
		return style.Render("│") + " " + fitStyled(s, inner) + " " + style.Render("│")
	}
	return []string{
		edge("┌", "┐", " Studio "),
		row(style.Render("▲") + " " + clip(what, inner-2)),
		row(styleDim.Render(clip(keys, inner))),
		edge("└", "┘", ""),
	}
}

// bannerKey answers y, o and n from the sidebar for the banner over the agent under the cursor.
// ok is false when no banner shows, or the key isn't one of its.
func (m *app) bannerKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	x, shown := m.paneTarget()
	if !shown {
		return nil, false
	}
	c, blocked := m.paneCard(x)
	if !blocked || !bannerKeys(c) {
		return nil, false
	}
	a := c.first()
	ch := m.choiceOf(c)
	switch {
	case key.Matches(msg, keyAllow):
		return m.decide(c, decisionFor(a, *ch, "allow"), "Allowed", ""), true
	case key.Matches(msg, keyOnce):
		return m.decide(c, decisionFor(a, *ch, "allow_once"), "Allowed", hostOf(a)+" once"), true
	case key.Matches(msg, keyDeny):
		return m.decide(c, decisionFor(a, *ch, "deny"), "Denied", ""), true
	}
	return nil, false
}

// paneCursor is where the terminal's cursor is on the screen while the pane has the keys, or nil.
func (m *app) paneCursor() *tea.Cursor {
	p := &m.pane
	if !m.paneFocused() || p.term == nil || p.ended || m.chats.inPane {
		return nil
	}
	x, y, shown := p.term.cursor()
	if !shown || x >= m.paneW() || y >= m.termRows() {
		return nil
	}
	return tea.NewCursor(m.paneX()+x, bodyTop+2+y)
}

// splitBody is Home's wide layout: the sidebar, a line, and the pane; the line is the accent's
// while the pane has the keys. In full screen the pane has the whole width.
func (m *app) splitBody(visible []sandboxes.View, w, h int) []string {
	if m.zoomed() {
		return m.paneBody(w, h)
	}
	listW := sidebarWidth(w)
	left := m.sidebar(visible, listW-1, h) // a cell of room before the line
	right := m.paneBody(w-listW-2, h)
	sep := styleDim.Render("│")
	if m.paneFocused() {
		sep = styleAccent.Render("┃")
	}
	left, right = pad(left, h), pad(right, h)
	out := make([]string, h)
	for i := range out {
		out[i] = fitStyled(left[i], listW) + sep + " " + right[i]
	}
	return out
}
