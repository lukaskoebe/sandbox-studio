package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The frame is the web's page in a terminal: a crumb row that says where you are, the sidebar
// from paneWidth columns, and the place's columns beside it, a thread and its computer side by
// side from splitWidth (docs/tui.md). Below paneWidth the sidebar is a drawer that \ slides over
// the body; below splitWidth tab flips one column between the thread and its computer.

// layout is the widths of the frame's columns; 0 means the column is not drawn. The columns
// are split by one cell, so sidebar + main + side and the splits make the width.
type layout struct{ sidebar, main, side int }

// sidebarW is the width of the sidebar.
const sidebarW = 30

// layoutFor is the widths of the frame's columns at w cells, the only place that decides them.
// It depends on the width and the place on show, never on the look.
func (m *app) layoutFor(w int) layout {
	if m.zoom && m.screen == screenMain {
		return layout{0, w, 0}
	}
	if w < paneWidth {
		return layout{0, w, 0}
	}
	l := layout{sidebar: sidebarW, main: w - sidebarW - 1}
	if w >= splitWidth && m.screen == screenMain && m.hasSide() {
		narrow := min(max((w-sidebarW-1)*42/100, 40), 60)
		wide := w - sidebarW - 1 - narrow - 1
		l.main, l.side = narrow, wide
		if m.here.kind == placeThread && m.here.term != "" {
			l.main, l.side = wide, narrow // an agent's terminal needs the width more than Details does
		}
	}
	return l
}

// hasSide reports whether the place on show has a computer to show beside it: a thread's, or a
// teammate's on its home.
func (m *app) hasSide() bool {
	return (m.here.kind == placeThread || m.here.kind == placeMate) && m.computerOn() != nil
}

// paneKind is what a column of the frame shows.
type paneKind int

const (
	paneNone     paneKind = iota
	paneHomeList          // Home's list and its reading pane
	paneThread            // a thread: its transcript and composer, or its terminal
	paneMateHome          // a teammate's home
	paneComputer          // the computer, with its tabs
	paneReview            // a push's review alone
	paneScreen            // a full screen: Activity, Settings, the keys…
)

// mainPane is what the main column shows: the place, or its computer while tab flipped to it
// or it is in full screen.
func (m *app) mainPane() paneKind {
	if m.screen != screenMain {
		return paneScreen
	}
	switch m.here.kind {
	case placeHome:
		return paneHomeList
	case placeComputer:
		return paneComputer
	case placeReview:
		return paneReview
	}
	if m.computerOn() != nil && (m.zoom && m.focus == focusSide || m.layoutFor(m.width).side == 0 && m.flip) {
		return paneComputer
	}
	if m.here.kind == placeMate {
		return paneMateHome
	}
	return paneThread
}

// sidePane is what the side column shows, if it is drawn.
func (m *app) sidePane() paneKind {
	if m.layoutFor(m.width).side > 0 {
		return paneComputer
	}
	return paneNone
}

// frameColumns is where the main and side columns' text goes on screen: after the split and a
// cell of room when a column is left of it.
func (m *app) frameColumns() (main, side rect) {
	l := m.layoutFor(m.width)
	h := m.bodyRows()
	x := 0
	if l.sidebar > 0 {
		x = l.sidebar + 1
	}
	room := 0
	if x > 0 {
		room = 1
	}
	main = rect{x + room, bodyTop, max(l.main-room, 1), h}
	if l.side > 0 {
		sx := x + l.main + 1
		side = rect{sx + 1, bodyTop, max(l.side-1, 1), h}
	}
	return main, side
}

// paneRect is where a pane of the frame is on screen, ok false when it isn't drawn.
func (m *app) paneRectOf(k paneKind) (rect, bool) {
	main, side := m.frameColumns()
	switch {
	case m.mainPane() == k:
		return main, true
	case m.sidePane() == k:
		return side, true
	}
	return rect{}, false
}

// mainW is the width of the main column's text, which the full screens are laid out in.
func (m *app) mainW() int {
	main, _ := m.frameColumns()
	return main.w
}

// shellZoomed reports whether a shell has the keys in full screen, which draws it alone with a
// status line.
func (m *app) shellZoomed() bool {
	return m.zoom && m.screen == screenMain && m.paneFocused() && m.pane.term != nil
}

// mainFrame draws the frame of w×h for the screen on show: the crumb row and its rule, the
// sidebar beside them and the body, the columns, the bottom rule, the key bar and the notice line.
func (m *app) mainFrame(w, h int) []string {
	if m.shellZoomed() {
		return m.zoomFrame(w, h)
	}
	l := m.layoutFor(w)
	rows := max(h-5, 1)
	x0 := 0
	if l.sidebar > 0 {
		x0 = l.sidebar + 1
	}
	rest := max(w-x0, 1)
	right := []string{m.crumbRow(rest, x0, l.sidebar == 0), styleDim.Render(strings.Repeat("─", rest))}
	right = append(right, pad(m.columnsBody(l, rows), rows)...)
	lines := right
	if l.sidebar > 0 {
		left := m.sidebarColumn(l.sidebar, rows+2)
		split := styleDim.Render("│")
		lines = make([]string, len(right))
		for i := range right {
			sep := split
			if i >= bodyTop && m.mainSplitLit() {
				sep = styleAccent.Render("┃")
			}
			lines[i] = fitStyled(left[i], l.sidebar) + sep + right[i]
		}
	}
	if box := m.toastBox(w); box != nil && len(box) <= rows {
		overlay(lines, box, bodyTop, w)
	}
	lines = append(lines, m.bottomRule(w, l))
	if m.drawer && l.sidebar == 0 && m.screen == screenMain {
		m.drawDrawer(lines, w)
	}
	return lines
}

// mainSplitLit reports whether the split left of the main column is the accent's: its shell or
// composer has the keys.
func (m *app) mainSplitLit() bool {
	if m.screen != screenMain {
		return false
	}
	k := m.mainPane()
	switch {
	case k == paneThread && m.chats.composing:
		return true
	case k == paneMateHome && m.chats.composing:
		return true
	}
	rc, ok := m.paneRect()
	main, _ := m.frameColumns()
	return ok && m.paneFocused() && rc.x == main.x
}

// columnsBody is the body right of the sidebar: the main column, and the side one with the
// split between them, the accent's while the side has the keys.
func (m *app) columnsBody(l layout, h int) []string {
	main, side := m.frameColumns()
	room := ""
	if l.sidebar > 0 {
		room = " "
	}
	left := m.column(main, m.paneLines(m.mainPane(), rect{0, bodyTop, main.w, h}))
	if l.side == 0 {
		out := make([]string, h)
		for i := range out {
			out[i] = room + fitStyled(left[i], main.w)
		}
		return out
	}
	right := m.column(side, m.paneLines(m.sidePane(), rect{0, bodyTop, side.w, h}))
	sep := styleDim.Render("│")
	if m.focus == focusSide {
		sep = styleAccent.Render("┃")
	}
	out := make([]string, h)
	for i := range out {
		out[i] = room + fitStyled(left[i], main.w) + sep + " " + right[i]
	}
	return out
}

// column pads a column's lines to its rows and moves the rows and keys it recorded for the
// mouse from the column's own cells to the screen's.
func (m *app) column(rc rect, draw func() []string) []string {
	rows, keys := len(m.mouse.rows), len(m.mouse.keys)
	lines := pad(draw(), rc.h)
	for i := rows; i < len(m.mouse.rows); i++ {
		m.mouse.rows[i].x0 += rc.x
		m.mouse.rows[i].x1 += rc.x
	}
	for i := keys; i < len(m.mouse.keys); i++ {
		m.mouse.keys[i].x0 += rc.x
		m.mouse.keys[i].x1 += rc.x
	}
	return lines
}

// paneLines draws a pane in the part rc of its column, rc at the column's own cells.
func (m *app) paneLines(k paneKind, rc rect) func() []string {
	return func() []string {
		switch k {
		case paneHomeList:
			return m.homeBody(rc.w, rc.h)
		case paneThread:
			if m.here.term != "" {
				return m.threadTerminal(rc.w, rc.h)
			}
			return m.threadPane(rc.w, rc.h)
		case paneMateHome:
			return m.mateHome(rc.w, rc.h)
		case paneComputer:
			return m.computerPane(rc.w, rc.h)
		case paneReview:
			return m.reviewPlace(rc.w, rc.h)
		case paneScreen:
			return m.body(rc.w, rc.h)
		}
		return nil
	}
}

// bottomRule is the rule under the body: ┴ under the splits, and the accent's heavy rule while a
// shell has the keys.
func (m *app) bottomRule(w int, l layout) string {
	line, cross, style := '─', '┴', styleDim
	if m.paneFocused() && m.screen == screenMain {
		line, cross, style = '━', '┻', styleAccent
	}
	cells := []rune(strings.Repeat(string(line), w))
	for _, c := range m.paneColumns(w, l) {
		if c > 0 && c < w {
			cells[c] = cross
		}
	}
	if m.drawer && l.sidebar == 0 && m.screen == screenMain && sidebarW < w {
		cells[sidebarW] = '┘'
	}
	return style.Render(string(cells))
}

// paneColumns are the columns of the splits of the screen on show: the sidebar's, the side
// column's, and the splits a pane draws inside its column.
func (m *app) paneColumns(w int, l layout) []int {
	var cols []int
	x0 := 0
	if l.sidebar > 0 {
		x0 = l.sidebar + 1
		cols = append(cols, l.sidebar)
	}
	if l.side > 0 {
		cols = append(cols, x0+l.main)
	}
	main, _ := m.frameColumns()
	switch {
	case m.screen == screenMain && m.mainPane() == paneHomeList:
		if x, ok := m.inboxColumn(); ok {
			cols = append(cols, x)
		}
	case m.screen == screenComputers && m.computersSplit(main.w):
		cols = append(cols, main.x+sidebarWidth(main.w))
	case m.screen == screenMain && m.mainPane() == paneReview && m.pagerSplit(main.w):
		cols = append(cols, main.x+pagerListWidth(main.w))
	case m.screen == screenMain && m.comp.tab == tabChanges && m.computerOn() != nil:
		if rc, ok := m.paneRectOf(paneComputer); ok && rc.w >= railBeside && len(m.changeRows()) > 0 {
			cols = append(cols, rc.x+railWidth(rc.w))
		}
	case m.screen == screenMain && m.comp.tab == tabFiles && m.computerOn() != nil:
		if sb := m.computerOn(); phaseOf(*sb) == phaseReady {
			if rc, ok := m.paneRectOf(paneComputer); ok && m.filesSplit(rc.w) {
				cols = append(cols, rc.x+filesListW)
			}
		}
	}
	return cols
}

// crumbRow is the crumb row in w cells at column x: ≡ when the sidebar isn't drawn, where you
// are, and on the right the computer on show, what waits for you and the time. It records its
// parts for the mouse.
func (m *app) crumbRow(w, x int, menu bool) string {
	m.mouse.crumb = m.mouse.crumb[:0]
	left := " "
	if menu {
		left += "≡  "
		m.mouse.crumb = append(m.mouse.crumb, crumbSpan{x0: x + 1, x1: x + 2, part: crumbMenu})
	}
	left += styleBold.Render(m.crumb())
	var parts []string
	var spans []crumbPart
	if sb := m.computerOn(); sb != nil && m.screen == screenMain {
		chip := m.phaseDot(phaseOf(*sb)) + " " + sb.Name
		if len(m.sandboxesOf(sb.TeammateID)) > 1 && sb.TeammateID != "" {
			chip += " ▾"
		}
		parts, spans = append(parts, chip), append(spans, crumbChip)
	}
	n := m.needCount()
	switch {
	case n > 0 && m.screen == screenMain && m.here.kind == placeHome:
		parts, spans = append(parts, m.waitingMark(fmt.Sprintf("▲ %d waiting for you", n))), append(spans, crumbWaiting)
	case n > 0:
		parts, spans = append(parts, m.waitingMark(fmt.Sprintf("▲ %d", n))), append(spans, crumbWaiting)
	case m.screen == screenMain && m.here.kind == placeHome:
		parts, spans = append(parts, styleDim.Render("nothing waits for you")), append(spans, crumbWaiting)
	}
	if x+w >= paneWidth {
		parts, spans = append(parts, styleDim.Render(m.now.Format("15:04"))), append(spans, crumbClock)
	}
	right := strings.Join(parts, styleDim.Render(" · ")) + " "
	row := spread(left, right, w)
	at := x + w - lipgloss.Width(right)
	for i, p := range parts {
		pw := lipgloss.Width(p)
		m.mouse.crumb = append(m.mouse.crumb, crumbSpan{x0: at, x1: at + pw, part: spans[i]})
		at += pw + 3
	}
	return row
}

// crumbPart is a part of the crumb row that a click acts on.
type crumbPart int

const (
	crumbMenu    crumbPart = iota // ≡ opens the drawer
	crumbChip                     // the computer cycles to the teammate's next one
	crumbWaiting                  // what waits goes to Home
	crumbClock
	crumbEnv // the sidebar's environment opens the picker
)

// crumbSpan is where a part of the crumb row, or of the sidebar's first row, is.
type crumbSpan struct {
	x0, x1 int
	part   crumbPart
}

// crumb is where you are: the place on show, or the full screen over it.
func (m *app) crumb() string {
	s := m.screen
	switch s {
	case screenActivity, screenEntry:
		return "Activity"
	case screenSettings:
		return "Settings"
	case screenComputers:
		return "Computers"
	case screenEnvs:
		return "Environments"
	case screenHelp:
		return "Keys"
	case screenNew, screenProgress:
		return "New computer"
	case screenCheckpoints:
		return "Checkpoints"
	}
	p := m.here
	mate := m.teammates[p.mate]
	sb := m.hereSandbox()
	switch p.kind {
	case placeMate:
		if m.mainPane() == paneComputer {
			return mate + " › desk"
		}
		return firstNonEmpty(mate, "Teammate")
	case placeThread:
		title := m.threadTitle()
		if mate == "" && sb != nil {
			return sb.Name + " › " + title
		}
		return firstNonEmpty(mate, "Thread") + " › " + title
	case placeReview:
		return "Review"
	case placeComputer:
		if sb == nil {
			return "Computer"
		}
		if t := m.teammates[sb.TeammateID]; t != "" {
			return t + " › desk"
		}
		return sb.Name
	}
	return "Home"
}

// threadTitle is the title of the thread on show.
func (m *app) threadTitle() string {
	p := m.here
	switch {
	case p.term != "":
		if sb, ok := m.sandboxByID(p.sandbox); ok {
			for _, x := range m.sessionsOf(sb) {
				if x.term.Name == p.term {
					return x.title()
				}
			}
		}
		return p.term
	case p.chat == "":
		return "New thread"
	case m.chats.on && m.chats.id == p.chat && m.chats.loaded:
		return chatTitle(m.chats.chat)
	}
	if c, ok := m.chatByID(p.chat); ok {
		return chatTitle(c)
	}
	return "Thread"
}

// sidebarColumn is the sidebar in w cells and h rows: Studio and the environment, the filter,
// and the list.
func (m *app) sidebarColumn(w, h int) []string {
	head := " " + m.pal.brand.Render("◆") + " " + styleBold.Render("Studio")
	if m.look.plain {
		head = " ◆ " + styleBold.Render("Studio")
	}
	env := firstNonEmpty(m.env.Name, "…") + " ▾ "
	m.mouse.crumb = append(m.mouse.crumb, crumbSpan{x0: w - lipgloss.Width(env), x1: w - 1, part: crumbEnv})
	filter := styleDim.Render(" / Filter")
	switch {
	case m.filtering:
		filter = " / " + m.filter + styleAccent.Render("▏")
	case m.filter != "":
		filter = spread(" / "+styleBold.Render(m.filter), styleDim.Render("esc clears "), w-1)
	}
	lines := []string{spread(head, env, w-1), filter}
	rows := m.homeRows()
	switch {
	case m.env.ID == "":
	case !m.loaded:
		lines = append(lines, "", styleDim.Render(" Loading…"))
	case m.filter != "" && len(rows) == 0:
		lines = append(lines, "", styleDim.Render(clip(" Nothing matches “"+m.filter+"”.", w-1)))
	default:
		lines = append(lines, m.treeLines(rows, w-1, h-2)...)
	}
	return pad(lines, h)
}

// drawDrawer draws the sidebar as a drawer over the left of the frame's lines, down to the
// bottom rule, the body behind it left as it is.
func (m *app) drawDrawer(lines []string, w int) {
	if w <= sidebarW+1 {
		return
	}
	rows := len(m.mouse.rows)
	box := m.sidebarColumn(sidebarW, len(lines)-1)
	for i, b := range box {
		if i >= len(lines)-1 {
			break
		}
		lines[i] = fitStyled(b, sidebarW) + styleDim.Render("│") + ansi.Cut(lines[i], sidebarW+1, w)
	}
	// The drawer's rows are on top: a click there is the sidebar's.
	m.mouse.rows = append(m.mouse.rows[rows:], m.mouse.rows[:rows]...)
}

// zoomFrame is a shell with the keys in full screen: its screen in every row but the last, and
// a status line that says which it is and how to leave.
func (m *app) zoomFrame(w, h int) []string {
	p := &m.pane
	lines := pad(p.term.lines(w, max(h-1, 1)), max(h-1, 0))
	where := ""
	if x, ok := m.paneTarget(); ok {
		if sb, found := m.sandboxByID(x.sandbox); found {
			where = m.phaseDot(phaseOf(sb)) + " " + sb.Name + " › " + x.title()
		}
	}
	status := spread(" "+where, styleDim.Render("ctrl+] keys back · alt+z")+" ", w)
	if box := m.toastBox(w); box != nil && len(box) < h {
		overlay(lines, box, 0, w)
	}
	return append(lines, status)
}
