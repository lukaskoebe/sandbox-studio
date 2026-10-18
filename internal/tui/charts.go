package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The dashboard is Home's pane while the cursor is on a sandbox: its facts, its CPU over the last
// two minutes as an area chart, its memory as a gauge and a history, the approval waiting on it,
// its terminals, ports and history, its network as it happens, and its teammate's browser.

// areaChart draws samples, the newest on the right, as an area of rows rows against top: each
// sample is per columns wide, and a column fills in eighths of a cell, so the chart is finer than
// its rows. Samples not read yet are blank, with a dim dot on the bottom row. A row's cells take
// the style that row's height asks for: style, or high for the rows above three quarters of top.
func areaChart(samples []float64, slots, per int, top float64, rows int, style, high lipgloss.Style) []string {
	if len(samples) > slots {
		samples = samples[len(samples)-slots:]
	}
	missing := slots - len(samples)
	out := make([]string, rows)
	for r := range rows {
		floor := (rows - 1 - r) * 8 // the eighths under this row
		rowStyle := style
		if rows > 1 && float64(floor) >= 0.75*float64(rows*8) {
			rowStyle = high
		}
		var b strings.Builder
		if missing > 0 {
			blank := strings.Repeat(" ", missing*per)
			if r == rows-1 {
				blank = styleDim.Render(strings.Repeat("·", missing*per))
			}
			b.WriteString(blank)
		}
		var cells strings.Builder
		for _, v := range samples {
			eighths := 1 // a sample of 0 still shows the lowest block
			if top > 0 {
				eighths = max(int(math.Round(min(max(v/top, 0), 1)*float64(rows*8))), 1)
			}
			cell := " "
			switch level := eighths - floor; {
			case level >= 8:
				cell = "█"
			case level > 0:
				cell = string(sparkBlocks[level-1])
			}
			cells.WriteString(strings.Repeat(cell, per))
		}
		b.WriteString(rowStyle.Render(cells.String()))
		out[r] = b.String()
	}
	return out
}

// chartSize is the slots and the columns per slot of a chart in w cells: every sample when they
// fit, each as wide as the room allows, or the newest that fit.
func chartSize(w int) (slots, per int) {
	if w >= usageKeep {
		return usageKeep, min(w/usageKeep, 2)
	}
	return max(w, 1), 1
}

// stats are the mean and the peak of samples.
func stats(samples []float64) (mean, peak float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	for _, v := range samples {
		mean += v
		peak = max(peak, v)
	}
	return mean / float64(len(samples)), peak
}

// spanText is how long slots samples take: "2 min", "40 s".
func spanText(slots int) string {
	secs := slots * int(usageEvery.Seconds())
	if secs >= 60 && secs%60 == 0 {
		return fmt.Sprintf("%d min", secs/60)
	}
	return fmt.Sprintf("%d s", secs)
}

// usageCharts are a running sandbox's CPU chart and its memory gauge and history in w cells,
// with rows rows for the CPU's area.
func (m *app) usageCharts(sb sandboxes.View, w, rows int) []string {
	const axis = 4 // "100┤"
	switch p := phaseOf(sb); {
	case p == phaseBooting || p == phaseCreating:
		return []string{styleDim.Render("CPU  ") + shimmer(m.spin, min(24, w-5))}
	case p != phaseReady:
		return []string{styleDim.Render("Not running; s starts it.")}
	case m.usage.unsupported:
		return []string{styleDim.Render("This runtime reports no CPU or memory use.")}
	}
	u, ok := m.usage.last[sb.ID]
	if !ok {
		return []string{styleBold.Render("CPU") + styleDim.Render("  reading…")}
	}
	slots, per := chartSize(w - axis)
	cw := slots * per
	cpu := m.usage.cpu[sb.ID]
	mean, peak := stats(cpu)
	cpus := max(u.CPUs, sb.CPUs)
	head := styleBold.Render("CPU") + "  " + styleBold.Render(fmt.Sprintf("%.0f %%", u.CPUPercent)) +
		styleDim.Render(fmt.Sprintf(" of %d %s", cpus, plural(cpus, "CPU", "CPUs")))
	lines := []string{spread(head, styleDim.Render(fmt.Sprintf("avg %.0f %% · peak %.0f %%", mean, peak)), axis+cw)}
	area := areaChart(cpu, slots, per, 100, rows, m.pal.running, m.pal.waiting)
	for i, l := range area {
		label := "   │"
		switch i {
		case 0:
			label = "100┤"
		case rows - 1:
			label = "  0┤"
		}
		lines = append(lines, styleDim.Render(label)+l)
	}
	lines = append(lines, strings.Repeat(" ", axis)+spread(styleDim.Render(spanText(slots)+" ago"), styleDim.Render("now"), cw), "")

	memStyle := m.pal.hue(areaMemory)
	share := 0.0
	if u.MemoryTotalBytes > 0 {
		share = float64(u.MemoryBytes) / float64(u.MemoryTotalBytes)
	}
	memHead := styleBold.Render("MEMORY") + "  " + styleBold.Render(memText(u))
	lines = append(lines, spread(memHead, styleDim.Render(fmt.Sprintf("%.0f %%", share*100)), axis+cw))
	lines = append(lines, styleDim.Render("    ")+bar(share, cw, memStyle))
	hist := areaChart(m.usage.mem[sb.ID], slots, per, 1, 1, memStyle, memStyle)
	return append(lines, styleDim.Render("    ")+hist[0])
}

// dashboard is the sandbox under the cursor in Home's pane, in w cells and h rows.
func (m *app) dashboard(sb sandboxes.View, w, h int) []string {
	ph := phaseOf(sb)
	facts := []string{firstNonEmpty(m.teammates[sb.TeammateID], "no teammate")}
	facts = append(facts, nonEmpty(sb.TemplateName)...)
	facts = append(facts, fmt.Sprintf("%d %s", sb.CPUs, plural(sb.CPUs, "CPU", "CPUs")), sizeText(uint64(sb.MemoryMiB)<<20))
	status := m.statusCell(ph, len(ph.label())+2)
	if (ph == phaseReady || ph == phaseBooting) && sb.StartedAt != nil {
		status += styleDim.Render(" · " + uptime(m.now.Sub(*sb.StartedAt)))
	}
	lines := []string{
		spread(styleBold.Render(sb.Name), status, w),
		styleDim.Render(strings.Repeat("─", w)),
		clip(strings.Join(facts, " · "), w),
		"",
	}
	if !m.look.plain {
		rows := 4
		if h < 30 {
			rows = 3
		}
		if h < 20 {
			lines = append(lines, m.usageLines(sb, w)...)
		} else {
			lines = append(lines, m.usageCharts(sb, w, rows)...)
		}
	}
	if card := m.waitingCard(sb, w); card != nil {
		lines = append(append(lines, ""), card...)
	}
	lines = append(lines, "")
	label := func(name string) string { return styleDim.Render(fit(name, 11)) }
	if ph == phaseReady {
		lines = append(lines, label("TERMINALS")+m.terminalsText(sb.ID), label("PORTS")+m.portsText(sb.ID, w-11))
	}
	if !m.look.plain {
		history := m.historyLine(sb, w-11)
		lines = append(lines, label("HISTORY")+history[0])
		for _, l := range history[1:] {
			lines = append(lines, strings.Repeat(" ", 11)+l)
		}
	}
	lines = append(lines, label("ID")+styleDim.Render(sb.ID))
	// The network and the browser share the rows left: the network first, a few of its newest.
	if left := h - len(lines) - 1; left >= 3 && ph == phaseReady {
		net := m.netPane(w, min(left, 8))
		lines = append(append(lines, ""), net...)
	}
	if more := m.browserLines(sb.TeammateID, w, h-len(lines)-1); more != nil {
		lines = append(append(lines, ""), more...)
	}
	return lines
}
