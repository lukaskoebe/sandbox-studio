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

// groupHeader is a sandbox's row, the header of its sessions in w cells: its name and teammate
// (and template, on a wide list), a rule, and on the right the approvals waiting on it, its CPU
// sparkline when there's room, and its status.
func (m *app) groupHeader(sb sandboxes.View, selected bool, w int) string {
	ph := phaseOf(sb)
	wide := w >= 64
	left := m.cursorMark(selected) + styleBold.Render(sb.Name)
	if t := m.teammates[sb.TeammateID]; t != "" {
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
		if m.look.plain {
			waiting = styleAccent.Render(waiting)
		} else {
			waiting = m.pal.waiting.Render(waiting)
		}
		right = waiting + "  " + right
	}
	if !m.look.plain && ph != phaseStopped && ph != phaseSuspended && ph != phaseFailed &&
		w-lipgloss.Width(left)-lipgloss.Width(right)-colSpark-3 >= 4 {
		right = m.cpuSpark(sb, colSpark) + " " + right
	}
	fill := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if fill < 2 {
		left = fitStyled(left, max(w-lipgloss.Width(right)-4, 6))
		fill = w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	}
	return left + " " + styleDim.Render(strings.Repeat("─", max(fill, 1))) + " " + right
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

// homeBody is Home: the sidebar of the environment's sandboxes and their sessions, with the pane
// beside it on a wide terminal.
func (m *app) homeBody(w, h int) []string {
	switch {
	case m.env.ID == "":
		return []string{styleDim.Render("No environment yet. Start Studio once, then run studio tui again.")}
	case !m.loaded:
		return []string{styleDim.Render("Loading…")}
	case len(m.sandboxes) == 0:
		return []string{
			"No sandboxes in " + m.env.Name + " yet.",
			"",
			styleDim.Render("Press n to create one."),
		}
	}
	visible := m.visible()
	if len(visible) == 0 {
		return []string{styleDim.Render("No sandbox matches “" + m.filter + "”.")}
	}
	if m.homeSplit(w) {
		return m.splitBody(visible, w, h)
	}
	return m.sidebar(visible, w, h)
}

// sidebar is the rows of Home's list in w cells and h lines, the cursor's row in view: each
// sandbox as the header of a group, its sessions under it, and a blank line between groups.
func (m *app) sidebar(visible []sandboxes.View, w, h int) []string {
	rows := m.homeRows()
	at := m.cursorRow(rows)
	var order []int // the row of each line, -1 for the blank line before a group
	cursorLine := 0
	for i, r := range rows {
		if r.sub < 0 && i > 0 {
			order = append(order, -1)
		}
		if i == at {
			cursorLine = len(order)
		}
		order = append(order, i)
	}
	// The list scrolls once the cursor passes the last line.
	offset := 0
	if cursorLine >= h {
		offset = cursorLine - h + 1
	}
	var lines []string
	sessions := map[int][]session{}
	for _, i := range order[offset:] {
		if len(lines) >= h {
			break
		}
		if i < 0 {
			lines = append(lines, "")
			continue
		}
		r := rows[i]
		selected := i == at
		if r.sub < 0 {
			m.hitRow(len(lines), 0, w, rowSandbox, r.sb)
			lines = append(lines, m.groupHeader(visible[r.sb], selected, w))
			continue
		}
		list, ok := sessions[r.sb]
		if !ok {
			list = m.sessionsOf(visible[r.sb])
			sessions[r.sb] = list
		}
		m.hitSession(len(lines), 0, w, r.sb, r.sub)
		lines = append(lines, m.sessionRow(list[r.sub], r.num, selected, w))
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
