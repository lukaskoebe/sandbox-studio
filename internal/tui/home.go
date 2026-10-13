package tui

import (
	"fmt"

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

// Column widths of the sandbox list: cursor, status, name, teammate, template, uptime, approvals.
const (
	colStatus    = 11
	colTeammate  = 9
	colUptime    = 10
	colApprovals = 11
)

// row is one sandbox of the list: its status, name, teammate, template, uptime and the
// approvals waiting on it. The name is bold on the cursor's row.
func (m *app) row(sb sandboxes.View, selected bool, w int) string {
	cursor := "  "
	if selected {
		cursor = styleAccent.Render("▸") + " "
	}
	ph := phaseOf(sb)
	// The flexible columns share what the fixed ones leave: the name gets the larger half.
	flex := max(w-2-colStatus-colTeammate-colUptime-colApprovals-4, 8)
	nameW := flex / 2
	tmplW := max(flex-nameW-1, 4)

	name := fit(sb.Name, nameW)
	if selected {
		name = styleBold.Render(name)
	}
	teammate := m.teammates[sb.TeammateID]
	teammateCell := styleDim.Render(fit("—", colTeammate))
	if teammate != "" {
		teammateCell = fit(teammate, colTeammate)
	}
	up := ""
	if ph == phaseReady || ph == phaseBooting {
		if sb.StartedAt != nil {
			up = uptime(m.now.Sub(*sb.StartedAt))
		}
	}
	approvals := ""
	if n := m.pendingFor(sb.ID); n > 0 {
		approvals = styleAccent.Render(fmt.Sprintf("%d %s", n, plural(n, "approval", "approvals")))
	}
	return cursor + ph.cell(colStatus) + " " + name + " " + teammateCell + " " +
		styleDim.Render(fit(sb.TemplateName, tmplW)) + " " + styleDim.Render(fit(up, colUptime)) + " " + approvals
}

// homeBody is the sandbox list of the environment on show, the cursor's row in view.
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
			styleDim.Render("Create one in the browser: " + m.browserURL("/e/"+m.env.ID)),
		}
	}
	visible := m.visible()
	if len(visible) == 0 {
		return []string{styleDim.Render("No sandbox matches “" + m.filter + "”.")}
	}
	// The cursor's row stays in view: the list scrolls once the cursor passes the last row.
	offset := 0
	if m.cursor >= h {
		offset = m.cursor - h + 1
	}
	var lines []string
	for i := offset; i < len(visible) && len(lines) < h; i++ {
		lines = append(lines, m.row(visible[i], i == m.cursor, w))
	}
	return lines
}
