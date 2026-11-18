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
	keySearch    = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search"))
	keyEnv       = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "env"))
	keyLifecycle = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start/stop"))
	keySuspend   = key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "suspend"))
	keyDelete    = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	keyWaiting   = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next waiting"))
	keyYes       = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "delete"))
	keyNo        = key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n", "keep"))
	keyInterrupt = key.NewBinding(key.WithKeys("ctrl+c"))
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

// key routes a key press to the mode and screen the user is in: a prompt or a confirmation
// first, then a shell with the keys (ctrl+c too: it interrupts the agent there, not the TUI),
// the composer, the full screens, and in the frame the part of it that has the cursor.
func (m *app) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.search.open {
		return m.searchKey(msg)
	}
	if !m.filtering && m.typing == nil && m.confirm == nil && m.paneFocused() {
		return m.paneKey(msg)
	}
	if key.Matches(msg, keyInterrupt) {
		return m, tea.Quit
	}
	// ctrl+k opens Search Studio from anywhere but a field and a shell with the keys; / from the
	// screens without a filter of their own.
	if !m.filtering && m.typing == nil && m.confirm == nil && m.checkpoints.restoring == nil {
		if key.Matches(msg, keySearchAny) ||
			key.Matches(msg, keySearch) && (m.screen == screenSettings || m.screen == screenComputers) {
			return m, m.openSearch("")
		}
	}
	switch {
	case m.filtering:
		return m.filterKey(msg)
	case m.typing != nil:
		return m.typingKey(msg)
	case m.confirm != nil:
		return m.confirmKey(msg)
	case m.checkpoints.restoring != nil && m.screen == screenCheckpoints:
		return m.restoreKey(msg)
	}
	switch m.screen {
	case screenMain:
		if m.composerKeys() {
			return m.composeKey(msg)
		}
		return m.mainKey(msg)
	case screenEnvs:
		return m.envsKey(msg)
	case screenActivity:
		return m.activityKey(msg)
	case screenEntry:
		return m.entryKey(msg)
	case screenNew:
		return m.newKey(msg)
	case screenProgress:
		return m.progressKey(msg)
	case screenCheckpoints:
		return m.checkpointsKey(msg)
	case screenComputers:
		return m.computersKey(msg)
	case screenSettings:
		return m.settingsKey(msg)
	default: // help: any key closes it
		m.screen = m.helpBack
		return m, nil
	}
}

// composerKeys reports whether the composer has the keys: it is open on the thread or the
// teammate's home in the main column, which has the cursor.
func (m *app) composerKeys() bool {
	c := &m.chats
	if !c.on || !c.composing || c.reading || m.sidebarKeys() || m.focus != focusMain {
		return false
	}
	k := m.mainPane()
	return k == paneThread || k == paneMateHome
}

func (m *app) envsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyCancel):
		m.screen = screenMain
	case key.Matches(msg, keyUp):
		m.picker = max(m.picker-1, 0)
	case key.Matches(msg, keyDown):
		m.picker = min(m.picker+1, max(len(m.envs)-1, 0))
	case key.Matches(msg, keyChoose):
		m.screen = screenMain
		if m.picker < len(m.envs) && m.envs[m.picker].ID != m.env.ID {
			m.env = m.envs[m.picker]
			m.sandboxes, m.loaded, m.cursor, m.sub = nil, false, 0, -1
			m.at, m.mate = atHome, ""
			m.stopChat()
			m.goHome()
			return m, m.loadSandboxes()
		}
	}
	return m, nil
}

// filterKey types into Activity's filter by computer. Enter keeps the filter, esc clears it.
func (m *app) filterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	target := &m.activity.filter
	switch {
	case key.Matches(msg, keyKeep):
		m.filtering = false
	case key.Matches(msg, keyEsc):
		*target, m.filtering = "", false
	case msg.String() == "backspace":
		if runes := []rune(*target); len(runes) > 0 {
			*target = string(runes[:len(runes)-1])
		}
	case msg.Text != "":
		*target += msg.Text
	}
	m.activity.cursor = 0
	return m, nil
}

// confirmKey answers a pending delete: y deletes the sandbox, any other key keeps it.
func (m *app) confirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sb := *m.confirm
	if k, ok := m.confirmButtons(msg, "delete:"+sb.ID); ok {
		if k == "" {
			return m, nil
		}
		msg = pressOf(k)
	}
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

// suspend suspends a running sandbox: its memory is saved, and s resumes it where it was.
func (m *app) suspend(sb *sandboxes.View) tea.Cmd {
	if sb == nil {
		return nil
	}
	switch phaseOf(*sb) {
	case phaseReady:
		m.setNotice("Suspending "+sb.Name+"…", false)
		return m.lifecycle(*sb, "suspend", "suspend")
	case phaseSuspended:
		m.setNotice(sb.Name+" is suspended already. s resumes it.", false)
	default:
		m.setNotice("Only a running computer can be suspended.", false)
	}
	return nil
}

func (m *app) askDelete(sb *sandboxes.View) {
	if sb == nil {
		return
	}
	m.confirm = sb
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

// openHelp lists the keys of the screen on show. Any key returns to it.
func (m *app) openHelp() {
	m.helpBack, m.helpPart = m.screen, ""
	if m.screen == screenMain && !m.sidebarKeys() {
		switch {
		case m.inboxKeys():
			m.helpPart = "home"
		case m.mainPane() == paneReview:
			m.helpPart = "review"
		case (m.focus == focusSide || m.mainPane() == paneComputer) && m.comp.tab == tabChanges:
			m.helpPart = "changes"
		case (m.focus == focusSide || m.mainPane() == paneComputer) && m.comp.tab == tabFiles:
			m.helpPart = "files"
		case m.focus == focusSide, m.mainPane() == paneComputer, m.here.term != "":
			m.helpPart = "computer"
		case m.mainPane() == paneMateHome:
			m.helpPart = "mate"
		case m.mainPane() == paneThread:
			m.helpPart = "thread"
		}
	}
	m.screen = screenHelp
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
	case m.search.open:
		return m.searchBindings()
	case m.filtering:
		return []key.Binding{keyKeep, keyClear}
	case m.typing != nil:
		return []key.Binding{m.typing.enter, keyCancel}
	case m.confirm != nil:
		return []key.Binding{keyYes, keyNo}
	}
	switch m.screen {
	case screenSettings:
		return []key.Binding{keyUp, keyOpen, keyActivity, keyBack, keyHelp}
	case screenEnvs:
		return []key.Binding{keyUp, keyChoose, keyCancel}
	case screenHelp:
		return []key.Binding{keyBack}
	case screenActivity, screenEntry:
		return m.activityBindings()
	case screenNew, screenProgress:
		return m.newBindings()
	case screenCheckpoints:
		return m.checkpointBindings()
	case screenComputers:
		return m.computersBindings()
	}
	if m.paneFocused() {
		return m.paneBindings()
	}
	if m.composerKeys() {
		return m.chatBindings()
	}
	if m.sidebarKeys() {
		return m.sidebarBindings()
	}
	if b := m.bannerBindings(); b != nil {
		return b // the banner has the keys: esc gives them back
	}
	var bindings []key.Binding
	switch {
	case m.focus == focusSide && m.sidePane() == paneComputer, m.mainPane() == paneComputer:
		bindings = m.computerBindings()
		if (!m.hasSide() || m.focus != focusSide && m.layoutFor(m.width).side > 0) && !m.lineCursorOn() {
			bindings = append(bindings, keyBack) // esc lets go of a line cursor first
		}
	case m.mainPane() == paneHomeList:
		return append(m.inboxBindings(), keySidebarTo)
	case m.mainPane() == paneMateHome:
		bindings = m.mateBindings()
	case m.mainPane() == paneReview:
		return append(m.reviewPlaceBindings(), keySidebarTo)
	case m.here.term != "":
		if sb, ok := m.sandboxByID(m.here.sandbox); ok {
			bindings = m.terminalBindings(sb)
		}
		if m.hasSide() {
			bindings = append(bindings, keyToSide)
		}
		bindings = append(bindings, keyBack)
	default:
		bindings = m.threadBindings()
	}
	return append(bindings, keySidebarTo, keyHelp)
}

// sidebarBindings are the keys the key bar shows while the sidebar has the cursor.
func (m *app) sidebarBindings() []key.Binding {
	bindings := []key.Binding{keyOpen, keyUp}
	if m.at == atMate || m.at == atThread {
		bindings = append(bindings, keyNewThread, keyOlder)
	} else {
		bindings = append(bindings, keyNewComputer)
	}
	if m.undoable() != nil {
		bindings = append(bindings, keyUndo)
	}
	return append(bindings, keyWaiting, keySidebarTo, keyHelp, keyQuit, keySearch, keySettings, keyActivity, keyEnv)
}
