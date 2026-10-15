package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// input is a line of text the user types: a chat prompt, or the name of a new sandbox. Text goes
// in at the end, as in the filter. Pasted text keeps its line breaks.
type input struct {
	text []rune
}

// key applies a key press that edits the text, and reports whether it did.
func (in *input) key(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "backspace":
		if len(in.text) > 0 {
			in.text = in.text[:len(in.text)-1]
		}
		return true
	case "ctrl+u":
		in.text = nil
		return true
	case "ctrl+w":
		s := strings.TrimRight(string(in.text), " ")
		if i := strings.LastIndex(s, " "); i >= 0 {
			s = s[:i+1]
		} else {
			s = ""
		}
		in.text = []rune(s)
		return true
	}
	if msg.Text != "" {
		in.text = append(in.text, []rune(msg.Text)...)
		return true
	}
	return false
}

// paste adds pasted text.
func (in *input) paste(s string) {
	in.text = append(in.text, []rune(strings.ReplaceAll(s, "\r\n", "\n"))...)
}

func (in *input) value() string { return string(in.text) }

func (in *input) set(s string) { in.text = []rune(s) }

// line is the text on one line of w cells, its end in view, with the cursor after it. Line
// breaks show as ⏎.
func (in *input) line(prefix string, w int) string {
	text := strings.ReplaceAll(in.value(), "\n", "⏎")
	room := max(w-len([]rune(prefix))-1, 1)
	if runes := []rune(text); len(runes) > room {
		text = "…" + string(runes[len(runes)-room+1:])
	}
	return styleBold.Render(prefix) + text + styleAccent.Render("▏")
}
