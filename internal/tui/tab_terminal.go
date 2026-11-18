package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The Terminal tab is a line of the computer's terminals as chips, the one on show bold and an
// agent that waits for you marked ▲, and that terminal as it runs under it. enter gives it the
// keys, alt+1 to alt+9 show another, + opens a new shell and T starts the teammate's agent. A
// thread that is an agent in a terminal draws the same, its chips over a rule.

// terminalChips are the names of a computer's terminals in the guest's order, with on among
// them; main when it has none yet, which the attach starts.
func (m *app) terminalChips(sb sandboxes.View, on string) []string {
	var names []string
	for _, t := range m.sess.terms[sb.ID] {
		names = append(names, t.Name)
	}
	if on != "" && !slices.Contains(names, on) {
		names = append(names, on)
	}
	if len(names) == 0 {
		names = []string{terminalName}
	}
	return names
}

// freshShell is the name + gives a new shell, as the web's + does
// (web/src/components/computer/terminals.tsx): main when it is free, else the smallest number
// from 2 that is.
func freshShell(names []string) string {
	if !slices.Contains(names, terminalName) {
		return terminalName
	}
	for n := 2; ; n++ {
		if s := strconv.Itoa(n); !slices.Contains(names, s) {
			return s
		}
	}
}

// chipsLine is the line of a computer's terminals, on the one on show, and the terminal's size
// on the right while it is connected.
func (m *app) chipsLine(sb sandboxes.View, on string, w int) string {
	sessions := map[string]session{}
	for _, x := range m.sessionsOf(sb) {
		sessions[x.term.Name] = x
	}
	parts := []string{}
	for _, name := range m.terminalChips(sb, on) {
		label := name
		if x, ok := sessions[name]; ok {
			label = x.title()
			if m.stateOf(x) == stateBlocked && x.kind == sessionAgent {
				label += " " + m.waitingMark("▲")
			}
		}
		if name == on {
			label = styleBold.Render(label)
		} else {
			label = styleDim.Render(label)
		}
		parts = append(parts, label)
	}
	parts = append(parts, styleDim.Render("+ new shell"))
	size := ""
	if p := &m.pane; p.term != nil && p.key == m.termSession(sb.ID, on).id() {
		size = styleDim.Render(fmt.Sprintf("%d×%d", m.paneW(), m.termRows())) + " "
	}
	return spread(" "+strings.Join(parts, "  "), size, w)
}

// terminalTab is the Terminal tab in w cells and h rows: the chips, and the terminal on show.
func (m *app) terminalTab(sb sandboxes.View, w, h int) []string {
	lines := []string{m.chipsLine(sb, m.comp.term, w)}
	return append(lines, m.termScreen(m.termSession(sb.ID, m.comp.term), sb, w, max(h-1, 0))...)
}

// threadTerminal is a thread that is an agent in a terminal, in w cells and h rows: the chips of
// its computer over a rule, the accent's while it has the keys, and the terminal.
func (m *app) threadTerminal(w, h int) []string {
	sb, ok := m.sandboxByID(m.here.sandbox)
	if !ok {
		return []string{styleDim.Render("That computer is gone.")}
	}
	lines := []string{m.chipsLine(sb, m.here.term, w), m.paneRule(w)}
	return append(lines, m.termScreen(m.termSession(sb.ID, m.here.term), sb, w, max(h-2, 0))...)
}

// pickChip shows the n-th of the computer's terminals, in the thread or on the Terminal tab,
// still with the keys if the last one had them.
func (m *app) pickChip(n int) tea.Cmd {
	sb := m.computerOn()
	if m.threadTermShows() {
		if s, ok := m.sandboxByID(m.here.sandbox); ok {
			sb = &s
		}
	}
	if sb == nil {
		return nil
	}
	on := m.comp.term
	if m.threadTermShows() {
		on = m.here.term
	}
	names := m.terminalChips(*sb, on)
	if n < 1 || n > len(names) {
		return nil
	}
	name := names[n-1]
	if m.threadTermShows() {
		m.here.term = name
	} else {
		m.comp.term = name
	}
	if m.pane.focus {
		return m.focusPane()
	}
	return nil
}

// newShell opens a new shell on the Terminal tab, with the keys.
func (m *app) newShell(sb sandboxes.View) tea.Cmd {
	if phaseOf(sb) != phaseReady {
		m.setNotice("Start "+sb.Name+" first: Details, then s.", false)
		return nil
	}
	m.comp.term = freshShell(m.terminalChips(sb, m.comp.term))
	return m.focusPane()
}

// terminalKey answers the keys of the Terminal tab, and of a thread that is a terminal, while
// Studio has them: the banner's first.
func (m *app) terminalKey(msg tea.KeyPressMsg, sb sandboxes.View) tea.Cmd {
	if cmd, ok := m.bannerKey(msg); ok {
		return cmd
	}
	switch {
	case key.Matches(msg, keyFocus):
		if phaseOf(sb) != phaseReady {
			m.setNotice("Start "+sb.Name+" first: Details, then s.", false)
			return nil
		}
		return m.focusPane()
	case key.Matches(msg, keySwitch):
		return m.pickChip(int(msg.Code - '0'))
	case key.Matches(msg, keyNewShell) && !m.threadTermShows():
		return m.newShell(sb)
	case key.Matches(msg, keyAgentHere):
		return m.startAgent(&sb)
	case key.Matches(msg, keyZoom):
		m.zoom = !m.zoom
	}
	return nil
}

// terminalBindings are the keys of the Terminal tab while Studio has them: the banner's while
// its choices have the arrows.
func (m *app) terminalBindings(sb sandboxes.View) []key.Binding {
	if b := m.bannerBindings(); b != nil {
		return b
	}
	bindings := []key.Binding{keyFocus}
	if x, ok := m.paneTarget(); ok {
		if c, waiting := m.paneCard(x); waiting && bannerKeys(c) {
			bindings = append(bindings, keyAllow, keyOnce, keyDeny, keyMore)
		} else if waiting {
			bindings = append(bindings, keyReviewIt)
		}
	}
	bindings = append(bindings, keySwitch)
	if !m.threadTermShows() {
		bindings = append(bindings, keyNewShell)
	}
	if sb.TeammateID != "" {
		bindings = append(bindings, keyAgentHere)
	}
	return append(bindings, keyZoom)
}
