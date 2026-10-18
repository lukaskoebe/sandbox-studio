package tui

import (
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
)

// netState is the connections of the sandbox the network column shows.
type netState struct {
	sandbox string
	conns   []gateway.Conn
	loaded  bool
	busy    bool // a read, or the wait for the next one, is under way
	gen     int  // counts the sandboxes shown, so a read or tick for an earlier one is dropped
}

type (
	netLoaded struct {
		gen   int
		conns []gateway.Conn
		err   error
	}
	netTick struct{ gen int }
)

// netWanted is the sandbox whose connections the screen on show draws.
func (m *app) netWanted() string {
	if !m.dashShows() {
		return ""
	}
	return m.selectedID()
}

// watchNet reads the connections of the network column's sandbox, again every activityEvery
// while it shows; another sandbox under the cursor is read at once.
func (m *app) watchNet() tea.Cmd {
	n := &m.net
	id := m.netWanted()
	if id == "" || (n.busy && id == n.sandbox) {
		return nil
	}
	if id != n.sandbox {
		*n = netState{sandbox: id, gen: n.gen + 1}
	}
	n.busy = true
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, n.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		conns, err := client.Connections(rctx, env, id)
		return netLoaded{gen: gen, conns: conns, err: err}
	}
}

// netUpdate applies the network column's messages. ok is false for other messages.
func (m *app) netUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	n := &m.net
	switch msg := msg.(type) {
	case netLoaded:
		if msg.gen != n.gen {
			return nil, true
		}
		if msg.err == nil {
			n.conns, n.loaded = msg.conns, true
		}
		gen := n.gen
		return tea.Tick(activityEvery, func(time.Time) tea.Msg { return netTick{gen} }), true
	case netTick:
		if msg.gen == n.gen {
			n.busy = false // watchNet reads again if the column still shows
		}
		return nil, true
	}
	return nil, false
}

// netPane is the network column in w cells: the sandbox's connections, newest first, each with
// its verdict's dot, its age, its host and its verdict.
func (m *app) netPane(w, h int) []string {
	n := &m.net
	conns := slices.Clone(n.conns)
	slices.SortStableFunc(conns, func(x, y gateway.Conn) int { return y.Started.Compare(x.Started) })
	bad := 0
	for _, c := range conns {
		if c.Verdict == gateway.VerdictDenied || c.Verdict == gateway.VerdictFailed {
			bad++
		}
	}
	head := "NETWORK"
	if len(conns) > 0 {
		head += fmt.Sprintf("  %d %s", len(conns), plural(len(conns), "connection", "connections"))
		if bad > 0 {
			head += fmt.Sprintf(" · %d refused or failed", bad)
		}
	}
	lines := []string{styleDim.Render(clip(head, w))}
	switch {
	case !n.loaded:
		return append(lines, styleDim.Render("Loading…"))
	case len(conns) == 0:
		return append(lines, styleDim.Render(clip("No connections yet.", w)))
	}
	const colAge, colWord = 4, 10
	hostW := max(w-2-colAge-colWord-2, 8)
	for _, c := range conns {
		if len(lines) >= h {
			break
		}
		word := connVerdict(c.Verdict)
		style := m.verdictStyle(word)
		lines = append(lines, style.Render("●")+" "+styleDim.Render(fit(shortAge(m.now.Sub(c.Started)), colAge))+" "+
			fit(hostPortOf(c.Host, c.Port), hostW)+" "+style.Render(fit(word, colWord)))
	}
	return lines
}

// shortAge is how long ago something happened in its largest unit: "now", "40s", "12m", "2h".
func shortAge(d time.Duration) string {
	switch {
	case d < 5*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}
