package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// screen is what the body shows. Overlays (the environment picker, help) are screens too:
// esc or any key returns to the screen they came from.
type screen int

const (
	screenHome screen = iota
	screenDetail
	screenEnvs
	screenHelp
	screenApprovals
	screenChats    // a sandbox's chats
	screenChat     // one chat's transcript
	screenActivity // the connections and decisions log
	screenEntry    // one entry of the log, and its rule
	screenPager    // the review of a git push or pull request
	screenNew      // the form of a new sandbox
	screenProgress // a new sandbox's create, until it is ready
)

// notice is the one line under the key bar: what the last action did, or what went wrong.
type notice struct {
	text   string
	failed bool
}

// Timing of the background work. Requests give up after requestTimeout; a burst of change
// events refetches once, debounced; a lost event stream is retried every reconnectDelay.
const (
	requestTimeout = 15 * time.Second
	refreshDelay   = 300 * time.Millisecond
	reconnectDelay = 3 * time.Second
)

// app is the terminal UI's model: the data behind the screens, the screen on show, and what the
// user is doing with it. Commands return the messages that Update applies.
type app struct {
	ctx    context.Context
	client *Client
	addr   string // the Studio's loopback address, for the browser hints

	width, height int
	now           time.Time

	envs      []store.Environment
	env       store.Environment // the environment on show; zero until the list arrives
	sandboxes []sandboxes.View
	teammates map[string]string // names by ID
	team      []store.Teammate  // the teammates of the environment, in the API's order
	approvals []api.ApprovalView
	known     map[string]bool // the pending approvals of the last load, to spot new ones
	primed    bool            // whether the first approvals load happened

	loaded     bool // the first sandbox list of the environment arrived
	offline    bool // the event stream is down
	stream     <-chan events.Event
	refreshing bool // a debounced refresh is scheduled

	screen         screen
	back           screen             // where the approvals screen returns to
	helpBack       screen             // where help returns to, and whose keys it lists
	chats          chatState          // the chat screens
	activity       activityState      // the Activity screens
	pager          pagerState         // the review of a git approval
	newSb          newState           // a new sandbox, from its form to its create's end
	cursor         int                // the selected sandbox, among the visible ones
	approvalCursor int                // the selected card of the approvals screen
	choices        map[string]*choice // the approvals' choices, by the ID of a card's first approval
	split          map[string]bool    // the approvals reviewed one by one, out of their group
	picker         int                // the environment picker's cursor
	filter         string             // the text the filter narrows the list by: a name, teammate or template
	filtering      bool               // the filter takes the keys
	confirm        *sandboxes.View    // the sandbox a delete waits for y or n about
	notice         notice
	keys           help.Model
}

// newApp returns the model for a session with the Studio at addr. Its first screen loads on Init.
func newApp(ctx context.Context, client *Client, addr string) *app {
	h := help.New()
	h.Styles = helpStyles()
	h.ShortSeparator = "  "
	return &app{
		ctx: ctx, client: client, addr: addr,
		now:       time.Now(),
		teammates: map[string]string{},
		known:     map[string]bool{},
		choices:   map[string]*choice{},
		split:     map[string]bool{},
		keys:      h,
		width:     defaultWidth,
		height:    defaultHeight,
	}
}

// Init starts the first loads, the event stream and the clock that refreshes uptimes.
func (m *app) Init() tea.Cmd {
	return tea.Batch(m.loadEnvs(), m.loadApprovals(), m.subscribe(), clock())
}

// Update applies one message to the model and returns the commands it starts.
func (m *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, ok := m.chatUpdate(msg); ok {
		return m, cmd
	}
	if cmd, ok := m.activityUpdate(msg); ok {
		return m, cmd
	}
	if cmd, ok := m.newUpdate(msg); ok {
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.keys.SetWidth(msg.Width)
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		m.paste(msg.Content)
	case clockMsg:
		m.now = time.Time(msg)
		return m, clock()
	case envsLoaded:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.envs = msg.envs
		if m.env.ID == "" && len(m.envs) > 0 {
			m.env = m.envs[0]
			return m, m.loadSandboxes()
		}
	case sandboxesLoaded:
		if msg.env != m.env.ID {
			return m, nil // the environment changed while this was on its way
		}
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		prev := ""
		if sb := m.selected(); sb != nil {
			prev = sb.ID
		}
		m.loaded = true
		m.sandboxes = msg.list
		m.team = msg.teammates
		m.teammates = map[string]string{}
		for _, t := range msg.teammates {
			m.teammates[t.ID] = t.Name
		}
		if !m.reselect(prev) && m.screen == screenDetail {
			m.screen = screenHome // the sandbox on show is gone
			m.setNotice("That sandbox was deleted.", true)
		}
		m.followProgress()
	case approvalsLoaded:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		return m, m.setApprovals(msg.list)
	case subscribed:
		if msg.err != nil {
			m.offline = true
			return m, reconnect()
		}
		m.offline, m.stream = false, msg.ch
		return m, listen(msg.ch)
	case eventReceived:
		cmds := []tea.Cmd{listen(m.stream)}
		if !m.refreshing {
			m.refreshing = true
			cmds = append(cmds, refreshAfter())
		}
		return m, tea.Batch(cmds...)
	case streamEnded:
		m.offline, m.stream = true, nil
		return m, reconnect()
	case reconnectDue:
		return m, tea.Batch(m.subscribe(), m.refresh())
	case refreshDue:
		m.refreshing = false
		return m, m.refresh()
	case actionDone:
		if msg.err != nil {
			m.setNotice(msg.err.Error(), true)
		} else {
			m.setNotice(msg.done, false)
		}
		return m, m.refresh()
	case attachDone:
		if msg.err != nil {
			m.setNotice("Could not open the terminal: "+msg.err.Error(), true)
		} else {
			m.setNotice("Detached from "+msg.sandbox+".", false)
		}
		return m, m.refresh()
	}
	return m, nil
}

// refresh refetches what the screens show: the sandboxes of the environment, the pending
// approvals of every environment, and the list of the screen on show.
func (m *app) refresh() tea.Cmd {
	cmds := []tea.Cmd{m.loadSandboxes(), m.loadApprovals()}
	if m.screen == screenChats {
		cmds = append(cmds, m.loadChats())
	}
	return tea.Batch(cmds...)
}

// paste takes pasted text into the field that takes the keys: the filter or the composer.
func (m *app) paste(text string) {
	switch {
	case m.filtering:
		id := m.selectedID()
		*m.filterTarget() += strings.Join(strings.Fields(text), " ")
		m.filtered(id)
	case m.screen == screenChat && m.chats.composing:
		m.chats.draft.paste(text)
	case m.screen == screenNew && m.newSb.step == stepName:
		m.newSb.name.paste(strings.ToLower(strings.Join(strings.Fields(text), "")))
	}
}

// setApprovals stores the pending approvals. The first load announces nothing; a later load
// that brings an approval the last one did not have rings the terminal bell once.
func (m *app) setApprovals(list []api.ApprovalView) tea.Cmd {
	fresh := false
	known := make(map[string]bool, len(list))
	for _, a := range list {
		known[a.ID] = true
		if m.primed && !m.known[a.ID] {
			fresh = true
		}
	}
	m.approvals, m.known, m.primed = list, known, true
	if fresh {
		return bell()
	}
	return nil
}

// fail shows an error from a background load. The screens keep the data they had.
func (m *app) fail(err error) {
	m.setNotice(err.Error(), true)
}

func (m *app) setNotice(text string, failed bool) {
	m.notice = notice{text: text, failed: failed}
}

// reselect puts the cursor back on the sandbox prev when it is still visible, reporting whether
// it is, and keeps the cursor in range otherwise.
func (m *app) reselect(prev string) bool {
	visible := m.visible()
	found := false
	for i, sb := range visible {
		if sb.ID == prev {
			m.cursor, found = i, true
		}
	}
	m.cursor = min(max(m.cursor, 0), max(len(visible)-1, 0))
	return found
}

// selected is the sandbox under the cursor, or nil when none is visible.
func (m *app) selected() *sandboxes.View {
	visible := m.visible()
	if m.cursor < 0 || m.cursor >= len(visible) {
		return nil
	}
	sb := visible[m.cursor]
	return &sb
}

// visible is the sandboxes the filter lets through, in the order the API lists them.
func (m *app) visible() []sandboxes.View {
	query := m.filter
	if query == "" {
		return m.sandboxes
	}
	var out []sandboxes.View
	for _, sb := range m.sandboxes {
		if matches(query, sb.Name, m.teammates[sb.TeammateID], sb.TemplateName) {
			out = append(out, sb)
		}
	}
	return out
}

// pendingFor counts the pending approvals of one sandbox.
func (m *app) pendingFor(sandboxID string) int {
	n := 0
	for _, a := range m.approvals {
		if a.SandboxID == sandboxID {
			n++
		}
	}
	return n
}

// size is the terminal size the frame is laid out for.
func (m *app) size() (int, int) {
	return m.width, m.height
}

// browserURL is the address of a page of the web UI, for hints that send the user there.
func (m *app) browserURL(path string) string {
	return "http://" + m.addr + path
}
