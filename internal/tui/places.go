package tui

import (
	tea "charm.land/bubbletea/v2"
)

// The rows of the sidebar that are not a teammate's or a computer's: ENVIRONMENT's places, which
// enter opens, and the + rows, which make something new.

// openPlace opens an ENVIRONMENT row: the Computers screen, Activity (narrowed to the network
// for Network), Settings, or the address of a page that lives in the browser.
func (m *app) openPlace(p envPlace) tea.Cmd {
	switch p {
	case envComputers:
		m.openComputers()
	case envNetwork:
		cmd := m.openActivity("")
		m.activity.kind = activityKindOf("network")
		return cmd
	case envSecrets:
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/secrets"), false)
	case envMemory:
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/memory"), false)
	case envActivity:
		return m.openActivity("")
	case envSettings:
		return m.openSettings()
	}
	return nil
}

// activityKindOf is the index of a kind among activityKinds.
func activityKindOf(kind string) int {
	for i, k := range activityKinds {
		if k == kind {
			return i
		}
	}
	return 0
}

// rowActionDo answers enter on a + row: a new computer starts its form; a new teammate needs a
// provider, a model and a soul, which the browser asks for.
func (m *app) rowActionDo(a rowAction) tea.Cmd {
	if m.env.ID == "" {
		return nil
	}
	if a == actionNewComputer {
		return m.openNew()
	}
	m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/teammates"), false)
	return nil
}
