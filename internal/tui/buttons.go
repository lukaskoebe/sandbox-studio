package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Buttons: the choices of a card, of the banner over a session, of a chat's question and of the
// confirmations are a row of controls that ← and → move over and enter or space press, as well
// as the letter each one has. A toggle, such as a card's pattern, is a control too: pressing it
// steps it. Nothing is focused until an arrow is pressed, so enter keeps its meaning until then.

var (
	keyLeft  = key.NewBinding(key.WithKeys("left"), key.WithHelp("←/→", "choose"))
	keyRight = key.NewBinding(key.WithKeys("right"))
	keyPress = key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("space", "press"))
)

// buttonFocus is the control the arrows are on: the at-th control of owner, which names the row
// of controls, such as a card by its approval. A focus whose owner is not on show is no focus.
type buttonFocus struct {
	owner string
	at    int
}

// buttonAt is the focused control of owner's row, or -1.
func (m *app) buttonAt(owner string) int {
	if m.buttons.owner != owner || owner == "" {
		return -1
	}
	return m.buttons.at
}

// focusButton focuses the at-th control of owner's row.
func (m *app) focusButton(owner string, at int) {
	m.buttons = buttonFocus{owner: owner, at: at}
}

// blurButtons lets go of the focus.
func (m *app) blurButtons() {
	m.buttons = buttonFocus{}
}

// buttonKey answers the arrows and the press of owner's controls. The first arrow focuses the
// first-th control, a later one the next or the previous; enter or space then presses the one
// focused, and press is its key. handled is false when the key is not for the controls.
func (m *app) buttonKey(msg tea.KeyPressMsg, owner string, controls []choiceCap, first int) (press string, handled bool) {
	if len(controls) == 0 || owner == "" {
		return "", false
	}
	at := m.buttonAt(owner)
	switch {
	case key.Matches(msg, keyLeft), key.Matches(msg, keyRight):
		dir := 1
		if key.Matches(msg, keyLeft) {
			dir = -1
		}
		switch {
		case at < 0:
			at = min(max(first, 0), len(controls)-1)
		default:
			at = min(max(at+dir, 0), len(controls)-1)
		}
		m.focusButton(owner, at)
		return "", true
	case key.Matches(msg, keyPress) && at >= 0 && at < len(controls):
		return controls[at].key, true
	}
	return "", false
}

// ansiStrip is text without its styles, to draw it in another.
func ansiStrip(s string) string { return ansi.Strip(s) }

// focusStyle is a focused control: the accent, reversed.
var focusStyle = styleAccent.Reverse(true).Bold(true)

// focused draws text as the control the arrows are on. Without styles, as in the plain look
// under NO_COLOR, brackets say so.
func (m *app) focused(text string) string {
	if m.look.plain {
		return styleAccent.Render("[" + text + "]")
	}
	return focusStyle.Render(" " + text + " ")
}

// buttonsLine lays controls out on one line: the visual look's keycaps, or the plain look's keys
// and words, with the focused one, at, drawn as such. It says where each control sits.
func (m *app) buttonsLine(caps []choiceCap, at int) (string, []keySpan) {
	if !m.look.plain {
		return m.pal.capsLine(caps, at, m.focused)
	}
	parts := make([]string, 0, len(caps))
	spans := make([]keySpan, 0, len(caps))
	x := 0
	for i, c := range caps {
		part := styleBold.Render(c.key) + " " + c.label
		if i == at {
			part = m.focused(c.key + " " + c.label)
		}
		spans = append(spans, keySpan{x0: x, x1: x + lipgloss.Width(part), key: c.key})
		x += lipgloss.Width(part) + 2
		parts = append(parts, part)
	}
	return strings.Join(parts, "  "), spans
}

// resumeState is where a decision made away from a session returns to: the approval it waits
// for, and the pane that had the keys or the terminal that was attached. When the approval is
// no longer pending, the TUI goes back there.
type resumeState struct {
	approval string
	back     screen        // the screen to return to
	pane     bool          // Home's pane had the keys
	attach   *attachTarget // the terminal to attach again
}

// attachTarget is a terminal to attach: its sandbox, by ID and name, and its own name.
type attachTarget struct {
	id, sandbox, name string
}

// resume goes back to what a decision interrupted, once its approval is decided: the pane gets
// the keys again, or the terminal is attached again. A resume whose approval still waits stays.
func (m *app) resume() tea.Cmd {
	r := m.resuming
	if r == nil || m.known[r.approval] {
		return nil
	}
	m.resuming = nil
	m.blurButtons()
	switch {
	case r.attach != nil:
		m.screen = screenHome
		for i := range m.sandboxes {
			if m.sandboxes[i].ID == r.attach.id {
				return m.attachTo(&m.sandboxes[i], r.attach.name)
			}
		}
	default:
		m.screen = r.back
		if _, ok := m.paneTarget(); ok && r.pane {
			m.pane.focus = true
		}
	}
	return nil
}

// cardIndex is the index of the card that holds the approval, or -1.
func (m *app) cardIndex(id string) int {
	for i, c := range m.cards() {
		for _, a := range c.approvals {
			if a.ID == id {
				return i
			}
		}
	}
	return -1
}

// openApprovalsOn shows the approvals with the cursor on the card of the approval, and returns
// to r once it is decided.
func (m *app) openApprovalsOn(id string, r *resumeState) {
	m.openApprovals()
	if i := m.cardIndex(id); i >= 0 {
		m.approvalCursor = i
	}
	m.resuming = r
}

// A confirmation's choices: y does it, n keeps things as they are. The arrows start on keep, so
// enter keeps too.
func confirmCaps(verb string) []choiceCap {
	return []choiceCap{{"y", verb}, {"n", "Keep"}}
}

// confirmButtons answers the arrows and the press of a confirmation's choices.
func (m *app) confirmButtons(msg tea.KeyPressMsg, owner string) (press string, handled bool) {
	if m.buttonAt(owner) < 0 {
		m.focusButton(owner, 1)
	}
	return m.buttonKey(msg, owner, confirmCaps(""), 1)
}

// confirmRow is a confirmation's choices on the notice line, keep focused until the arrows move.
func (m *app) confirmRow(owner, verb string) string {
	at := m.buttonAt(owner)
	if at < 0 {
		at = 1
	}
	row, _ := m.buttonsLine(confirmCaps(verb), at)
	return row
}
