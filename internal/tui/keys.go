package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The keys. They are the same on every screen, and none of them is configurable. The key bar
// shows the ones that apply where the user is.
var (
	keyUp        = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	keyDown      = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	keyOpen      = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open"))
	keyChoose    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))
	keyKeep      = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "keep"))
	keyClear     = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear"))
	keyEsc       = key.NewBinding(key.WithKeys("esc"))
	keyBack      = key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "back"))
	keyCancel    = key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc", "cancel"))
	keyQuit      = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	keyHelp      = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help"))
	keyFilter    = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	keyEnv       = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "env"))
	keyNew       = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new"))
	keyLifecycle = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start/stop"))
	keyDelete    = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	keyChat      = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "chat"))
	keyYes       = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "delete"))
	keyNo        = key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n", "keep"))
	keyInterrupt = key.NewBinding(key.WithKeys("ctrl+c"))
	keyApprovals = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "approvals"))
	keyTerminal  = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "terminal"))
	keyActivity  = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "activity"))
	keyAllow     = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "allow"))
	keyDeny      = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "deny"))
	keyOnce      = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "allow once"))
	keyOnceDeny  = key.NewBinding(key.WithKeys("O"), key.WithHelp("O", "deny once"))
	keyHour      = key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "allow 1 hour"))
	keyIgnore    = key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "ignore"))
	keyPattern   = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pattern"))
	keyPort      = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "any port"))
	keyScope     = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "scope"))
	keySite      = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "allow site"))
	keyAllowAll  = key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "allow all"))
	keyDenyAll   = key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "deny all"))
	keySplit     = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "review one by one"))
)

// key routes a key press to the mode and screen the user is in.
func (m *app) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keyInterrupt) {
		return m, tea.Quit
	}
	switch {
	case m.filtering:
		return m.filterKey(msg)
	case m.confirm != nil:
		return m.confirmKey(msg)
	}
	switch m.screen {
	case screenHome:
		return m.homeKey(msg)
	case screenDetail:
		return m.detailKey(msg)
	case screenEnvs:
		return m.envsKey(msg)
	case screenApprovals:
		return m.approvalsKey(msg)
	default: // help: any key closes it
		m.screen = m.back
		return m, nil
	}
}

func (m *app) homeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyQuit):
		return m, tea.Quit
	case key.Matches(msg, keyUp):
		m.cursor = max(m.cursor-1, 0)
	case key.Matches(msg, keyDown):
		m.cursor = min(m.cursor+1, max(len(m.visible())-1, 0))
	case key.Matches(msg, keyOpen):
		if m.selected() != nil {
			m.screen = screenDetail
		}
	case key.Matches(msg, keyNew):
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID), false)
	case key.Matches(msg, keyLifecycle):
		return m, m.toggle(m.selected())
	case key.Matches(msg, keyDelete):
		m.askDelete(m.selected())
	case key.Matches(msg, keyChat):
		m.openChat(m.selected())
	case key.Matches(msg, keyEnv):
		m.openEnvs()
	case key.Matches(msg, keyFilter):
		m.startFilter()
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyTerminal):
		return m, m.attachTo(m.selected())
	case key.Matches(msg, keyActivity):
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/network/activity"), false)
	case key.Matches(msg, keyEsc):
		if m.filter != "" {
			m.filter = ""
			m.reselect(m.selectedID())
		}
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

func (m *app) detailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyBack):
		m.screen = screenHome
	case key.Matches(msg, keyLifecycle):
		return m, m.toggle(m.selected())
	case key.Matches(msg, keyDelete):
		m.askDelete(m.selected())
	case key.Matches(msg, keyChat):
		m.openChat(m.selected())
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyTerminal):
		return m, m.attachTo(m.selected())
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

func (m *app) envsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyCancel):
		m.screen = screenHome
	case key.Matches(msg, keyUp):
		m.picker = max(m.picker-1, 0)
	case key.Matches(msg, keyDown):
		m.picker = min(m.picker+1, max(len(m.envs)-1, 0))
	case key.Matches(msg, keyChoose):
		m.screen = screenHome
		if m.picker < len(m.envs) && m.envs[m.picker].ID != m.env.ID {
			m.env = m.envs[m.picker]
			m.sandboxes, m.loaded, m.cursor, m.filter = nil, false, 0, ""
			return m, m.loadSandboxes()
		}
	}
	return m, nil
}

// filterKey types into the filter. Enter keeps the filter, esc clears it.
func (m *app) filterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	id := m.selectedID()
	switch {
	case key.Matches(msg, keyKeep):
		m.filtering = false
	case key.Matches(msg, keyEsc):
		m.filter, m.filtering = "", false
	case msg.String() == "backspace":
		if runes := []rune(m.filter); len(runes) > 0 {
			m.filter = string(runes[:len(runes)-1])
		}
	case msg.Text != "":
		m.filter += msg.Text
	}
	m.reselect(id)
	return m, nil
}

// confirmKey answers a pending delete: y deletes the sandbox, any other key keeps it.
func (m *app) confirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sb := *m.confirm
	m.confirm = nil
	if key.Matches(msg, keyYes) {
		m.setNotice("Deleting "+sb.Name+"…", false)
		return m, m.remove(sb)
	}
	m.setNotice("Kept "+sb.Name, false)
	return m, nil
}

// toggle starts a stopped sandbox, stops a running one and resumes a suspended one. A sandbox
// that is still starting, or that failed, has nothing to toggle.
func (m *app) toggle(sb *sandboxes.View) tea.Cmd {
	if sb == nil {
		return nil
	}
	switch phaseOf(*sb) {
	case phaseReady:
		m.setNotice("Stopping "+sb.Name+"…", false)
		return m.lifecycle(*sb, "stop", "stop")
	case phaseStopped:
		m.setNotice("Starting "+sb.Name+"…", false)
		return m.lifecycle(*sb, "start", "start")
	case phaseSuspended:
		m.setNotice("Resuming "+sb.Name+"…", false)
		return m.lifecycle(*sb, "resume", "resume")
	case phaseFailed:
		m.setNotice(sb.Name+" could not be created. Delete it with d.", true)
	default:
		m.setNotice(sb.Name+" is still starting.", false)
	}
	return nil
}

func (m *app) askDelete(sb *sandboxes.View) {
	if sb == nil {
		return
	}
	m.confirm = sb
}

func (m *app) openChat(sb *sandboxes.View) {
	if sb == nil {
		return
	}
	m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/sandboxes/"+sb.ID+"/work"), false)
}

func (m *app) openEnvs() {
	m.screen = screenEnvs
	m.picker = 0
	for i, env := range m.envs {
		if env.ID == m.env.ID {
			m.picker = i
		}
	}
}

func (m *app) openHelp() {
	m.back = m.screen
	m.screen = screenHelp
}

func (m *app) startFilter() {
	m.filtering = true
}

// selectedID is the ID of the selected sandbox, or "" when none is visible.
func (m *app) selectedID() string {
	if sb := m.selected(); sb != nil {
		return sb.ID
	}
	return ""
}

// bindings are the keys the key bar shows for the current mode and screen.
func (m *app) bindings() []key.Binding {
	switch {
	case m.filtering:
		return []key.Binding{keyKeep, keyClear}
	case m.confirm != nil:
		return []key.Binding{keyYes, keyNo}
	}
	switch m.screen {
	case screenDetail:
		return []key.Binding{keyTerminal, keyLifecycle, keyDelete, keyChat, keyBack, keyHelp}
	case screenEnvs:
		return []key.Binding{keyUp, keyChoose, keyCancel}
	case screenHelp:
		return []key.Binding{keyBack}
	case screenApprovals:
		return m.approvalBindings()
	}
	return []key.Binding{keyOpen, keyApprovals, keyTerminal, keyLifecycle, keyDelete, keyHelp, keyQuit}
}

// approvalBindings are the keys the key bar shows for the approvals card under the cursor.
func (m *app) approvalBindings() []key.Binding {
	cards := m.cards()
	if m.approvalCursor >= len(cards) || len(cards) == 0 {
		return []key.Binding{keyBack}
	}
	c := cards[m.approvalCursor]
	switch {
	case c.group:
		return []key.Binding{keyAllowAll, keyDenyAll, keySplit, keyScope, keyBack}
	case isNetwork(c.first()):
		return []key.Binding{keyAllow, keyDeny, keyOnce, keyHour, keyPattern, keyScope, keyBack}
	}
	return []key.Binding{keyOpen, keyBack}
}
