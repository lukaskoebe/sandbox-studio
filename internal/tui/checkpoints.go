package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// checkpointState is the checkpoints screen: the sandbox's saved disks, newest first, and the
// restore that waits for y.
type checkpointState struct {
	sandbox   string // the ID of the sandbox whose checkpoints these are
	name      string // its name, while the list may not have it
	back      screen
	list      []store.Checkpoint
	loaded    bool
	cursor    int
	restoring *store.Checkpoint // the checkpoint a restore waits for y or n about
}

type checkpointsLoaded struct {
	sandbox string
	list    []store.Checkpoint
	err     error
}

// The keys of the checkpoints screen.
var (
	keyCheckpoints = key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "checkpoints"))
	keySaveCp      = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "save"))
	keyRestore     = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restore"))
	keyRestoreYes  = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "restore"))
	keySaveName    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save"))
)

// openCheckpoints shows the checkpoints of a sandbox.
func (m *app) openCheckpoints(sb *sandboxes.View) tea.Cmd {
	if sb == nil {
		return nil
	}
	m.checkpoints = checkpointState{sandbox: sb.ID, name: sb.Name, back: m.screen}
	m.screen = screenCheckpoints
	return m.loadCheckpoints()
}

// loadCheckpoints reads the checkpoints of the sandbox on the checkpoints screen.
func (m *app) loadCheckpoints() tea.Cmd {
	ctx, client, env, id := m.ctx, m.client, m.env.ID, m.checkpoints.sandbox
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.Checkpoints(rctx, env, id)
		return checkpointsLoaded{sandbox: id, list: list, err: err}
	}
}

// checkpointsUpdate applies the messages of the checkpoints screen. ok is false for other
// messages.
func (m *app) checkpointsUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	c := &m.checkpoints
	loaded, ok := msg.(checkpointsLoaded)
	if !ok {
		return nil, false
	}
	if loaded.sandbox != c.sandbox {
		return nil, true
	}
	if loaded.err != nil {
		m.fail(loaded.err)
		return nil, true
	}
	prev := ""
	if c.cursor < len(c.list) {
		prev = c.list[c.cursor].ID
	}
	c.list, c.loaded = newestFirst(loaded.list), true
	c.cursor = min(c.cursor, max(len(c.list)-1, 0))
	for i, cp := range c.list {
		if cp.ID == prev {
			c.cursor = i
		}
	}
	return nil, true
}

// newestFirst orders checkpoints by the time they were saved, the newest first.
func newestFirst(list []store.Checkpoint) []store.Checkpoint {
	slices.SortStableFunc(list, func(a, b store.Checkpoint) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return list
}

// checkpointSandbox is the sandbox of the checkpoints screen as the list has it now, or as it
// was when the screen opened.
func (m *app) checkpointSandbox() sandboxes.View {
	if sb, ok := m.sandboxByID(m.checkpoints.sandbox); ok {
		return sb
	}
	return sandboxes.View{Sandbox: store.Sandbox{ID: m.checkpoints.sandbox, Name: m.checkpoints.name}}
}

// checkpointsKey answers the checkpoints screen: the cursor moves, n saves one, r restores the
// one under the cursor after asking.
func (m *app) checkpointsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.checkpoints
	switch {
	case key.Matches(msg, keyBack):
		m.screen = c.back
	case key.Matches(msg, keyUp):
		c.cursor = max(c.cursor-1, 0)
	case key.Matches(msg, keyDown):
		c.cursor = min(c.cursor+1, max(len(c.list)-1, 0))
	case key.Matches(msg, keySaveCp):
		m.askCheckpointName()
	case key.Matches(msg, keyRestore):
		m.askRestore()
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// askCheckpointName asks for the new checkpoint's name on the notice line. An empty name takes
// one from the time, as the web's suggestion does.
func (m *app) askCheckpointName() {
	sb := m.checkpointSandbox()
	if sb.Status != "running" && sb.Status != "stopped" {
		m.setNotice("Save a checkpoint while "+sb.Name+" runs or is stopped.", true)
		return
	}
	m.ask(&linePrompt{
		label: "Checkpoint name (enter for the time): ",
		enter: keySaveName,
		submit: func(text string) (string, tea.Cmd) {
			name := strings.TrimSpace(text)
			if name == "" {
				name = checkpointName(m.now, m.checkpoints.list)
			}
			if len([]rune(name)) > 80 {
				return "at most 80 characters", nil
			}
			m.setNotice("Saving "+name+"…", false)
			return "", m.saveCheckpoint(sb, name)
		},
	})
}

// checkpointName is a name no checkpoint in list has, from the time: the web's suggestion.
func checkpointName(now time.Time, list []store.Checkpoint) string {
	base := "Checkpoint " + now.UTC().Format("2006-01-02 15:04:05") + " UTC"
	taken := map[string]bool{}
	for _, cp := range list {
		taken[cp.Name] = true
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s (%d)", base, n)
	}
	return name
}

// saveCheckpoint saves a checkpoint of the sandbox. A running sandbox is frozen while its disks
// are flushed, which can take a while, so the command has no timeout of its own.
func (m *app) saveCheckpoint(sb sandboxes.View, name string) tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		if _, err := client.CreateCheckpoint(ctx, env, sb.ID, name); err != nil {
			return actionDone{err: fmt.Errorf("save a checkpoint of %s: %w", sb.Name, err)}
		}
		return actionDone{done: "Saved " + name + "."}
	}
}

// askRestore asks before a restore of the checkpoint under the cursor. Only a stopped sandbox,
// on a runtime that can restore, goes back to a checkpoint.
func (m *app) askRestore() {
	c := &m.checkpoints
	if c.cursor >= len(c.list) {
		return
	}
	cp := c.list[c.cursor]
	sb := m.checkpointSandbox()
	switch {
	case cp.State != "ready":
		m.setNotice("That checkpoint is still being saved.", false)
	case !sb.CheckpointRestoreSupported:
		m.setNotice("Restoring needs a newer runtime.", true)
	case sb.Status != "stopped":
		m.setNotice("Stop "+sb.Name+" to restore a checkpoint.", true)
	default:
		c.restoring = &cp
	}
}

// restoreKey answers a pending restore: y puts the sandbox back to the checkpoint, any other key
// keeps it as it is.
func (m *app) restoreKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cp := *m.checkpoints.restoring
	m.checkpoints.restoring = nil
	sb := m.checkpointSandbox()
	if !key.Matches(msg, keyRestoreYes) {
		m.setNotice("Kept "+sb.Name+" as it is.", false)
		return m, nil
	}
	m.setNotice("Restoring "+sb.Name+" to "+cp.Name+"…", false)
	ctx, client, env := m.ctx, m.client, m.env.ID
	return m, func() tea.Msg {
		if _, err := client.RestoreCheckpoint(ctx, env, sb.ID, cp.ID); err != nil {
			return actionDone{err: fmt.Errorf("restore %s: %w", sb.Name, err)}
		}
		return actionDone{done: sb.Name + " is back at " + cp.Name + "."}
	}
}

// checkpointsBody lists the checkpoints, the newest first: the name, whether the schedule took
// it, its state and when it was saved.
func (m *app) checkpointsBody(w, h int) []string {
	c := &m.checkpoints
	sb := m.checkpointSandbox()
	lines := []string{
		styleBold.Render("Checkpoints") + styleDim.Render(" of "+sb.Name+" · the disks, not running processes"),
		"",
	}
	switch {
	case !c.loaded:
		return append(lines, styleDim.Render("Loading…"))
	case len(c.list) == 0:
		return append(lines, "No checkpoints yet. Press n to save one.")
	}
	const colKind, colAge = 7, 9
	nameW := max(w-2-colKind-1-colStatus-1-colAge, 8)
	room := max(h-len(lines), 1)
	offset := 0
	if c.cursor >= room {
		offset = c.cursor - room + 1
	}
	for i := offset; i < len(c.list) && len(lines) < h; i++ {
		cp := c.list[i]
		cursor, name := "  ", fit(cp.Name, nameW)
		if i == c.cursor {
			cursor, name = styleAccent.Render("▸")+" ", styleBold.Render(name)
		}
		kind := "manual"
		if cp.Auto {
			kind = "auto"
		}
		state := styleDim.Render(fit(cp.State, colStatus))
		if cp.State != "ready" {
			state = styleAccent.Render(fit(cp.State, colStatus))
		}
		lines = append(lines, cursor+name+" "+styleDim.Render(fit(kind, colKind))+" "+state+" "+styleDim.Render(age(m.now.Sub(cp.CreatedAt))))
	}
	return lines
}

// checkpointBindings are the keys the key bar shows on the checkpoints screen.
func (m *app) checkpointBindings() []key.Binding {
	if m.checkpoints.restoring != nil {
		return []key.Binding{keyRestoreYes, keyNo}
	}
	if len(m.checkpoints.list) == 0 {
		return []key.Binding{keySaveCp, keyBack, keyHelp}
	}
	return []key.Binding{keySaveCp, keyRestore, keyUp, keyBack, keyHelp}
}
