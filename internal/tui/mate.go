package tui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A teammate's row shows the teammate in the pane: its computer (or its sandboxes, once it has
// more than one), its sessions, its tasks, what it knows and its network level, as the web's
// teammate page does. Its keys act on its computer: a new chat, its agent in a terminal, a shell,
// another sandbox or a fork.

// mateEvery is how often the teammate on show is read again.
const mateEvery = 10 * time.Second

// mateState is the tasks and facts of the teammate on show.
type mateState struct {
	mate   string
	tasks  []store.Task
	facts  []memory.Fact
	loaded bool
	busy   bool
	gen    int
}

type (
	mateLoaded struct {
		gen   int
		tasks []store.Task
		facts []memory.Fact
		err   error
	}
	mateTick struct{ gen int }
)

var (
	keyMateChat = key.NewBinding(key.WithKeys("c", "C"), key.WithHelp("c", "new chat"))
	keyShell    = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "shell"))
	keyAgentTUI = key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "agent"))
	keyAnother  = key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "add sandbox"))
)

// watchMate reads the tasks and facts of the teammate whose row is on show in the pane.
func (m *app) watchMate() tea.Cmd {
	s := &m.mates
	if m.client == nil || !m.paneShows() || m.at != atMate || m.mate == "" {
		return nil
	}
	if s.mate != m.mate {
		*s = mateState{mate: m.mate, gen: s.gen + 1}
	}
	if s.busy {
		return nil
	}
	s.busy = true
	ctx, client, env, id, gen := m.ctx, m.client, m.env.ID, m.mate, s.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		out := mateLoaded{gen: gen}
		base := "/api/environments/" + env
		if out.err = client.Do(rctx, "GET", base+"/teammates/"+id+"/tasks", nil, &out.tasks); out.err != nil {
			return out
		}
		q := url.Values{"scope": {"teammate:" + id}, "status": {"active"}}
		out.err = client.Do(rctx, "GET", base+"/memory/facts?"+q.Encode(), nil, &out.facts)
		return out
	}
}

// mateUpdate applies the messages of the teammate on show. ok is false for other messages.
func (m *app) mateUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	s := &m.mates
	switch msg := msg.(type) {
	case mateLoaded:
		if msg.gen != s.gen {
			return nil, true
		}
		if msg.err == nil {
			s.tasks, s.facts, s.loaded = msg.tasks, msg.facts, true
		}
		gen := s.gen
		return tea.Tick(mateEvery, func(time.Time) tea.Msg { return mateTick{gen} }), true
	case mateTick:
		if msg.gen == s.gen {
			s.busy = false
		}
		return nil, true
	}
	return nil, false
}

// mateKey answers the keys of a teammate's row. ok is false for the keys it leaves to Home.
func (m *app) mateKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	t, on := m.selectedMate()
	if !on {
		return nil, false
	}
	sb := m.selected()
	needsOne := func() tea.Cmd {
		m.setNotice(t.Name+" has no sandbox yet. + gives it one.", false)
		return nil
	}
	switch {
	case key.Matches(msg, keyAnother):
		return m.openNewFor(t.ID), true
	case key.Matches(msg, keyMateChat):
		if sb == nil {
			return needsOne(), true
		}
		return m.newChat(sb), true
	case key.Matches(msg, keyAgentTUI):
		if sb == nil {
			return needsOne(), true
		}
		return m.startAgent(sb), true
	case key.Matches(msg, keyShell):
		if sb == nil {
			return needsOne(), true
		}
		return m.attachTo(sb, terminalName), true
	case key.Matches(msg, keyFork):
		if sb == nil {
			return needsOne(), true
		}
		m.askFork(sb)
		return nil, true
	}
	return nil, false
}

// openNewFor opens the form of a new sandbox for a teammate, which it skips to the template.
func (m *app) openNewFor(mate string) tea.Cmd {
	cmd := m.openNew()
	for i, t := range m.team {
		if t.ID == mate {
			m.newSb.teammate, m.newSb.step = i, stepTemplate
		}
	}
	return cmd
}

// mateBindings are the keys the key bar shows on a teammate's row.
func (m *app) mateBindings() []key.Binding {
	bindings := []key.Binding{keyMateChat, keyAgentTUI, keyShell, keyAnother}
	if m.selected() != nil {
		bindings = append(bindings, keyFork, keyLifecycle)
	}
	return append(bindings, keyNumber, keyWaiting, keyHelp, keyQuit)
}

// mateState is how the teammate is, from its sessions: needs you, working, done or idle.
func (m *app) mateStatus(id string) string {
	var blocked, working, done bool
	for _, sb := range m.sandboxesOf(id) {
		for _, x := range m.sessionsOf(sb) {
			switch m.stateOf(x) {
			case stateBlocked:
				blocked = true
			case stateWorking:
				working = true
			case stateDone:
				done = true
			}
		}
	}
	switch {
	case blocked:
		return m.waitingMark("▲ needs you")
	case working:
		return m.stateMark(stateWorking) + styleDim.Render(" working")
	case done:
		return m.stateMark(stateDone) + styleDim.Render(" done")
	}
	return styleDim.Render("○ idle")
}

// levelWords is a security level as the web names it.
func levelWords(level string) string {
	if level == "" {
		return "Default"
	}
	return strings.ToUpper(level[:1]) + level[1:]
}

// matePane is a teammate's row in the pane, in w cells and h rows.
func (m *app) matePane(w, h int) []string {
	t, ok := m.selectedMate()
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
		name = "SANDBOXES"
	}
	if len(list) == 0 {
		lines = append(lines, label(name)+styleDim.Render(clip("none yet; + gives "+t.Name+" a sandbox of its own", w-11)))
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
		lines = append(lines, lead+m.phaseDot(ph)+" "+styleBold.Render(sb.Name)+styleDim.Render(clip(" · "+strings.Join(facts, " · "), max(w-13-len(sb.Name), 0))))
	}
	lines = append(lines, "")
	first := true
	for _, r := range m.homeRows() {
		if r.kind != atSandbox || r.sub < 0 || r.sb < 0 || m.visible()[r.sb].TeammateID != t.ID {
			continue
		}
		x := m.sessionsOf(m.visible()[r.sb])[r.sub]
		if x.kind == sessionOlder {
			continue
		}
		lead := blank
		if first {
			lead, first = label("SESSIONS"), false
		}
		n := " "
		if r.num > 0 {
			n = fmt.Sprint(r.num)
		}
		lines = append(lines, lead+styleDim.Render(n)+" "+m.stateMark(m.stateOf(x))+" "+clip(x.title(), w/2)+"  "+styleDim.Render(sessionKindWord(x)))
	}
	if first {
		lines = append(lines, label("SESSIONS")+styleDim.Render("none yet; c starts a chat"))
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
	lines = append(lines, "", styleDim.Render(clip("c new chat · T agent in a terminal · t shell · + another sandbox · f fork", w)))
	return lines[:min(len(lines), h)]
}

// scopeWords is what the teammate's new rules apply to.
func scopeWords(t store.Teammate) string {
	switch t.ApprovalScope {
	case "sandbox":
		return "the sandbox that asks"
	case "environment":
		return "the environment"
	}
	return t.Name + "'s sandboxes"
}

// phaseDot is a phase's dot, styled.
func (m *app) phaseDot(ph phase) string {
	return strings.SplitN(ph.cell(4), " ", 2)[0]
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
