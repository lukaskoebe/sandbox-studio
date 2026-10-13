package tui

import "fmt"

// detailBody is the sandbox on the cursor, with the facts the list does not have room for.
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
	lines := []string{
		spread(styleBold.Render(sb.Name), ph.cell(colStatus), w),
		"",
		fact("Teammate", teammate),
		fact("Template", sb.TemplateName),
		fact("Resources", fmt.Sprintf("%d CPUs, %d MiB of memory", sb.CPUs, sb.MemoryMiB)),
		fact("Uptime", running),
		fact("Approvals", approvals),
		fact("ID", sb.ID),
	}
	return lines
}

// fact is a label and its value on one line of the detail.
func fact(label, value string) string {
	return "  " + styleDim.Render(fit(label, 11)) + value
}

// envsBody is the environment picker: the cursor's environment, and the one on show.
func (m *app) envsBody(w, h int) []string {
	lines := []string{styleBold.Render("Environments")}
	for i, env := range m.envs {
		cursor, name := "  ", env.Name
		if i == m.picker {
			cursor, name = styleAccent.Render("▸")+" ", styleBold.Render(env.Name)
		}
		if env.ID == m.env.ID {
			name += styleDim.Render("  on screen")
		}
		lines = append(lines, cursor+name)
	}
	return lines
}
