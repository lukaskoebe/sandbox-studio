package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// The rows of Home's list that are not a teammate's or a computer's: ENVIRONMENT's places, which
// enter opens, and the + rows, which make something new.

// openPlace opens an ENVIRONMENT row: the Computers screen, Activity (narrowed to the network
// for Network), Settings, or the address of a page that lives in the browser.
func (m *app) openPlace(p envPlace) tea.Cmd {
	switch p {
	case placeComputers:
		m.openComputers()
	case placeNetwork:
		cmd := m.openActivity("")
		m.activity.kind = activityKindOf("network")
		return cmd
	case placeSecrets:
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/secrets"), false)
	case placeMemory:
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/memory"), false)
	case placeActivity:
		return m.openActivity("")
	case placeSettings:
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

// placePane is Home's pane for a place or a + row, in w cells and h rows: what the row is, and
// what enter does.
func (m *app) placePane(w, h int) []string {
	title, words := "", []string{}
	var extra []string
	if m.at == atAction {
		switch m.rowAction {
		case actionNewTeammate:
			title = "New teammate"
			words = []string{"A teammate needs a provider, a model and a soul, which the browser asks for. enter prints its address."}
		case actionNewComputer:
			title = "New computer"
			words = []string{"enter starts a new computer: its teammate, a template and a name."}
		}
	} else {
		title = placeName(m.rowPlace)
		switch m.rowPlace {
		case placeComputers:
			title += styleDim.Render(fmt.Sprintf(" · %d", len(m.sandboxes)))
			words = []string{"Every computer of the environment, yours and the teammates'. enter lists them, with the keys to start, stop, fork and delete one."}
			for _, sb := range m.sandboxes {
				extra = append(extra, m.groupHeader(sb, false, 0, w))
			}
		case placeNetwork:
			cards := m.networkCards()
			words = []string{"What the computers ask to reach, and what Studio allowed and denied. enter opens Activity on the network."}
			if len(cards) == 0 {
				extra = append(extra, styleDim.Render("Nothing waits on the network."))
			}
			for _, c := range cards {
				extra = append(extra, clip(cardLine(c), w))
			}
		case placeSecrets:
			words = []string{"The secrets the environment hands its computers live in the browser. enter prints the address."}
		case placeMemory:
			words = []string{"What the teammates remember, and what waits for you to confirm, lives in the browser. enter prints the address."}
		case placeActivity:
			words = []string{"The connections and decisions of the environment, newest first. enter opens it."}
		case placeSettings:
			title += styleDim.Render(" · " + version.Version)
			words = []string{"Studio's settings, as the web's sections. enter opens them."}
		}
	}
	lines := []string{styleBold.Render(title), styleDim.Render(strings.Repeat("─", w))}
	for _, text := range words {
		lines = append(lines, wrapText(text, w)...)
	}
	if len(extra) > 0 {
		lines = append(lines, "")
		lines = append(lines, extra...)
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}
