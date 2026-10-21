package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Home's list is a tree, as the web's sidebar: Home first, with what needs you, then each
// teammate with what it works in, and the sandboxes without a teammate below. A teammate with one
// sandbox lists that sandbox's sessions right under it, its computer; the sandbox level shows
// only once it has two or more.

// rowKind is what a row of Home's list is.
type rowKind int

const (
	atSandbox rowKind = iota // a sandbox's row, or a session under it
	atHome                   // Home's row: what needs you
	atMate                   // a teammate's row
)

// homeRow is a row of Home's list: Home, a teammate, a sandbox by its index among the visible
// ones, or one of its sessions by its index under it (sub); sub is -1 for the sandbox's own row.
type homeRow struct {
	kind    rowKind
	mate    string // the teammate of a teammate's row
	sb, sub int    // sb is -1 on Home's row, and on the row of a teammate with no sandbox
	num     int    // the session's number, 1 to 9, or 0
	indent  int    // the cells a row under a teammate with several sandboxes is indented by
	gap     bool   // a blank line goes before the row
	label   string // a dim line before the row: "no teammate"
}

// homeRows are the rows of Home's list, in order.
func (m *app) homeRows() []homeRow {
	visible := m.visible()
	var rows []homeRow
	if m.filter == "" {
		rows = append(rows, homeRow{kind: atHome, sb: -1, sub: -1})
	}
	num := 0
	sessions := func(i, indent int) {
		for j, x := range m.sessionsOf(visible[i]) {
			r := homeRow{sb: i, sub: j, indent: indent}
			if x.kind != sessionOlder && num < 9 {
				num++
				r.num = num
			}
			rows = append(rows, r)
		}
	}
	known := map[string]bool{}
	for _, t := range m.team {
		known[t.ID] = true
	}
	owned := map[string][]int{}
	var unowned []int
	for i, sb := range visible {
		if known[sb.TeammateID] {
			owned[sb.TeammateID] = append(owned[sb.TeammateID], i)
		} else {
			unowned = append(unowned, i)
		}
	}
	for _, t := range m.team {
		list := owned[t.ID]
		if m.filter != "" && len(list) == 0 && !matches(m.filter, t.Name) {
			continue
		}
		r := homeRow{kind: atMate, mate: t.ID, sb: -1, sub: -1, gap: len(rows) > 0}
		if len(list) > 0 {
			r.sb = list[0]
		}
		rows = append(rows, r)
		if len(list) == 1 {
			sessions(list[0], 0)
			continue
		}
		for _, i := range list {
			rows = append(rows, homeRow{sb: i, sub: -1, indent: 2})
			sessions(i, 2)
		}
	}
	for k, i := range unowned {
		r := homeRow{sb: i, sub: -1, gap: len(rows) > 0}
		if k == 0 && len(rows) > 0 && len(m.team) > 0 {
			r.label = "no teammate"
		}
		rows = append(rows, r)
		sessions(i, 0)
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
}

// goHome puts the cursor on Home's row, and Home on show.
func (m *app) goHome() {
	m.screen = screenHome
	m.at, m.mate, m.sub = atHome, "", -1
	m.pane.focus = false
	m.filter = ""
}

// cursorRow is the index of the cursor's row among rows. A sandbox whose own row isn't drawn,
// the one sandbox of a teammate, is on its teammate's row.
func (m *app) cursorRow(rows []homeRow) int {
	sub := m.subRow()
	for i, r := range rows {
		switch {
		case r.kind != m.at:
		case r.kind == atHome:
			return i
		case r.kind == atMate:
			if r.mate == m.mate {
				return i
			}
		case r.sb == m.cursor && r.sub == sub:
			return i
		}
	}
	for i, r := range rows {
		if m.at == atSandbox && r.kind == atMate && r.sb == m.cursor {
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

// sandboxesOf are the visible sandboxes of a teammate, in the list's order.
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

// waitingMark is "▲ n", in the waiting color.
func (m *app) waitingMark(text string) string {
	if m.look.plain {
		return styleAccent.Render(text)
	}
	return m.pal.waiting.Render(text)
}

// ruledRow is a tree row of w cells: left, a rule, and right.
func ruledRow(left, right string, w int) string {
	fill := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if fill < 2 {
		left = fitStyled(left, max(w-lipgloss.Width(right)-4, 6))
		fill = w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	}
	return left + " " + styleDim.Render(strings.Repeat("─", max(fill, 1))) + " " + right
}

// homeLine is Home's row: what needs you, or that nothing does.
func (m *app) homeLine(selected bool, w int) string {
	left := m.cursorMark(selected) + styleBold.Render("Home")
	right := styleDim.Render("✓ all clear")
	if n := m.needCount(); n > 0 {
		right = m.waitingMark(fmt.Sprintf("▲ %d need you", n))
	} else if !m.look.plain {
		right = m.pal.running.Render("✓") + styleDim.Render(" all clear")
	}
	return ruledRow(left, right, w)
}

// mateLine is a teammate's row: its name and its computer, or how many sandboxes it has, and on
// the right what waits on them and how its one sandbox is.
func (m *app) mateLine(t store.Teammate, selected bool, w int) string {
	list := m.sandboxesOf(t.ID)
	wide := w >= 64
	left := m.cursorMark(selected) + styleBold.Render(t.Name)
	switch len(list) {
	case 0:
		left += styleDim.Render(" · no sandbox")
	case 1:
		left += styleDim.Render(" · " + list[0].Name)
	default:
		left += styleDim.Render(fmt.Sprintf(" · %d sandboxes", len(list)))
	}
	var right string
	waiting := 0
	running := 0
	for _, sb := range list {
		waiting += m.pendingFor(sb.ID)
		if phaseOf(sb) == phaseReady {
			running++
		}
	}
	switch {
	case len(list) == 1:
		ph := phaseOf(list[0])
		right = m.groupStatus(list[0], ph, wide)
		if !m.look.plain && ph == phaseReady && w-lipgloss.Width(left)-lipgloss.Width(right)-colSpark-3 >= 4 {
			right = m.cpuSpark(list[0], colSpark) + " " + right
		}
	case running > 0:
		right = styleAccent.Render("●") + styleDim.Render(fmt.Sprintf(" %d running", running))
	case len(list) > 1:
		right = styleDim.Render("○ stopped")
	}
	if waiting > 0 {
		mark := fmt.Sprintf("▲ %d", waiting)
		if right == "" {
			right = m.waitingMark(mark)
		} else {
			right = m.waitingMark(mark) + "  " + right
		}
	}
	if right == "" {
		right = " "
	}
	return ruledRow(left, right, w)
}

// treeLine is the line of a row of Home's list.
func (m *app) treeLine(r homeRow, visible []sandboxes.View, selected bool, w int) string {
	switch {
	case r.kind == atHome:
		return m.homeLine(selected, w)
	case r.kind == atMate:
		t, _ := m.mateOf(r.mate)
		return m.mateLine(t, selected, w)
	case r.sub < 0:
		return m.groupHeader(visible[r.sb], selected, r.indent, w)
	}
	list := m.sessionsOf(visible[r.sb])
	return m.sessionRow(list[r.sub], r.num, selected, r.indent, w)
}
