package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The composer is a box at the bottom of the chat, under the transcript: the prompt as it is
// typed, wrapped, its newest lines in view, framed in the chat's color while it has the keys.
// Otherwise it is one dim line that says how to start writing.

// composerLines is how many lines of the prompt the composer shows at most.
const composerLines = 6

// composerOpen reports whether the chat on show takes prompts, so it shows the composer.
func (m *app) composerOpen() bool {
	c := &m.chats
	switch {
	case c.gone, !c.loaded && c.id != "":
		return false
	case c.chat.State == store.ChatTerminal, c.chat.State == store.ChatOrphaned:
		return false
	}
	return true
}

// composer is the composer's box in w cells, or nil when the chat takes no prompt.
func (m *app) composer(w int) []string {
	c := &m.chats
	if !m.composerOpen() || w < 12 {
		return nil
	}
	inner := w - 4
	var text []string
	switch {
	case c.composing:
		for _, l := range strings.Split(c.draft.value(), "\n") {
			wrapped := wrapText(l, inner-1)
			if len(wrapped) == 0 {
				wrapped = []string{""} // an empty line of the prompt
			}
			text = append(text, wrapped...)
		}
		if len(text) > composerLines {
			text = text[len(text)-composerLines:]
		}
		text[len(text)-1] += styleAccent.Render("▏")
	default:
		// Not writing, the composer is one line, so the transcript keeps the rows: a field on the
		// keycaps' background, or dim text where there is none.
		hint := "Write to " + m.teammateName() + "…"
		switch {
		case c.sending:
			hint = "Sending…"
		case c.draft.value() != "":
			hint = clip(oneLine(c.draft.value()), max(w-24, 8)) + "  (a draft)"
		}
		line := spread(" › "+hint, "p write ", w)
		if m.pal.capBg && !m.look.plain {
			return []string{m.pal.field.Faint(true).Render(line)}
		}
		return []string{styleDim.Render(line)}
	}
	frame, label := styleAccent, styleBold
	if !m.look.plain {
		frame = m.pal.hue(areaChat)
		label = frame.Bold(true)
	}
	title := " › " + m.teammateName() + " "
	if c.id == "" {
		title = " › a new chat with " + m.teammateName() + " "
	}
	keys := " enter sends · ctrl+j new line "
	fill := max(w-4-lipgloss.Width(title)-lipgloss.Width(keys), 0)
	out := []string{frame.Render("╭─") + label.Render(title) + frame.Render(strings.Repeat("─", fill)) + styleDim.Render(keys) + frame.Render("─╮")}
	if lipgloss.Width(title)+lipgloss.Width(keys)+4 > w {
		out[0] = frame.Render("╭" + strings.Repeat("─", w-2) + "╮")
	}
	for _, l := range text {
		out = append(out, frame.Render("│")+" "+fitStyled(l, inner)+" "+frame.Render("│"))
	}
	return append(out, frame.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

// composerRows is how many rows the composer takes in the chat on show.
func (m *app) composerRows() int {
	return len(m.composer(m.chatWidth()))
}

// withComposer puts the composer's box under lines, at the bottom of h rows.
func withComposer(lines, box []string, h int) []string {
	if box == nil {
		return lines
	}
	for len(lines) < h-len(box) {
		lines = append(lines, "")
	}
	return append(lines[:min(len(lines), max(h-len(box), 0))], box...)
}
