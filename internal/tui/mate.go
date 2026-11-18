package tui

import (
	"net/url"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A teammate's home shows the teammate: its computers, its tasks, what it knows and its network
// level, as the web's teammate page does (thread.go draws it). This file reads what it shows.

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
	keyOlder = key.NewBinding(key.WithKeys("right", "left"), key.WithHelp("→/←", "older threads"))
)

// watchMate reads the tasks and facts of the teammate whose home is on show.
func (m *app) watchMate() tea.Cmd {
	s := &m.mates
	if m.client == nil || m.screen != screenMain || m.here.kind != placeMate || m.mainPane() != paneMateHome {
		return nil
	}
	mate := m.here.mate
	if s.mate != mate {
		*s = mateState{mate: mate, gen: s.gen + 1}
	}
	if s.busy {
		return nil
	}
	s.busy = true
	ctx, client, env, id, gen := m.ctx, m.client, m.env.ID, mate, s.gen
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

// openNewFor opens the form of a new computer for a teammate, which it skips to the template.
func (m *app) openNewFor(mate string) tea.Cmd {
	cmd := m.openNew()
	for i, t := range m.team {
		if t.ID == mate {
			m.newSb.teammate, m.newSb.step = i, stepTemplate
		}
	}
	return cmd
}

// mateState is how the teammate is, from its sessions: needs you, working, done or idle.
func (m *app) mateStatus(id string) string {
	var blocked, working, done bool
	for _, x := range m.threadsOf(id) {
		switch m.stateOf(x) {
		case stateBlocked:
			blocked = true
		case stateWorking:
			working = true
		case stateDone:
			done = true
		}
	}
	switch {
	case blocked:
		return m.waitingMark("▲ waiting for you")
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

// scopeWords is what the teammate's new rules apply to.
func scopeWords(t store.Teammate) string {
	switch t.ApprovalScope {
	case "sandbox":
		return "the computer that asks"
	case "environment":
		return "the environment"
	}
	return t.Name + "'s computers"
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
