package tui

import (
	"math"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// The shared pieces of the visual look: status dots, keycaps, cards, sparklines, bars, the
// spinner, the tab header and the grouped key bar. The plain look draws none of them.

// spinFrames are the frames of the spinner of a running turn or tool call.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// sparkBlocks are the eight heights of a sparkline.
var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

// sparkline draws the last w values as block heights against top, right-aligned in w cells:
// a shorter history leaves blanks on the left. A value of 0 still shows the lowest block.
func sparkline(values []float64, top float64, w int) string {
	if w <= 0 {
		return ""
	}
	if len(values) > w {
		values = values[len(values)-w:]
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", w-len(values)))
	for _, v := range values {
		i := 0
		if top > 0 {
			i = int(math.Round(min(max(v/top, 0), 1) * float64(len(sparkBlocks)-1)))
		}
		b.WriteRune(sparkBlocks[i])
	}
	return b.String()
}

// bar draws a share from 0 to 1 as a bar of w cells: filled in fill, the rest in dim shade.
func bar(share float64, w int, fill lipgloss.Style) string {
	if w <= 0 {
		return ""
	}
	n := int(math.Round(min(max(share, 0), 1) * float64(w)))
	return fill.Render(strings.Repeat("█", n)) + styleDim.Render(strings.Repeat("░", w-n))
}

// shimmer is the moving shade of something starting, in w cells: a band of ▒▓ that runs over ░.
func shimmer(frame, w int) string {
	if w <= 0 {
		return ""
	}
	cells := []rune(strings.Repeat("░", w))
	at := frame % (w + 2)
	for i, r := range []rune("▒▓▒") {
		if j := at - 1 + i; j >= 0 && j < w {
			cells[j] = r
		}
	}
	return styleDim.Render(string(cells))
}

// spinner is the spinner's frame on show.
func (m *app) spinner() string {
	return spinFrames[m.spin%len(spinFrames)]
}

// dot is a status's mark in its color: running, waiting and failed are filled, stopped hollow.
func (p *palette) dot(status string) string {
	switch status {
	case "running":
		return p.running.Render("●")
	case "busy":
		return p.running.Render("◐")
	case "waiting":
		return p.waiting.Render("●")
	case "failed":
		return p.failed.Render("●")
	}
	return p.stopped.Render("○")
}

// statusCell is a phase's dot and word in a cell of w cells. The visual look colors the dot by
// the web's status set and never the word.
func (m *app) statusCell(p phase, w int) string {
	if m.look.plain {
		return p.cell(w)
	}
	status := "stopped"
	switch p {
	case phaseCreating, phaseBooting:
		status = "busy"
	case phaseReady:
		status = "running"
	case phaseFailed:
		status = "failed"
	}
	return m.pal.dot(status) + " " + fit(p.label(), w-2)
}

// keycap is a key as a cap: bold on a dim background, or in brackets on 16 colors.
func (p *palette) cap(k string) string {
	if p.capBg {
		return p.keycap.Render(" " + k + " ")
	}
	return styleDim.Render("[") + p.keycap.Render(k) + styleDim.Render("]")
}

// choiceCap is one choice of a card: its keycap and what it does.
type choiceCap struct {
	key, label string
}

// keySpan is where a key sits on a line, the cells from x0 to x1 (excluded), and the key a
// click on it presses.
type keySpan struct {
	x0, x1 int
	key    string
}

// capsLine lays the choices of a card out as keycaps on one line, and says where each sits. The
// at-th choice is the one the arrows are on, drawn by focused.
func (p *palette) capsLine(caps []choiceCap, at int, focused func(string) string) (string, []keySpan) {
	parts := make([]string, 0, len(caps))
	spans := make([]keySpan, 0, len(caps))
	x := 0
	for i, c := range caps {
		part := p.cap(c.key) + " " + c.label
		if i == at {
			part = focused(c.key + " " + c.label)
		}
		spans = append(spans, keySpan{x0: x, x1: x + lipgloss.Width(part), key: c.key})
		x += lipgloss.Width(part) + 2
		parts = append(parts, part)
	}
	return strings.Join(parts, "  "), spans
}

// cardBox draws lines in a card of w cells: a rounded frame in frame's style with the area's
// rule down its left edge.
func cardBox(lines []string, w int, rule, frame lipgloss.Style) []string {
	inner, edge := max(w-4, 1), max(w-2, 0)
	out := []string{frame.Render("╭" + strings.Repeat("─", edge) + "╮")}
	for _, l := range lines {
		out = append(out, rule.Render("┃")+" "+fitStyled(l, inner)+" "+frame.Render("│"))
	}
	return append(out, frame.Render("╰"+strings.Repeat("─", edge)+"╯"))
}

// Key groups of the key bar: what acts on the thing under the cursor, where to go from it, and
// the keys of the app itself.
const (
	groupAct = iota
	groupGo
	groupApp
)

// barKey is the key a click on a binding of the key bar presses, or "" for one a click doesn't
// press: the arrows that scroll, and the keys with a modifier.
func barKey(b key.Binding) string {
	keys := b.Keys()
	if len(keys) == 0 || keys[0] == "up" || keys[0] == "down" || strings.Contains(keys[0], "+") {
		return ""
	}
	return keys[0]
}

// keyGroup is the group a key of the key bar belongs to.
func keyGroup(b key.Binding) int {
	h := b.Help()
	switch h.Key + " " + h.Desc {
	case "t terminal", "tab computer", "tab thread", "\\ sidebar", "m next computer", "l activity", "e env",
		"h checkpoints", "/ filter", "/ computer", ", settings", "r rules (browser)":
		return groupGo
	case "? help", "q quit", "esc/q back", "esc back", "esc cancel", "esc close":
		return groupApp
	}
	return groupAct
}

// groupedKeys is the visual key bar of w cells: the keys that apply, grouped by what they act
// on with a dim │ between the groups. Help reads "more", since it lists the keys the bar has no
// room for. When the separators leave too little room the groups go without them, as the plain
// bar has, and what still doesn't fit is cut with an ellipsis.
func (m *app) groupedKeys(bindings []key.Binding, w int) (string, []keySpan) {
	type item struct {
		text, key string
	}
	var groups [3][]item
	for _, b := range bindings {
		if !b.Enabled() || b.Help().Key == "" {
			continue
		}
		h := b.Help()
		desc := h.Desc
		if h.Key == "?" {
			desc = "more"
		}
		g := keyGroup(b)
		groups[g] = append(groups[g], item{styleBold.Render(h.Key) + " " + styleDim.Render(desc), barKey(b)})
	}
	join := func(between string) (string, []keySpan, bool) {
		type piece struct {
			text, key string
			sep       int // the cells of the separator the piece starts with
		}
		var pieces []piece
		total := 0
		for _, g := range groups {
			for i, it := range g {
				sep := "  "
				switch {
				case len(pieces) == 0:
					sep = ""
				case i == 0:
					sep = between
				}
				pieces = append(pieces, piece{sep + it.text, it.key, lipgloss.Width(sep)})
				total += lipgloss.Width(sep + it.text)
			}
		}
		fits := total <= w
		limit := w
		if !fits {
			limit = w - 2
		}
		var b strings.Builder
		var spans []keySpan
		used := 0
		for _, p := range pieces {
			pw := lipgloss.Width(p.text)
			if used+pw > limit {
				break
			}
			b.WriteString(p.text)
			if p.key != "" {
				spans = append(spans, keySpan{x0: used + p.sep, x1: used + pw, key: p.key})
			}
			used += pw
		}
		if !fits {
			b.WriteString(styleDim.Render(" …"))
		}
		return b.String(), spans, fits
	}
	if line, spans, ok := join(styleDim.Render(" │ ")); ok {
		return line, spans
	}
	line, spans, _ := join("  ")
	return line, spans
}
