package tui

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The live numbers of the visual look: each running sandbox's CPU and memory, read from the
// usage the workspace's status line reads, the history of the sandbox in the detail pane, and the
// spinner's frames. Each is read only while a screen that draws it is on show.

// usageEvery is how often Home reads the usage of its running sandboxes. The runtime samples a
// VM every few seconds, so a faster poll shows nothing new.
const usageEvery = 2 * time.Second

// usageKeep is how many samples of each sandbox the sparklines keep: two minutes.
const usageKeep = 60

// spinEvery is how often the spinner and the shimmer move.
const spinEvery = 120 * time.Millisecond

// usageState is the CPU and memory use of the running sandboxes, kept as a short history.
type usageState struct {
	cpu         map[string][]float64             // CPU share samples by sandbox ID, oldest first
	last        map[string]sandboxes.ResourceUse // the latest sample by sandbox ID
	busy        bool                             // a read or the wait for the next one is under way
	unsupported bool                             // the runtime reports no usage
}

// lineageState is the history of the sandbox the detail pane shows.
type lineageState struct {
	sandbox string
	data    sandboxes.Lineage
	loaded  bool
	busy    bool
	stale   bool // the sandboxes changed since it was read
}

type (
	usageLoaded struct {
		samples     map[string]sandboxes.ResourceUse
		unsupported bool
	}
	usageTick     struct{}
	lineageLoaded struct {
		sandbox string
		data    sandboxes.Lineage
		err     error
	}
	spinTick struct{}
)

// usageWanted is the running sandboxes whose usage the screen on show draws: Home's visible
// ones, or the detail's.
func (m *app) usageWanted() []string {
	if m.look.plain || m.usage.unsupported || m.env.ID == "" {
		return nil
	}
	var ids []string
	switch m.screen {
	case screenHome:
		for _, sb := range m.visible() {
			if phaseOf(sb) == phaseReady {
				ids = append(ids, sb.ID)
			}
		}
	case screenDetail:
		if sb := m.selected(); sb != nil && phaseOf(*sb) == phaseReady {
			ids = append(ids, sb.ID)
		}
	}
	return ids
}

// watchUsage starts reading the usage when a screen that draws it is on show and no read is
// under way.
func (m *app) watchUsage() tea.Cmd {
	if m.usage.busy {
		return nil
	}
	ids := m.usageWanted()
	if len(ids) == 0 {
		return nil
	}
	m.usage.busy = true
	return m.loadUsage(ids)
}

// loadUsage reads the usage of the sandboxes, side by side. A sandbox that stopped meanwhile
// answers 409 and is left out; a runtime without usage answers 501, and the reads stop.
func (m *app) loadUsage(ids []string) tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		out := usageLoaded{samples: map[string]sandboxes.ResourceUse{}}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Go(func() {
				u, err := client.Usage(rctx, env, id)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					out.samples[id] = u
				case apiStatus(err) == http.StatusNotImplemented:
					out.unsupported = true
				}
			})
		}
		wg.Wait()
		return out
	}
}

// record adds a read's samples to the history.
func (u *usageState) record(samples map[string]sandboxes.ResourceUse) {
	if u.cpu == nil {
		u.cpu, u.last = map[string][]float64{}, map[string]sandboxes.ResourceUse{}
	}
	for id, s := range samples {
		hist := append(u.cpu[id], s.CPUPercent)
		if len(hist) > usageKeep {
			hist = hist[len(hist)-usageKeep:]
		}
		u.cpu[id], u.last[id] = hist, s
	}
}

// lineageWanted is the sandbox whose history the screen on show draws: the detail pane's, on
// Home's wide layout or the detail screen.
func (m *app) lineageWanted() string {
	if m.look.plain || m.env.ID == "" {
		return ""
	}
	if m.screen == screenDetail || (m.screen == screenHome && m.homeSplit(m.width)) {
		return m.selectedID()
	}
	return ""
}

// watchLineage reads the history of the detail pane's sandbox when it changed: another sandbox,
// or the sandboxes changed since.
func (m *app) watchLineage() tea.Cmd {
	l := &m.lineage
	id := m.lineageWanted()
	if id == "" || l.busy || (id == l.sandbox && !l.stale) {
		return nil
	}
	if id != l.sandbox {
		*l = lineageState{sandbox: id}
	}
	l.stale, l.busy = false, true
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		data, err := client.Lineage(rctx, env, id)
		return lineageLoaded{sandbox: id, data: data, err: err}
	}
}

// spinWanted reports whether the screen on show has something that moves: a turn that runs, or a
// sandbox that starts where its shimmer shows.
func (m *app) spinWanted() bool {
	if m.look.plain {
		return false
	}
	booting := func(sb sandboxes.View) bool {
		p := phaseOf(sb)
		return p == phaseBooting || p == phaseCreating
	}
	switch m.screen {
	case screenChat:
		return m.chats.running()
	case screenHome:
		if !m.sparkColumn(m.width) {
			return false
		}
		for _, sb := range m.visible() {
			if booting(sb) {
				return true
			}
		}
	case screenDetail:
		sb := m.selected()
		return sb != nil && booting(*sb)
	}
	return false
}

// watchSpin starts the spinner's ticks when something on show moves.
func (m *app) watchSpin() tea.Cmd {
	if m.spinning || !m.spinWanted() {
		return nil
	}
	m.spinning = true
	return tea.Tick(spinEvery, func(time.Time) tea.Msg { return spinTick{} })
}

// liveUpdate applies the messages of the live numbers. ok is false for other messages.
func (m *app) liveUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	switch msg := msg.(type) {
	case usageLoaded:
		m.usage.record(msg.samples)
		m.usage.unsupported = m.usage.unsupported || msg.unsupported
		if len(m.usageWanted()) == 0 {
			m.usage.busy = false
			return nil, true
		}
		return tea.Tick(usageEvery, func(time.Time) tea.Msg { return usageTick{} }), true
	case usageTick:
		ids := m.usageWanted()
		if len(ids) == 0 {
			m.usage.busy = false
			return nil, true
		}
		return m.loadUsage(ids), true
	case lineageLoaded:
		l := &m.lineage
		l.busy = false
		if msg.sandbox == l.sandbox && msg.err == nil {
			l.data, l.loaded = msg.data, true
		}
		return nil, true
	case spinTick:
		m.spin++
		m.spinning = false
		return nil, true // watchSpin goes on while something moves
	}
	return nil, false
}

// cpuSpark is a sandbox's CPU as a sparkline of w cells: a shimmer while it
// starts, and nothing while it is stopped or before the first sample.
func (m *app) cpuSpark(sb sandboxes.View, w int) string {
	switch phaseOf(sb) {
	case phaseBooting, phaseCreating:
		return shimmer(m.spin, w)
	case phaseReady:
		if hist := m.usage.cpu[sb.ID]; len(hist) > 0 {
			return m.pal.running.Render(sparkline(hist, 100, w))
		}
	}
	return strings.Repeat(" ", max(w, 0))
}

// usageLines are a running sandbox's CPU and memory for the detail pane, in w cells.
func (m *app) usageLines(sb sandboxes.View, w int) []string {
	switch p := phaseOf(sb); {
	case p == phaseBooting || p == phaseCreating:
		return []string{styleDim.Render("CPU  ") + shimmer(m.spin, min(16, w-5))}
	case p != phaseReady:
		return []string{styleDim.Render("Not running; s starts it.")}
	case m.usage.unsupported:
		return []string{styleDim.Render("This runtime reports no CPU or memory use.")}
	}
	u, ok := m.usage.last[sb.ID]
	if !ok {
		return []string{styleDim.Render("CPU  …")}
	}
	// The samples not yet read are dim dots, so the line keeps its width as the history grows.
	sparkW := min(max(w/3, 8), 24)
	pad := max(sparkW-len(m.usage.cpu[sb.ID]), 0)
	cpu := styleDim.Render("CPU  "+strings.Repeat("·", pad)) + m.cpuSpark(sb, sparkW-pad) + fmt.Sprintf(" %3.0f %%", u.CPUPercent)
	mem := styleDim.Render("MEM ") + m.memBar(u, 10) + " " + memText(u)
	if lipgloss.Width(cpu)+lipgloss.Width(mem)+5 <= w {
		return []string{cpu + styleDim.Render("  ┊  ") + mem}
	}
	return []string{cpu, mem}
}

// memBar is a sandbox's memory in use as a bar of w cells.
func (m *app) memBar(u sandboxes.ResourceUse, w int) string {
	share := 0.0
	if u.MemoryTotalBytes > 0 {
		share = float64(u.MemoryBytes) / float64(u.MemoryTotalBytes)
	}
	return bar(share, w, m.pal.hue(areaMemory))
}

// memText is the memory in use and the sandbox's memory: "1.4 / 2 GiB".
func memText(u sandboxes.ResourceUse) string {
	if u.MemoryTotalBytes == 0 {
		return sizeText(u.MemoryBytes)
	}
	return strings.TrimSuffix(sizeText(u.MemoryBytes), " GiB") + " / " + sizeText(u.MemoryTotalBytes)
}

// sizeText is a size of memory in GiB with one decimal, or in MiB below one GiB.
func sizeText(b uint64) string {
	const mib, gib = 1 << 20, 1 << 30
	if b < gib {
		return fmt.Sprintf("%d MiB", b/mib)
	}
	s := fmt.Sprintf("%.1f", float64(b)/gib)
	return strings.TrimSuffix(s, ".0") + " GiB"
}

// historyLine is the sandbox's lineage in one line of w cells: created ○, forks ○, checkpoints
// ● (auto ones ·), restores and rebases ◇, and the current generation ◆. The second line says
// what the marks count.
func (m *app) historyLine(sb sandboxes.View, w int) []string {
	l := &m.lineage
	if l.sandbox != sb.ID || !l.loaded {
		return []string{styleDim.Render("…")}
	}
	auto := map[string]bool{}
	for _, cp := range l.data.Checkpoints {
		auto[cp.ID] = cp.Auto
	}
	var marks []string
	var created time.Time
	counts := map[string]int{}
	for _, ev := range l.data.Events {
		switch ev.Kind {
		case store.EventCreate:
			marks, created = append(marks, "○"), ev.CreatedAt
		case store.EventFork:
			if len(marks) == 0 {
				created = ev.CreatedAt // forked: its history starts here
			} else {
				counts["fork"]++
			}
			marks = append(marks, "○")
		case store.EventCheckpoint:
			counts["checkpoint"]++
			if auto[ev.CheckpointID] {
				marks = append(marks, "·")
			} else {
				marks = append(marks, "●")
			}
		case store.EventRestore, store.EventRebase:
			counts["restore"]++
			marks = append(marks, "◇")
		}
	}
	now := styleAccent.Render("◆") + " now"
	line := ""
	room := w - 6
	for i := len(marks) - 1; i >= 0; i-- {
		next := marks[i] + styleDim.Render("──") + line
		if lipgloss.Width(next) > room {
			line = styleDim.Render("…") + line
			break
		}
		line = next
	}
	var legend []string
	if !created.IsZero() {
		legend = append(legend, "created "+age(m.now.Sub(created)))
	}
	for _, k := range []string{"checkpoint", "fork", "restore"} {
		if n := counts[k]; n > 0 {
			legend = append(legend, fmt.Sprintf("%d %s", n, plural(n, k, k+"s")))
		}
	}
	return []string{line + now, styleDim.Render(fit(strings.Join(legend, " · "), w))}
}
