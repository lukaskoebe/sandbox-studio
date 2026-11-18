package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A thread is a chat with a teammate: its transcript, a request it waits on as a card above the
// composer, and the composer. A teammate's home is who it is, its computers, tasks and what it
// knows, over a composer whose first prompt starts a thread on the computer it shows.

// The keys of a thread and a teammate's home.
var (
	keyNewThread = key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "new thread"))
	keyToSide    = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "computer"))
	keyWrite     = key.NewBinding(key.WithKeys("enter", "p"), key.WithHelp("enter", "write"))
)

// chatRect is where the chat on show is drawn: Home's reading pane, or the main column.
func (m *app) chatRect() rect {
	if m.chats.reading {
		if rc, ok := m.readingRect(); ok {
			return rc
		}
	}
	main, _ := m.frameColumns()
	return main
}

// threadCard is the request the thread on show waits on that the transcript doesn't show itself:
// any but its own permission asks, such as a push or a network request of its computer.
func (m *app) threadCard() (card, bool) {
	c := &m.chats
	if m.here.kind != placeThread || m.here.chat == "" || !c.on || c.reading || c.id != m.here.chat {
		return card{}, false
	}
	ch, ok := m.chatByID(c.id)
	if !ok {
		ch = c.chat
	}
	waiting := map[string]bool{}
	for _, a := range m.threadWaiting(ch) {
		if a.ChatPermission == nil {
			waiting[a.ID] = true
		}
	}
	for _, cd := range m.cards() {
		if waiting[cd.first().ID] {
			return cd, true
		}
	}
	return card{}, false
}

// threadCardLines is the thread's card in w cells with its keycaps, or nil.
func (m *app) threadCardLines(w int) ([]string, []keyHit) {
	c, ok := m.threadCard()
	if !ok || w < 20 {
		return nil, nil
	}
	if m.look.plain {
		return m.cardLines(c, true, w), nil
	}
	return m.visualCard(c, true, w)
}

// threadCardRows is how many rows the thread's card takes.
func (m *app) threadCardRows() int {
	lines, _ := m.threadCardLines(m.chatWidth())
	return len(lines)
}

// threadPane is the thread in the main column, in w cells and h rows.
func (m *app) threadPane(w, h int) []string {
	return m.chatBody(w, h)
}

// cardTakes reports whether a key answers card c in a thread, where the other keys are the
// transcript's.
func cardTakes(msg tea.KeyPressMsg, c card) bool {
	a := c.first()
	var keys []key.Binding
	switch {
	case isGit(a):
		keys = []key.Binding{keyOpen, keyPush, keyForcePush, keyReject, keyIgnore, keyChanges}
	case c.group:
		keys = []key.Binding{keyAllowAll, keyDenyAll, keySplit, keyScope}
	case isNetwork(a):
		keys = []key.Binding{keyAllow, keyDeny, keyOnce, keyOnceDeny, keyHour, keyIgnore, keyPattern, keyScope, keyPort, keySite}
	default:
		for _, k := range kindBindings(a) {
			if k.Help().Desc != "back" && k.Help().Key != "enter" {
				keys = append(keys, k)
			}
		}
	}
	return slices.ContainsFunc(keys, func(b key.Binding) bool { return key.Matches(msg, b) })
}

// threadKey answers the keys of a thread while the composer is closed: its own permission ask
// takes y, a and n, a card waiting above the composer its keys, and the rest are the transcript's.
func (m *app) threadKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.chats
	if c.composing {
		return m.composeKey(msg)
	}
	if it, ok := c.pendingAsk(); ok && it.Kind == chat.KindPermission && it.Permission != nil {
		if n := permissionAlias(msg, permissionPicks(it.Permission.Options)); n > 0 {
			msg = pressOf(fmt.Sprint(n))
		}
	} else if cd, ok := m.threadCard(); ok {
		if !isGit(cd.first()) {
			if k, handled := m.buttonKey(msg, cardOwner(cd), cardControls(cd, *m.choiceOf(cd)), len(cardToggles(cd))); handled {
				if k == "" {
					return m, nil
				}
				msg = pressOf(k)
			}
		}
		if cardTakes(msg, cd) {
			if isGit(cd.first()) {
				return m, m.pushRowKey(msg, cd.first())
			}
			return m, m.cardKey(msg, cd)
		}
	}
	switch {
	case key.Matches(msg, keyNewThread):
		return m, m.newThread(m.here.mate)
	case key.Matches(msg, keyShellTab):
		return m, m.shellTab()
	case key.Matches(msg, keyChangesTab):
		m.changesView()
		return m, nil
	case key.Matches(msg, keyComputer):
		m.nextComputer()
		return m, nil
	}
	return m.chatKey(msg)
}

// cardBarKeys is how many of a card's keys the key bar names in a thread.
const cardBarKeys = 3

// threadBindings are the keys the key bar shows in a thread: the card's first.
func (m *app) threadBindings() []key.Binding {
	c := &m.chats
	if c.composing {
		return m.chatBindings()
	}
	var bindings []key.Binding
	cd, carded := m.threadCard()
	if _, ask := c.pendingAsk(); ask {
		carded = false
	}
	if !carded {
		bindings = m.chatBindings()
	} else {
		// The card shows all its keys on its own line; the bar names its first few, and the
		// transcript's that the card doesn't take.
		for _, b := range m.rowBindings(inboxRow{card: &cd}) {
			if len(bindings) < cardBarKeys && slices.ContainsFunc(b.Keys(), func(k string) bool { return cardTakes(pressOf(k), cd) }) {
				bindings = append(bindings, b)
			}
		}
		for _, b := range m.chatBindings() {
			if slices.ContainsFunc(b.Keys(), func(k string) bool { return cardTakes(pressOf(k), cd) }) {
				if b.Help() != keyPrompt.Help() {
					continue
				}
				b = keyWrite // p is the card's: enter writes
			}
			bindings = append(bindings, b)
		}
	}
	if m.hasSide() {
		bindings = append(bindings, keyToSide)
	}
	bindings = append(bindings, keyShellTab, keyChangesTab)
	if sb := m.computerOn(); sb != nil && len(m.sandboxesOf(sb.TeammateID)) > 1 {
		bindings = append(bindings, keyComputer)
	}
	return bindings
}

// mateHome is a teammate's home in w cells and h rows: who it is over the composer of a new
// thread.
func (m *app) mateHome(w, h int) []string {
	var box []string
	if m.chats.on && m.chats.id == "" {
		box = m.composer(w)
	}
	return withComposer(m.matePane(w, max(h-len(box), 1)), box, h)
}

// mateKey answers the keys of a teammate's home: enter writes the first prompt of a thread on the
// computer it shows, + gives the teammate another computer, and t its terminal.
func (m *app) mateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.chats
	if c.composing {
		return m.composeKey(msg)
	}
	t, ok := m.mateOf(m.here.mate)
	if !ok {
		return m, m.back()
	}
	switch {
	case key.Matches(msg, keyNewComputer): // the composer is the new thread here
		return m, m.openNewFor(t.ID)
	case key.Matches(msg, keyWrite):
		if !c.on {
			m.setNotice(t.Name+" has no computer yet. + gives it one.", false)
			return m, nil
		}
		return m, m.startComposing()
	case key.Matches(msg, keyShellTab):
		return m, m.shellTab()
	case key.Matches(msg, keyChangesTab):
		m.changesView()
	case key.Matches(msg, keyComputer):
		m.nextComputer()
	case key.Matches(msg, keyBack):
		return m, m.back()
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// mateBindings are the keys the key bar shows on a teammate's home.
func (m *app) mateBindings() []key.Binding {
	if m.chats.composing {
		return m.chatBindings()
	}
	bindings := []key.Binding{keyWrite}
	if m.hasSide() {
		bindings = append(bindings, keyToSide)
	}
	bindings = append(bindings, keyShellTab, keyChangesTab, keyNewComputer)
	if len(m.sandboxesOf(m.here.mate)) > 1 {
		bindings = append(bindings, keyComputer)
	}
	return append(bindings, keyBack)
}

// matePane is a teammate's home above its composer, in w cells and h rows: its role and how it
// is, its computers, tasks, what it knows and its network.
func (m *app) matePane(w, h int) []string {
	t, ok := m.mateOf(m.here.mate)
	if !ok {
		return nil
	}
	label := func(name string) string { return styleDim.Render(fit(name, 11)) }
	blank := strings.Repeat(" ", 11)
	head := styleBold.Render(t.Name) + styleDim.Render(" · "+firstNonEmpty(t.Role, "teammate"))
	lines := []string{spread(head, m.mateStatus(t.ID), w), styleDim.Render(strings.Repeat("─", w))}
	list := m.sandboxesOf(t.ID)
	name := "COMPUTER"
	if len(list) > 1 {
		name = "COMPUTERS"
	}
	if len(list) == 0 {
		lines = append(lines, label(name)+styleDim.Render(clip("none yet; + gives "+t.Name+" a computer of its own", w-11)))
	}
	shown := ""
	if sb := m.computerOn(); sb != nil {
		shown = sb.ID
	}
	for i, sb := range list {
		ph := phaseOf(sb)
		facts := nonEmpty(sb.TemplateName)
		if sb.CPUs > 0 {
			facts = append(facts, fmt.Sprintf("%d %s", sb.CPUs, plural(sb.CPUs, "CPU", "CPUs")))
		}
		if sb.MemoryMiB > 0 {
			facts = append(facts, sizeText(uint64(sb.MemoryMiB)<<20))
		}
		if ph == phaseReady && sb.StartedAt != nil {
			facts = append(facts, "up "+shortUptime(m.now.Sub(*sb.StartedAt)))
		} else {
			facts = append(facts, strings.ToLower(ph.label()))
		}
		lead := label(name)
		if i > 0 {
			lead = blank
		}
		sbName := styleBold.Render(sb.Name)
		if sb.ID == shown && len(list) > 1 {
			sbName += styleDim.Render(" ◂")
		}
		lines = append(lines, lead+m.phaseDot(ph)+" "+sbName+styleDim.Render(clip(" · "+strings.Join(facts, " · "), max(w-15-len(sb.Name), 0))))
	}
	lines = append(lines, "")
	s := &m.mates
	switch {
	case !s.loaded:
		lines = append(lines, label("TASKS")+styleDim.Render("…"))
	case len(s.tasks) == 0:
		lines = append(lines, label("TASKS")+styleDim.Render("none"))
	default:
		tasks := slices.Clone(s.tasks)
		slices.SortStableFunc(tasks, func(a, b store.Task) int {
			return boolInt(a.Status == "done") - boolInt(b.Status == "done")
		})
		for i, task := range tasks {
			if i == 5 {
				lines = append(lines, blank+styleDim.Render(fmt.Sprintf("+ %d more", len(tasks)-5)))
				break
			}
			lead := blank
			if i == 0 {
				lead = label("TASKS")
			}
			box, title := "☐", clip(task.Title, w-13)
			switch task.Status {
			case "done":
				box, title = "☑", styleDim.Render(title)
			case "in_progress":
				box = "◐"
			}
			lines = append(lines, lead+styleAccent.Render(box)+" "+title)
		}
	}
	lines = append(lines, "")
	switch {
	case !s.loaded:
	case len(s.facts) == 0:
		lines = append(lines, label("KNOWS")+styleDim.Render("nothing yet"))
	default:
		facts := slices.Clone(s.facts)
		slices.SortStableFunc(facts, func(a, b memory.Fact) int { return b.ObservedAt.Compare(a.ObservedAt) })
		count := fmt.Sprintf("  · %d %s", len(facts), plural(len(facts), "fact", "facts"))
		lines = append(lines, label("KNOWS")+clip(oneLine(facts[0].Text), max(w-11-len(count), 8))+styleDim.Render(count))
	}
	level := levelWords(t.SecurityLevel)
	if t.SecurityLevel == "" {
		level = levelWords(m.env.SecurityLevel) + styleDim.Render(", the environment's")
	}
	lines = append(lines, label("NETWORK")+level+styleDim.Render(clip(" · new rules apply to "+scopeWords(t), max(w-11-len(level), 0))))
	hint := "enter writes to " + t.Name + "; the first prompt starts a thread"
	if sb := m.computerOn(); sb != nil {
		hint += " on " + sb.Name
	}
	lines = append(lines, "", styleDim.Render(clip(hint+".", w)))
	return lines[:min(len(lines), h)]
}
