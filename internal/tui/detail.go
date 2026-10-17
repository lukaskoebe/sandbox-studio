package tui

import (
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// detailBody is the sandbox on the cursor, with the facts the list does not have room for and
// the ports it listens on.
func (m *app) detailBody(w, h int) []string {
	sb := m.selected()
	if sb == nil {
		return []string{styleDim.Render("No sandbox is selected.")}
	}
	ph := phaseOf(*sb)
	teammate := m.teammates[sb.TeammateID]
	if teammate == "" {
		teammate = "none"
	}
	running := "—"
	if (ph == phaseReady || ph == phaseBooting) && sb.StartedAt != nil {
		running = uptime(m.now.Sub(*sb.StartedAt))
	}
	approvals := "none waiting"
	if n := m.pendingFor(sb.ID); n > 0 {
		approvals = styleAccent.Render(fmt.Sprintf("%d %s waiting", n, plural(n, "approval", "approvals")))
	}
	if n := m.pendingFor(sb.ID); n > 0 && !m.look.plain {
		approvals = m.pal.waiting.Render("●") + fmt.Sprintf(" %d %s waiting", n, plural(n, "approval", "approvals"))
	}
	lines := []string{
		spread(styleBold.Render(sb.Name), m.statusCell(ph, colStatus), w),
		"",
		fact("Teammate", teammate),
		fact("Template", sb.TemplateName),
		fact("Resources", fmt.Sprintf("%d CPUs, %d MiB of memory", sb.CPUs, sb.MemoryMiB)),
		fact("Uptime", running),
	}
	if !m.look.plain {
		for i, l := range m.usageLines(*sb, w-13) {
			label := ""
			if i == 0 {
				label = "Usage"
			}
			lines = append(lines, fact(label, l))
		}
	}
	lines = append(lines, fact("Approvals", approvals), fact("ID", sb.ID))
	if !m.look.plain {
		for i, l := range m.historyLine(*sb, w-13) {
			label := ""
			if i == 0 {
				label = "History"
			}
			lines = append(lines, fact(label, l))
		}
	}
	return append(lines, m.portLines(sb.ID, ph == phaseReady)...)
}

// detailPane is the selected sandbox beside Home's list, in w cells: its status, its facts, its
// CPU and memory, the first approval waiting on it, its terminals, ports and history.
func (m *app) detailPane(sb sandboxes.View, w, h int) []string {
	ph := phaseOf(sb)
	facts := []string{firstNonEmpty(m.teammates[sb.TeammateID], "no teammate")}
	facts = append(facts, nonEmpty(sb.TemplateName)...)
	facts = append(facts, fmt.Sprintf("%d %s", sb.CPUs, plural(sb.CPUs, "CPU", "CPUs")), sizeText(uint64(sb.MemoryMiB)<<20))
	if (ph == phaseReady || ph == phaseBooting) && sb.StartedAt != nil {
		facts = append(facts, uptime(m.now.Sub(*sb.StartedAt)))
	}
	lines := []string{
		spread(styleBold.Render(sb.Name), m.statusCell(ph, colStatus), w),
		styleDim.Render(strings.Repeat("─", w)),
		clip(strings.Join(facts, " · "), w),
		"",
	}
	lines = append(lines, m.usageLines(sb, w)...)
	if card := m.waitingCard(sb, w); card != nil {
		lines = append(append(lines, ""), card...)
	}
	lines = append(lines, "")
	label := func(name string) string { return styleDim.Render(fit(name, 11)) }
	if ph == phaseReady {
		lines = append(lines, label("TERMINALS")+m.terminalsText(sb.ID), label("PORTS")+m.portsText(sb.ID, w-11))
	}
	history := m.historyLine(sb, w-11)
	lines = append(lines, label("HISTORY")+history[0])
	for _, l := range history[1:] {
		lines = append(lines, strings.Repeat(" ", 11)+l)
	}
	return append(lines, label("ID")+styleDim.Render(sb.ID))
}

// waitingCard is the first approval waiting on a sandbox as its card, and how many
// more wait; nil when none does.
func (m *app) waitingCard(sb sandboxes.View, w int) []string {
	var first *card
	n := 0
	for _, c := range m.cards() {
		if c.first().SandboxID != sb.ID {
			continue
		}
		n++
		if first == nil {
			first = &c
		}
	}
	if first == nil {
		return nil
	}
	more := "a approvals"
	if n > 1 {
		more = fmt.Sprintf("%d more · a approvals", n-1)
	}
	return append(m.visualCard(*first, false, w), "  "+styleDim.Render(more))
}

// terminalsText names the terminals of the detail pane's sandbox and counts them.
func (m *app) terminalsText(id string) string {
	p := &m.ports
	if p.sandbox != id || !p.loaded {
		return styleDim.Render("…")
	}
	if len(p.terminals) == 0 {
		return styleDim.Render("none yet; t opens main")
	}
	names := make([]string, 0, len(p.terminals))
	for _, t := range p.terminals {
		names = append(names, t.Name)
	}
	return strings.Join(names, " · ") + styleDim.Render(fmt.Sprintf(" · %d", len(names)))
}

// portsText lists the open ports of the detail pane's sandbox on one line of w cells; the detail
// screen has their addresses.
func (m *app) portsText(id string, w int) string {
	p := &m.ports
	switch {
	case p.sandbox != id || !p.loaded:
		return styleDim.Render("…")
	case len(p.list) == 0:
		return styleDim.Render("none open")
	}
	ports := make([]string, 0, len(p.list))
	for _, port := range p.list {
		ports = append(ports, fmt.Sprintf(":%d", port.Port))
	}
	return clip(strings.Join(ports, " · "), w)
}

// fact is a label and its value on one line of the detail.
func fact(label, value string) string {
	return "  " + styleDim.Render(fit(label, 11)) + value
}

// envsBody is the environment picker: the cursor's environment, and the one on show.
func (m *app) envsBody(w, h int) []string {
	lines := []string{styleBold.Render("Environments")}
	for i, env := range m.envs {
		m.hitRow(len(lines), 0, w, rowEnv, i)
		cursor, name := "  ", env.Name
		if i == m.picker {
			cursor, name = m.cursorMark(true), styleBold.Render(env.Name)
		}
		if env.ID == m.env.ID {
			name += styleDim.Render("  on screen")
		}
		lines = append(lines, cursor+name)
	}
	return lines
}
