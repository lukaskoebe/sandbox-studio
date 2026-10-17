package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The visual look's motion: a toast for the result of an action or an error, at the top right
// for a few seconds, and a new approval's card sliding in from the top. The plain look reports
// results on the notice line and draws new cards at once.

const (
	toastFor   = 3 * time.Second
	toastWidth = 48 // the most cells a toast takes, its frame included

	slideSteps = 5 // the frames of a card's slide-in
	slideEvery = 30 * time.Millisecond
)

// toastState is the toast on show: its text, whether it reports a failure, and the number that
// tells its expiry from an older one's.
type toastState struct {
	text   string
	failed bool
	id     int
	timed  bool // its expiry is on its way
}

// toastDone is a toast's time running out.
type toastDone struct{ id int }

// slideState is the cards sliding in: the approvals that arrived, and the frame of their slide.
type slideState struct {
	ids    map[string]bool
	step   int
	moving bool // a tick of the slide is on its way
}

// slideTick moves the slide-in on by a frame.
type slideTick struct{}

// report shows the result of an action, or an error: as a toast in the visual look, on the
// notice line in the plain one. A toast clears the notice line, which often still says what
// was being done.
func (m *app) report(text string, failed bool) {
	if m.look.plain {
		m.setNotice(text, failed)
		return
	}
	m.notice = notice{}
	m.toast = toastState{text: text, failed: failed, id: m.toast.id + 1}
}

// watchToast starts the clock of a new toast.
func (m *app) watchToast() tea.Cmd {
	if m.toast.text == "" || m.toast.timed {
		return nil
	}
	m.toast.timed = true
	id := m.toast.id
	return tea.Tick(toastFor, func(time.Time) tea.Msg { return toastDone{id} })
}

// slideIn starts the slide of the cards of approvals that just arrived.
func (m *app) slideIn(ids []string) {
	if m.look.plain || len(ids) == 0 {
		return
	}
	m.slide.ids = map[string]bool{}
	for _, id := range ids {
		m.slide.ids[id] = true
	}
	m.slide.step = 0
}

// watchSlide ticks the slide-in while cards move and the approvals are on show.
func (m *app) watchSlide() tea.Cmd {
	if len(m.slide.ids) == 0 || m.slide.moving {
		return nil
	}
	m.slide.moving = true
	return tea.Tick(slideEvery, func(time.Time) tea.Msg { return slideTick{} })
}

// motionUpdate applies the toast's and the slide's messages. ok is false for other messages.
func (m *app) motionUpdate(msg tea.Msg) (ok bool) {
	switch msg := msg.(type) {
	case toastDone:
		if msg.id == m.toast.id {
			m.toast = toastState{id: m.toast.id}
		}
		return true
	case slideTick:
		m.slide.moving = false
		if m.slide.step++; m.slide.step >= slideSteps-1 || m.screen != screenApprovals {
			m.slide = slideState{}
		}
		return true
	}
	return false
}

// sliding is how many of a card's n lines show while it slides in: its last ones, as if it came
// down from under the header. A card that isn't sliding shows them all.
func (m *app) sliding(c card, n int) int {
	for _, a := range c.approvals {
		if m.slide.ids[a.ID] {
			return max(n*(m.slide.step+1)/slideSteps, 1)
		}
	}
	return n
}

// toastBox is the toast on show as the lines of a box at most toastWidth cells wide, or nil.
func (m *app) toastBox(w int) []string {
	if m.toast.text == "" || m.look.plain {
		return nil
	}
	inner := min(toastWidth, w-2) - 4
	if inner < 10 {
		return nil
	}
	frame, mark := m.pal.running, m.pal.running.Render("✓")
	if m.toast.failed {
		frame, mark = m.pal.failed, m.pal.failed.Render("✕")
	}
	text := lipgloss.NewStyle().Width(inner - 2).Render(m.toast.text)
	lines := strings.Split(text, "\n")
	if len(lines) > 3 {
		lines = append(lines[:2], clip(strings.Join(lines[2:], " "), inner-2))
	}
	width := 0
	for _, l := range lines {
		width = max(width, lipgloss.Width(strings.TrimRight(l, " ")))
	}
	out := []string{frame.Render("╭" + strings.Repeat("─", width+4) + "╮")}
	for i, l := range lines {
		lead := "  "
		if i == 0 {
			lead = mark + " "
		}
		out = append(out, frame.Render("│")+" "+lead+fitStyled(strings.TrimRight(l, " "), width)+" "+frame.Render("│"))
	}
	return append(out, frame.Render("╰"+strings.Repeat("─", width+4)+"╯"))
}

// overlay draws box over the right end of lines, from line top down, keeping one cell of
// margin to the terminal's edge.
func overlay(lines, box []string, top, w int) {
	for i, b := range box {
		y := top + i
		if y >= len(lines) {
			return
		}
		bw := lipgloss.Width(b)
		left := fitStyled(lines[y], max(w-bw-2, 0))
		lines[y] = left + " " + b
	}
}
