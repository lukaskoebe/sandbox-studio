package tui

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The Details tab is what the computer is and does: its status, teammate, template, resources,
// uptime and ports, and from 20 rows in the visual look the dashboard's charts, history and
// network. Its keys start, stop, suspend, fork and delete the computer, and open its checkpoints
// and Activity.

// keyShellTab goes to the Terminal tab with the shell's keys.
var keyShellTab = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "terminal"))

// detailsTab is the Details tab in w cells and h rows.
func (m *app) detailsTab(sb sandboxes.View, w, h int) []string {
	if !m.look.plain && h >= 20 {
		return m.dashboard(sb, w, h)
	}
	return m.detailBody(sb, w)
}

// detailsKey answers the keys of the Details tab.
func (m *app) detailsKey(msg tea.KeyPressMsg, sb sandboxes.View) tea.Cmd {
	switch {
	case key.Matches(msg, keyLifecycle):
		return m.toggle(&sb)
	case key.Matches(msg, keySuspend):
		return m.suspend(&sb)
	case key.Matches(msg, keyFork):
		m.askFork(&sb)
	case key.Matches(msg, keyCheckpoints):
		return m.openCheckpoints(&sb)
	case key.Matches(msg, keyDelete):
		m.askDelete(&sb)
	case key.Matches(msg, keyActivity):
		return m.openActivity(sb.Name)
	case key.Matches(msg, keyShellTab):
		return m.shellTab()
	case key.Matches(msg, keyAgentHere):
		return m.startAgent(&sb)
	}
	return nil
}

// shellTab shows the Terminal tab with the shell's keys, or the thread's terminal when the
// thread is one.
func (m *app) shellTab() tea.Cmd {
	if m.here.kind == placeThread && m.here.term != "" {
		m.flip, m.focus = false, focusMain
		return m.focusPane()
	}
	if !slices.Contains(m.computerTabs(), tabTerminal) {
		return nil
	}
	m.comp.tab = tabTerminal
	if m.hasSide() {
		if m.layoutFor(m.width).side > 0 {
			m.focus = focusSide
		} else {
			m.flip = true
		}
	}
	if sb := m.computerOn(); sb != nil && phaseOf(*sb) != phaseReady {
		m.setNotice("Start "+sb.Name+" first: Details, then s.", false)
		return nil
	}
	return m.focusPane()
}

// detailsBindings are the keys of the Details tab: suspend only while it runs.
func (m *app) detailsBindings(sb sandboxes.View) []key.Binding {
	bindings := []key.Binding{keyLifecycle}
	if phaseOf(sb) == phaseReady {
		bindings = append(bindings, keySuspend)
	}
	bindings = append(bindings, keyShellTab, keyDelete, keyActivity, keyFork, keyCheckpoints)
	if sb.TeammateID != "" {
		bindings = append(bindings, keyAgentHere)
	}
	return bindings
}
