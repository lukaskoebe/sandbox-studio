package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The computer pane is the web's computer screen: a row of tabs, the active one's rule heavy, and
// the tab under it. It shows beside a thread or a teammate's home from splitWidth, in place of
// them while tab flips to it, and alone for a computer opened on its own.

// computerTab is a tab of the computer pane, by its place in the web's row, which the digits
// pick, so the numbers don't move as tabs arrive.
type computerTab int

const (
	tabTerminal computerTab = iota
	tabFiles
	tabChanges
	tabBrowser
	tabDetails
)

// tabNames are the tabs' names, in the row's order.
var tabNames = [...]string{"Terminal", "Files", "Changes", "Browser", "Details"}

// computerState is the computer the pane shows and its tab.
type computerState struct {
	sandbox string
	tab     computerTab
	term    string            // the terminal the Terminal tab shows; "main" at first
	picked  map[string]string // a teammate's computer picked last, by teammate
}

// The keys of the computer pane.
var (
	keyTabs      = key.NewBinding(key.WithKeys("1", "5"), key.WithHelp("1-5", "tab"))
	keyComputer  = key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "next computer"))
	keyNewShell  = key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "new shell"))
	keyAgentHere = key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "start the agent"))
	keyFlip      = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "thread"))
)

// computerTabs are the tabs the pane has: the Terminal tab, but beside a thread that is an
// agent in a terminal, which shows that terminal itself, Changes and Details.
func (m *app) computerTabs() []computerTab {
	if m.here.kind == placeThread && m.here.term != "" {
		return []computerTab{tabChanges, tabDetails}
	}
	return []computerTab{tabTerminal, tabChanges, tabDetails}
}

// computerPane is the computer pane in w cells and h rows: the tab row, its rule and the tab.
func (m *app) computerPane(w, h int) []string {
	sb := m.computerOn()
	if sb == nil {
		return []string{styleDim.Render("That computer is gone.")}
	}
	row, rule := m.tabRow(*sb, w)
	lines := []string{row, rule}
	body := h - 2
	switch m.comp.tab {
	case tabTerminal:
		lines = append(lines, m.terminalTab(*sb, w, body)...)
	case tabChanges:
		lines = append(lines, m.changesTab(*sb, w, body)...)
	default:
		lines = append(lines, m.detailsTab(*sb, w, body)...)
	}
	return lines
}

// tabRow is the pane's row of tabs and the rule under it, heavy under the active tab: the
// accent's while a shell has the keys. On the right, the computer's status.
func (m *app) tabRow(sb sandboxes.View, w int) (string, string) {
	tabs := m.computerTabs()
	var b strings.Builder
	b.WriteString(" ")
	x0, x1 := 0, 0
	for i, t := range tabs {
		if i > 0 {
			b.WriteString("   ")
		}
		name := tabNames[t]
		if t == tabChanges {
			name = m.changesLabel()
		}
		if t == m.comp.tab {
			x0 = lipgloss.Width(b.String())
			x1 = x0 + lipgloss.Width(name)
			name = styleBold.Render(name)
		} else {
			name = styleDim.Render(name)
		}
		b.WriteString(name)
	}
	ph := phaseOf(sb)
	status := m.phaseDot(ph) + " " + styleDim.Render(strings.ToLower(ph.label())) + " "
	row := spread(b.String(), status, w)
	heavy := lipgloss.NewStyle()
	if m.paneFocused() {
		heavy = styleAccent
	}
	x1 = min(x1, w)
	x0 = min(x0, x1)
	rule := styleDim.Render(strings.Repeat("─", x0)) + heavy.Render(strings.Repeat("━", x1-x0)) + styleDim.Render(strings.Repeat("─", max(w-x1, 0)))
	return row, rule
}

// pickTab shows a tab by its digit, if the pane has it.
func (m *app) pickTab(n int) {
	t := computerTab(n - 1)
	if !slices.Contains(m.computerTabs(), t) {
		if n >= 1 && n <= len(tabNames) {
			m.setNotice(tabNames[t]+" comes to the terminal UI later. The browser has it.", false)
		}
		return
	}
	m.comp.tab = t
}

// computerKey answers the keys of the computer pane: a digit picks a tab, m shows the
// teammate's next computer, and the rest are the tab's.
func (m *app) computerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sb := m.computerOn()
	if sb == nil {
		return m, nil
	}
	if n := digit(msg); n > 0 && !strings.HasPrefix(msg.String(), "alt+") && n <= len(tabNames) {
		m.pickTab(n)
		return m, nil
	}
	if key.Matches(msg, keyComputer) {
		m.nextComputer()
		return m, nil
	}
	switch m.comp.tab {
	case tabTerminal:
		return m, m.terminalKey(msg, *sb)
	case tabChanges:
		return m, m.changesKey(msg)
	}
	return m, m.detailsKey(msg, *sb)
}

// computerBindings are the keys of the computer pane, the tab's first.
func (m *app) computerBindings() []key.Binding {
	sb := m.computerOn()
	if sb == nil {
		return nil
	}
	var bindings []key.Binding
	switch m.comp.tab {
	case tabTerminal:
		bindings = m.terminalBindings(*sb)
	case tabChanges:
		bindings = m.changesBindings()
	default:
		bindings = m.detailsBindings(*sb)
	}
	bindings = append(bindings, keyTabs)
	if len(m.sandboxesOf(sb.TeammateID)) > 1 && sb.TeammateID != "" {
		bindings = append(bindings, keyComputer)
	}
	if m.hasSide() {
		bindings = append(bindings, keyFlip)
	}
	return bindings
}
