package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// portsEvery is how often the detail screen reads a running sandbox's ports again. Studio
// announces no change to them, so the screen polls them, as the web's Ports region does.
const portsEvery = 3 * time.Second

// maxPorts bounds the ports the detail screen lists; the rest are counted.
const maxPorts = 6

// portsState is what the detail screen knows of its sandbox's open ports, and, for the visual
// look's detail pane, of its terminals.
type portsState struct {
	sandbox   string // the sandbox the list is of
	list      []api.PreviewPort
	terminals []agentproto.Session
	loaded    bool // the list of sandbox arrived
	busy      bool // a read or the wait for the next one is under way
}

type (
	portsLoaded struct {
		sandbox   string
		list      []api.PreviewPort
		terminals []agentproto.Session
		err       error
	}
	portsTick struct{ sandbox string }
)

// portsWanted is the sandbox whose ports the screen on show lists, while it runs: the detail's,
// or the one in the detail pane of Home's wide layout.
func (m *app) portsWanted() string {
	if m.screen != screenDetail && (m.screen != screenHome || !m.homeSplit(m.width)) {
		return ""
	}
	sb := m.selected()
	if sb == nil || phaseOf(*sb) != phaseReady {
		return ""
	}
	return sb.ID
}

// watchPorts starts reading the ports when the detail of a running sandbox is on show and no
// read is under way. A read that is under way checks again when it ends.
func (m *app) watchPorts() tea.Cmd {
	id := m.portsWanted()
	if id == "" || m.ports.busy {
		return nil
	}
	if m.ports.sandbox != id {
		m.ports = portsState{sandbox: id}
	}
	m.ports.busy = true
	return m.loadPorts(id)
}

// loadPorts reads the open ports of a sandbox, and in the visual look its terminals, which only
// it draws. A failed read of the terminals leaves them out.
func (m *app) loadPorts(id string) tea.Cmd {
	ctx, client, env, terminals := m.ctx, m.client, m.env.ID, !m.look.plain
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.Ports(rctx, env, id)
		out := portsLoaded{sandbox: id, list: list, err: err}
		if terminals && err == nil {
			out.terminals, _ = client.Terminals(rctx, env, id)
		}
		return out
	}
}

// portsUpdate applies the messages of the ports. ok is false for other messages. The reads go
// on every portsEvery while the sandbox's detail is on show, and stop once it is not.
func (m *app) portsUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	p := &m.ports
	switch msg := msg.(type) {
	case portsLoaded:
		if msg.sandbox == p.sandbox && msg.err == nil {
			p.list, p.terminals, p.loaded = msg.list, msg.terminals, true
		}
		if m.portsWanted() != msg.sandbox {
			p.busy = false
			return nil, true
		}
		return tea.Tick(portsEvery, func(time.Time) tea.Msg { return portsTick{sandbox: msg.sandbox} }), true
	case portsTick:
		if m.portsWanted() != msg.sandbox {
			p.busy = false
			return nil, true
		}
		return m.loadPorts(msg.sandbox), true
	}
	return nil, false
}

// portLines are the detail's Ports: each open port with its preview address, a link where the
// terminal shows links. A stopped sandbox has none.
func (m *app) portLines(id string, ready bool) []string {
	p := &m.ports
	switch {
	case !ready:
		return []string{fact("Ports", styleDim.Render("listed while the sandbox runs"))}
	case p.sandbox != id || !p.loaded:
		return []string{fact("Ports", styleDim.Render("…"))}
	case len(p.list) == 0:
		return []string{fact("Ports", styleDim.Render("none open; start a server, e.g. on port 3000"))}
	}
	var lines []string
	for i, port := range p.list {
		if i == maxPorts {
			more := len(p.list) - maxPorts
			lines = append(lines, fact("", styleDim.Render(fmt.Sprintf("and %d more", more))))
			break
		}
		label := ""
		if i == 0 {
			label = "Ports"
		}
		link := lipgloss.NewStyle().Hyperlink(port.URL).Render(port.URL)
		lines = append(lines, fact(label, fit(fmt.Sprintf(":%d", port.Port), 7)+link))
	}
	return lines
}
