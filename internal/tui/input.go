package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
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

// linePrompt is a line of text the notice line asks for: a checkpoint's name, a fork's name, or
// the answer to a chat's question. Enter submits it, esc drops it.
type linePrompt struct {
	label   string      // what is asked, before the text
	text    input       // what the user typed
	enter   key.Binding // enter, with the word the key bar shows for it
	name    bool        // the text is a sandbox name: lowercase, with no spaces
	problem string      // why the last submit was refused; it goes with the next key
	// submit takes the text. It returns a problem to keep the prompt open with, or the command
	// that goes on with the text.
	submit func(text string) (problem string, cmd tea.Cmd)
}

// typingKey answers the prompt of the notice line: enter submits it, esc drops it, and the other
// keys edit the text.
func (m *app) typingKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.typing
	switch {
	case msg.String() == "enter":
		problem, cmd := p.submit(p.text.value())
		if problem != "" {
			p.problem = problem
			return m, nil
		}
		if m.typing == p {
			m.typing = nil
		}
		return m, cmd
	case key.Matches(msg, keyEsc):
		m.typing = nil
	default:
		p.problem = ""
		if p.text.key(msg) && p.name {
			p.text.set(strings.ToLower(strings.ReplaceAll(p.text.value(), " ", "")))
		}
	}
	return m, nil
}

// view is the prompt on the notice line: its question, the text, and a refused submit's problem.
func (p *linePrompt) view(w int) string {
	line := p.text.line(p.label, w)
	if p.problem != "" {
		line += "  " + styleFailed.Render(p.problem)
	}
	return line
}

// ask opens a prompt on the notice line.
func (m *app) ask(p *linePrompt) {
	m.typing = p
	m.setNotice("", false)
}
