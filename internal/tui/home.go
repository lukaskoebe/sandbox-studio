package tui

import (
	"fmt"
	"strings"
	"time"

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

// Wide layouts of the visual look: Home's list gets the selected sandbox's detail beside it from
// splitWidth columns, and its rows a CPU sparkline from sparkWidth.
const (
	splitWidth = 120
	sparkWidth = 100
	colSpark   = 8
)

// cursorMark is the mark of the cursor's row: ▸ in the plain look, the accent's rule ▌ in the
// visual one.
func (m *app) cursorMark(selected bool) string {
	switch {
	case !selected:
		return "  "
	case m.look.plain:
		return styleAccent.Render("▸") + " "
	}
	return styleAccent.Render("▌") + " "
}

// sparkColumn reports whether Home's rows show their CPU sparkline.
func (m *app) sparkColumn(w int) bool {
	return !m.look.plain && w >= sparkWidth
}

// homeSplit reports whether Home shows the selected sandbox's detail next to the list.
func (m *app) homeSplit(w int) bool {
	return !m.look.plain && w >= splitWidth && m.env.ID != "" && m.loaded && len(m.visible()) > 0
}

// homeListWidth is the width of the list in Home's wide layout; the detail pane gets the rest.
func homeListWidth(w int) int {
	return min(max(w*2/5, 54), 66)
}

// row is one sandbox of the list: its status, name, teammate, template, uptime and the
// approvals waiting on it. The name is bold on the cursor's row.
func (m *app) row(sb sandboxes.View, selected bool, w int) string {
	cursor := m.cursorMark(selected)
	ph := phaseOf(sb)
	spark := ""
	if m.sparkColumn(w) {
		w -= colSpark + 1
		spark = m.cpuSpark(sb, colSpark) + " "
	}
	// The flexible columns share what the fixed ones leave: the name gets the larger half. The
	// visual look's approvals have a dot before them.
	flex := max(w-2-colStatus-colTeammate-colUptime-colApprovals-4, 8)
	if !m.look.plain {
		flex = max(flex-2, 8)
	}
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
		approvals = fmt.Sprintf("%d %s", n, plural(n, "approval", "approvals"))
		if m.look.plain {
			approvals = styleAccent.Render(approvals)
		} else {
			approvals = m.pal.waiting.Render("●") + " " + approvals
		}
	}
	return cursor + m.statusCell(ph, colStatus) + " " + name + " " + teammateCell + " " +
		styleDim.Render(fit(sb.TemplateName, tmplW)) + " " + spark + styleDim.Render(fit(up, colUptime)) + " " + approvals
}

// Columns of the list in Home's wide layout: teammate, uptime and the approvals waiting.
const (
	colPaneTeammate = 7
	colShortUptime  = 4
	colWaiting      = 3
)

// paneRow is one sandbox of the list in Home's wide layout, in w cells: its status, name,
// teammate, CPU sparkline, uptime, and the approvals waiting on it. The detail pane has the rest.
func (m *app) paneRow(sb sandboxes.View, selected bool, w int) string {
	ph := phaseOf(sb)
	nameW := max(w-2-colStatus-colPaneTeammate-colSpark-colShortUptime-colWaiting-5, 6)
	name := fit(sb.Name, nameW)
	if selected {
		name = styleBold.Render(name)
	}
	teammate := styleDim.Render(fit("—", colPaneTeammate))
	if t := m.teammates[sb.TeammateID]; t != "" {
		teammate = fit(t, colPaneTeammate)
	}
	up := ""
	if (ph == phaseReady || ph == phaseBooting) && sb.StartedAt != nil {
		up = shortUptime(m.now.Sub(*sb.StartedAt))
	}
	waiting := strings.Repeat(" ", colWaiting)
	if n := m.pendingFor(sb.ID); n > 0 {
		waiting = m.pal.waiting.Render("●") + fit(fmt.Sprint(n), colWaiting-1)
	}
	return m.cursorMark(selected) + m.statusCell(ph, colStatus) + " " + name + " " + teammate + " " +
		m.cpuSpark(sb, colSpark) + " " + styleDim.Render(fit(up, colShortUptime)) + " " + waiting
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
			styleDim.Render("Press n to create one."),
		}
	}
	visible := m.visible()
	if len(visible) == 0 {
		return []string{styleDim.Render("No sandbox matches “" + m.filter + "”.")}
	}
	if m.homeSplit(w) {
		return m.splitHome(visible, w, h)
	}
	// The cursor's row stays in view: the list scrolls once the cursor passes the last row.
	offset := 0
	if m.cursor >= h {
		offset = m.cursor - h + 1
	}
	var lines []string
	for i := offset; i < len(visible) && len(lines) < h; i++ {
		m.hitRow(len(lines), 0, w, rowSandbox, i)
		lines = append(lines, m.row(visible[i], i == m.cursor, w))
	}
	return lines
}

// splitHome is Home's wide layout: the list on the left, the cursor's sandbox in a detail pane on
// the right, as a mail client shows a message.
func (m *app) splitHome(visible []sandboxes.View, w, h int) []string {
	listW := homeListWidth(w)
	left := []string{styleDim.Render(fmt.Sprintf("SANDBOXES  %d", len(visible)))}
	rows := max(h-1, 1)
	offset := 0
	if m.cursor >= rows {
		offset = m.cursor - rows + 1
	}
	for i := offset; i < len(visible) && len(left) < h; i++ {
		m.hitRow(len(left), 0, listW, rowSandbox, i)
		left = append(left, m.paneRow(visible[i], i == m.cursor, listW-1))
	}
	var right []string
	detailW := w - listW - 2
	if m.homeDash(w) {
		detailW -= netPaneWidth(w) + 2
	}
	if sb := m.selected(); sb != nil {
		right = m.detailPane(*sb, detailW-1, h)
	}
	if m.homeDash(w) {
		right = joinPanes(right, m.netPane(netPaneWidth(w), h), detailW, h)
	}
	return joinPanes(left, right, listW, h)
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
