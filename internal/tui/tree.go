package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// Home's list is the web's sidebar: Home first, with what waits for you; TEAMMATES, each with
// its threads under it; YOUR COMPUTERS, the computers without a teammate with their terminals;
// and ENVIRONMENT, the places of the environment and Settings.

// rowKind is what a row of Home's list is.
type rowKind int

const (
	atSandbox rowKind = iota // a computer of yours, under YOUR COMPUTERS, or a terminal under it
	atHome                   // Home's row: what waits for you
	atMate                   // a teammate's row
	atThread                 // a row under a teammate: a thread, or a terminal of its computers
	atPlace                  // an ENVIRONMENT row, or Settings
	atAction                 // + New teammate, + New computer
)

// envPlace is a row of ENVIRONMENT.
type envPlace int

const (
	placeComputers envPlace = iota
	placeNetwork
	placeSecrets
	placeMemory
	placeActivity
	placeSettings
)

// envPlaces are ENVIRONMENT's rows, in order.
var envPlaces = []envPlace{placeComputers, placeNetwork, placeSecrets, placeMemory, placeActivity, placeSettings}

// rowAction is a row that makes something new.
type rowAction int

const (
	actionNewTeammate rowAction = iota
	actionNewComputer
)

// Sections of Home's list, the dim labels above their first rows.
const (
	sectionTeammates   = "TEAMMATES"
	sectionComputers   = "YOUR COMPUTERS"
	sectionEnvironment = "ENVIRONMENT"
)

// homeRow is a row of Home's list. sb is a computer by its index among the visible ones: a
// computer of yours, the computer of a thread, a teammate's first computer, or -1. sub is the
// row's index among threadsOf(mate) for a thread, among sessionsOf for a terminal of yours, and
// -1 for the computer's own row.
type homeRow struct {
	kind    rowKind
	mate    string // the teammate of a teammate's row or of a thread
	sb, sub int
	place   envPlace
	action  rowAction
	num     int    // the session's number, 1 to 9, or 0
	gap     bool   // a blank line goes before the row
	section string // the section's label, drawn before its first row
}

// homeRows are the rows of Home's list, in order. While the filter narrows the list, only the
// teammates and computers that match show, with their threads and terminals.
func (m *app) homeRows() []homeRow {
	visible := m.visible()
	filtering := m.filter != ""
	index := map[string]int{}
	for i, sb := range visible {
		index[sb.ID] = i
	}
	at := func(id string) int {
		if i, ok := index[id]; ok {
			return i
		}
		return -1
	}
	var rows []homeRow
	current := ""
	add := func(r homeRow, section string) {
		if section != current {
			current = section
			r.section, r.gap = section, len(rows) > 0
		}
		rows = append(rows, r)
	}
	num := 0
	number := func(r *homeRow, x session) {
		if x.kind != sessionOlder && num < 9 {
			num++
			r.num = num
		}
	}
	if !filtering {
		add(homeRow{kind: atHome, sb: -1, sub: -1}, "")
	}
	for _, t := range m.team {
		list := m.sandboxesOf(t.ID)
		if filtering && len(list) == 0 && !matches(m.filter, t.Name) {
			continue
		}
		r := homeRow{kind: atMate, mate: t.ID, sb: -1, sub: -1}
		if len(list) > 0 {
			r.sb = at(list[0].ID)
		}
		add(r, sectionTeammates)
		for j, x := range m.threadsOf(t.ID) {
			tr := homeRow{kind: atThread, mate: t.ID, sb: at(x.sandbox), sub: j}
			number(&tr, x)
			add(tr, sectionTeammates)
		}
	}
	if !filtering {
		add(homeRow{kind: atAction, action: actionNewTeammate, sb: -1, sub: -1}, sectionTeammates)
	}
	for _, i := range m.yourComputers() {
		add(homeRow{sb: i, sub: -1}, sectionComputers)
		for j, x := range m.sessionsOf(visible[i]) {
			r := homeRow{sb: i, sub: j}
			number(&r, x)
			add(r, sectionComputers)
		}
	}
	if filtering {
		return rows
	}
	add(homeRow{kind: atAction, action: actionNewComputer, sb: -1, sub: -1}, sectionComputers)
	for _, p := range envPlaces {
		add(homeRow{kind: atPlace, place: p, sb: -1, sub: -1}, sectionEnvironment)
	}
	return rows
}

// setCursor puts the cursor on a row.
func (m *app) setCursor(r homeRow) {
	m.at, m.mate = r.kind, r.mate
	if r.sb >= 0 {
		m.cursor = r.sb
	}
	m.sub = r.sub
	m.rowPlace, m.rowAction = r.place, r.action
}

// goHome puts the cursor on Home's row, and Home on show.
func (m *app) goHome() {
	m.screen = screenHome
	m.at, m.mate, m.sub = atHome, "", -1
	m.pane.focus = false
	m.filter = ""
}

// cursorRow is the index of the cursor's row among rows. A thread that went leaves the cursor on
// its teammate's row, and so does a teammate's computer, which has no row of its own here.
func (m *app) cursorRow(rows []homeRow) int {
	sub := m.subRow()
	for i, r := range rows {
		if r.kind != m.at {
			continue
		}
		switch r.kind {
		case atHome:
			return i
		case atMate:
			if r.mate == m.mate {
				return i
			}
		case atThread:
			if r.mate == m.mate && r.sub == sub {
				return i
			}
		case atPlace:
			if r.place == m.rowPlace {
				return i
			}
		case atAction:
			if r.action == m.rowAction {
				return i
			}
		default:
			if r.sb == m.cursor && r.sub == sub {
				return i
			}
		}
	}
	mate := m.mate
	if m.at == atSandbox {
		if visible := m.visible(); m.cursor >= 0 && m.cursor < len(visible) {
			mate = visible[m.cursor].TeammateID
		}
	}
	for i, r := range rows {
		if (m.at == atThread || m.at == atSandbox) && r.kind == atMate && r.mate == mate {
			return i
		}
	}
	return 0
}

// mateOf is the teammate with the ID.
func (m *app) mateOf(id string) (store.Teammate, bool) {
	for _, t := range m.team {
		if t.ID == id {
			return t, true
		}
	}
	return store.Teammate{}, false
}

// sandboxesOf are the visible computers of a teammate, in the list's order.
func (m *app) sandboxesOf(mate string) []sandboxes.View {
	var out []sandboxes.View
	for _, sb := range m.visible() {
		if sb.TeammateID == mate {
			out = append(out, sb)
		}
	}
	return out
}

// selectedMate is the teammate whose row the cursor is on.
func (m *app) selectedMate() (store.Teammate, bool) {
	if m.at != atMate {
		return store.Teammate{}, false
	}
	return m.mateOf(m.mate)
}

// needCount counts what waits for you, in every environment: Home's cards, where requests that
// arrive together are one.
func (m *app) needCount() int {
	return len(m.cards())
}

// waitingOn counts Home's cards that wait on one of the computers.
func (m *app) waitingOn(ids ...string) int {
	n := 0
	for _, c := range m.cards() {
		for _, id := range ids {
			if c.first().SandboxID == id {
				n++
				break
			}
		}
	}
	return n
}

// waitingMark is "▲ n", in the waiting color.
func (m *app) waitingMark(text string) string {
	if m.look.plain {
		return styleAccent.Render(text)
	}
	return m.pal.waiting.Render(text)
}

// ruledRow is a row of w cells: left, a rule, and right.
func ruledRow(left, right string, w int) string {
	fill := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if fill < 2 {
		left = fitStyled(left, max(w-lipgloss.Width(right)-4, 6))
		fill = w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	}
	return left + " " + styleDim.Render(strings.Repeat("─", max(fill, 1))) + " " + right
}

// homeLine is Home's row: what waits for you, or that nothing does.
func (m *app) homeLine(selected bool, w int) string {
	left := m.cursorMark(selected) + styleBold.Render("Home")
	right := styleDim.Render("✓ all clear")
	if n := m.needCount(); n > 0 {
		text := fmt.Sprintf("▲ %d", n)
		if w >= 40 {
			text += " waiting for you"
		}
		right = m.waitingMark(text)
	} else if !m.look.plain {
		right = m.pal.running.Render("✓") + styleDim.Render(" all clear")
	}
	return spread(left, right, w)
}

// mateLine is a teammate's row: its name and role, and on the right the spinner while one of its
// threads works and what waits on its computers.
func (m *app) mateLine(t store.Teammate, selected bool, w int) string {
	left := m.cursorMark(selected) + styleBold.Render(t.Name)
	if t.Role != "" {
		left += " " + styleDim.Render(t.Role)
	}
	list := m.sandboxesOf(t.ID)
	var right []string
	for _, x := range m.threadsOf(t.ID) {
		if m.stateOf(x) == stateWorking {
			right = append(right, m.stateMark(stateWorking))
			break
		}
	}
	ids := make([]string, len(list))
	for i, sb := range list {
		ids[i] = sb.ID
	}
	if n := m.waitingOn(ids...); n > 0 {
		right = append(right, m.waitingMark(fmt.Sprintf("▲ %d", n)))
	}
	if len(list) == 0 && w >= 40 {
		right = append(right, styleDim.Render("no computer"))
	}
	return spread(left, strings.Join(right, " "), w)
}

// computerLine is a computer's row under YOUR COMPUTERS: its status dot and name, its template
// on a wide list, and on the right what waits on it and its status.
func (m *app) computerLine(sb sandboxes.View, selected bool, w int) string {
	ph := phaseOf(sb)
	wide := w >= 64
	left := m.cursorMark(selected) + m.phaseDot(ph) + " " + styleBold.Render(sb.Name)
	if wide && sb.TemplateName != "" {
		left += "  " + styleDim.Render(sb.TemplateName)
	}
	word := strings.ToLower(ph.label())
	if ph == phaseReady && sb.StartedAt != nil {
		word = shortUptime(m.now.Sub(*sb.StartedAt))
		if wide {
			word = uptime(m.now.Sub(*sb.StartedAt))
		}
	}
	right := styleDim.Render(word)
	if n := m.waitingOn(sb.ID); n > 0 {
		right = m.waitingMark(fmt.Sprintf("▲ %d", n)) + " " + right
	}
	return spread(left, right, w)
}

// placeName is an ENVIRONMENT row's name.
func placeName(p envPlace) string {
	return [...]string{"Computers", "Network", "Secrets", "Memory", "Activity", "Settings"}[p]
}

// networkCards are Home's cards that ask for the network.
func (m *app) networkCards() []card {
	var out []card
	for _, c := range m.cards() {
		if isNetwork(c.first()) {
			out = append(out, c)
		}
	}
	return out
}

// placeLine is an ENVIRONMENT row: the place, and on the right its count, or the version for
// Settings.
func (m *app) placeLine(p envPlace, selected bool, w int) string {
	name := placeName(p)
	if selected {
		name = styleBold.Render(name)
	}
	left := m.cursorMark(selected) + name
	right := ""
	switch p {
	case placeComputers:
		right = styleDim.Render(fmt.Sprint(len(m.sandboxes)))
	case placeNetwork:
		if n := len(m.networkCards()); n > 0 {
			right = m.waitingMark(fmt.Sprintf("▲ %d", n))
		}
	case placeSettings:
		left = m.cursorMark(selected) + styleDim.Render(", ") + name
		right = styleDim.Render(version.Version)
	}
	return spread(left, right, w)
}

// actionLine is a row that makes something new: + New teammate, + New computer.
func (m *app) actionLine(a rowAction, selected bool, w int) string {
	text := [...]string{"+ New teammate", "+ New computer"}[a]
	if selected {
		return fitStyled(m.cursorMark(true)+styleBold.Render(text), w)
	}
	return fitStyled(m.cursorMark(false)+styleDim.Render(text), w)
}

// treeLine is the line of a row of Home's list.
func (m *app) treeLine(r homeRow, visible []sandboxes.View, selected bool, w int) string {
	switch r.kind {
	case atHome:
		return m.homeLine(selected, w)
	case atMate:
		t, _ := m.mateOf(r.mate)
		return m.mateLine(t, selected, w)
	case atPlace:
		return m.placeLine(r.place, selected, w)
	case atAction:
		return m.actionLine(r.action, selected, w)
	}
	if r.kind == atSandbox && r.sub < 0 {
		return m.computerLine(visible[r.sb], selected, w)
	}
	x, ok := m.rowSession(r, visible)
	if !ok {
		return ""
	}
	return m.sessionRow(x, r.num, selected, 0, w)
}
