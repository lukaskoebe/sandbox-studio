package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// phase is a sandbox's state as the web words it: the dot and the word of the status line.
type phase int

const (
	phaseCreating phase = iota
	phaseBooting
	phaseReady
	phaseSuspended
	phaseStopped
	phaseFailed
)

// phaseOf maps a sandbox's VM status to its phase. A running VM is ready once its guest agent
// is connected, as in the web UI.
func phaseOf(sb sandboxes.View) phase {
	switch sb.Status {
	case "creating":
		return phaseCreating
	case "running":
		if sb.Agent != nil {
			return phaseReady
		}
		return phaseBooting
	case "starting", "created":
		return phaseBooting
	case "suspended":
		return phaseSuspended
	case "crashed", "absent", "failed":
		return phaseFailed
	default:
		return phaseStopped
	}
}

// label is the phase's word; dot is its mark: ● running, ◐ busy, ○ stopped.
func (p phase) label() string {
	return [...]string{"Creating", "Booting", "Running", "Suspended", "Stopped", "Failed"}[p]
}

// cell is the phase's mark and word, styled, in a cell of w cells.
func (p phase) cell(w int) string {
	var mark string
	style := styleDim
	switch p {
	case phaseCreating, phaseBooting:
		mark, style = "◐", styleAccent
	case phaseReady:
		mark, style = "●", styleAccent
	case phaseFailed:
		mark, style = "●", styleFailed
	default:
		mark = "○"
	}
	return style.Render(mark) + " " + fit(p.label(), w-2)
}

// colStatus is the width of a sandbox's status in its detail.
const colStatus = 11

// colSpark is the width of the CPU sparkline in a sandbox's header, which it gets when the
// header has room for it.
const colSpark = 8

// cursorMark is the mark of the cursor's row: ▸ in the plain look, the accent's rule ▌ in the
// visual one. While Home's pane has the keys, the sidebar's cursor is dim.
func (m *app) cursorMark(selected bool) string {
	style := styleAccent
	if m.paneFocused() {
		style = styleDim
	}
	switch {
	case !selected:
		return "  "
	case m.look.plain:
		return style.Render("▸") + " "
	}
	return style.Render("▌") + " "
}

// groupHeader is a sandbox's row, the header of its sessions in w cells, indent cells in: its
// name and teammate (and template, on a wide list), a rule, and on the right the approvals
// waiting on it, its CPU sparkline when there's room, and its status. A sandbox under its
// teammate's row doesn't name the teammate again.
func (m *app) groupHeader(sb sandboxes.View, selected bool, indent, w int) string {
	ph := phaseOf(sb)
	wide := w >= 64
	left := m.cursorMark(selected) + strings.Repeat(" ", indent) + styleBold.Render(sb.Name)
	if t := m.teammates[sb.TeammateID]; t != "" && indent == 0 {
		left += styleDim.Render(" · " + t)
	}
	if wide && sb.TemplateName != "" {
		left += "  " + styleDim.Render(sb.TemplateName)
	}
	right := m.groupStatus(sb, ph, wide)
	if n := m.pendingFor(sb.ID); n > 0 {
		waiting := fmt.Sprintf("▲ %d", n)
		if wide {
			waiting += " " + plural(n, "approval", "approvals")
		}
		right = m.waitingMark(waiting) + "  " + right
	}
	if !m.look.plain && ph != phaseStopped && ph != phaseSuspended && ph != phaseFailed &&
		w-lipgloss.Width(left)-lipgloss.Width(right)-colSpark-3 >= 4 {
		right = m.cpuSpark(sb, colSpark) + " " + right
	}
	return ruledRow(left, right, w)
}

// groupStatus is a sandbox's status in its header: "● 2h" while it runs ("● up 2h 14m" on a
// wide list), "◐ booting", "○ stopped".
func (m *app) groupStatus(sb sandboxes.View, ph phase, wide bool) string {
	word := strings.ToLower(ph.label())
	var dot string
	switch ph {
	case phaseCreating, phaseBooting:
		dot = styleAccent.Render("◐")
	case phaseReady:
		dot = styleAccent.Render("●")
		if !m.look.plain {
			dot = m.pal.dot("running")
		}
		if sb.StartedAt != nil {
			if wide {
				word = uptime(m.now.Sub(*sb.StartedAt))
			} else {
				word = shortUptime(m.now.Sub(*sb.StartedAt))
			}
		}
	case phaseFailed:
		dot = styleFailed.Render("●")
		if !m.look.plain {
			dot = m.pal.dot("failed")
		}
	default:
		dot = styleDim.Render("○")
	}
	return dot + " " + styleDim.Render(word)
}

// shortUptime is an uptime in its largest unit: "12m", "2h", "4d".
func shortUptime(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// homeBody is Home: the tree of the environment's teammates, sandboxes and sessions, with the
// pane beside it on a wide terminal.
func (m *app) homeBody(w, h int) []string {
	switch {
	case m.env.ID == "":
		return []string{styleDim.Render("No environment yet. Start Studio once, then run studio tui again.")}
	case !m.loaded:
		return []string{styleDim.Render("Loading…")}
	case m.filter != "" && len(m.homeRows()) == 0:
		return []string{styleDim.Render("Nothing matches “" + m.filter + "”.")}
	}
	if m.homeSplit(w) {
		return m.splitBody(w, h)
	}
	if m.at == atHome {
		return m.narrowHome(w, h)
	}
	return m.sidebar(w, h)
}

// sidebar is the rows of Home's list in w cells and h lines, the cursor's row in view: Home,
// each teammate with its sandboxes and sessions under it, and a blank line between teammates.
func (m *app) sidebar(w, h int) []string {
	return m.treeLines(m.homeRows(), w, h)
}

// treeLines draws rows of Home's list in w cells and h lines, the cursor's row in view.
func (m *app) treeLines(rows []homeRow, w, h int) []string {
	all := m.homeRows()
	at := m.cursorRow(all)
	index := map[homeRow]int{} // a row's index in the whole list, which a click selects
	for i, r := range all {
		index[r] = i
	}
	const blank, label = -1, -2
	var order []int // the row of each line, blank or label for the lines between
	cursorLine := 0
	for i, r := range rows {
		if r.gap && len(order) > 0 {
			order = append(order, blank)
		}
		if r.label != "" {
			order = append(order, label)
		}
		if index[r] == at {
			cursorLine = len(order)
		}
		order = append(order, i)
	}
	// The list scrolls once the cursor passes the last line.
	offset := 0
	if cursorLine >= h {
		offset = cursorLine - h + 1
	}
	visible := m.visible()
	var lines []string
	for _, i := range order[offset:] {
		if len(lines) >= h {
			break
		}
		switch i {
		case blank:
			lines = append(lines, "")
			continue
		case label:
			lines = append(lines, "  "+styleDim.Render("no teammate"))
			continue
		}
		r := rows[i]
		m.hitRow(len(lines), 0, w, rowTree, index[r])
		lines = append(lines, m.treeLine(r, visible, index[r] == at, w))
	}
	return lines
}

// joinPanes lays two panes side by side in h rows: the left one w cells wide, a dim line, and
// the right one.
func joinPanes(left, right []string, w, h int) []string {
	left, right = pad(left, h), pad(right, h)
	out := make([]string, h)
	for i := range out {
		out[i] = fitStyled(left[i], w) + styleDim.Render("│") + " " + right[i]
	}
	return out
}
