package tui

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// onSession puts the sidebar's cursor on the row of a thread, the sidebar with the keys: the
// session with the title, in the computer with the ID.
func onSession(t testing.TB, m *app, sandbox, title string) *app {
	t.Helper()
	visible := m.visible()
	for _, r := range m.homeRows() {
		if r.kind != atThread && (r.kind != atSandbox || r.sub < 0) {
			continue
		}
		if x, ok := m.rowSession(r, visible); ok && x.sandbox == sandbox && x.title() == title {
			toSidebar(m) // before the cursor moves: the drawer opens on the row of the place on show
			m.setCursor(r)
			return settle(m)
		}
	}
	t.Fatalf("the sidebar has no row of %q in %s", title, sandbox)
	return m
}

// toSidebar gives the sidebar the keys, as \ does: a drawer below paneWidth.
func toSidebar(m *app) {
	if !m.sidebarKeys() {
		m.toggleSidebar()
	}
}

// onCard puts m on Home's list, its cursor on the i-th request.
func onCard(m *app, i int) *app {
	m.goHome()
	m.inbox.cursor = "card/" + m.cards()[i].first().ID
	m.inboxCursor(m.inboxRows()) // as the next frame does
	quietHome(m)
	return m
}

// cursorCard is the request under the cursor of Home's list.
func cursorCard(t testing.TB, m *app) card {
	t.Helper()
	r, ok := m.inboxRow()
	if !ok || r.card == nil {
		t.Fatalf("the cursor of Home's list is on %q, not a request", m.inbox.cursor)
	}
	return *r.card
}

// openSession opens the thread of a session of the computer with the ID: a chat by its title, or
// an agent's terminal by its name.
func openSession(t testing.TB, m *app, sandbox, title string) *app {
	t.Helper()
	sb, ok := m.sandboxByID(sandbox)
	if !ok {
		t.Fatalf("no computer %s", sandbox)
	}
	for _, x := range m.sessionsOf(sb) {
		if x.title() == title {
			x.mate = sb.TeammateID
			m.open(placeOf(x))
			return settle(m)
		}
	}
	for _, c := range m.sess.chats[sandbox] {
		if chatTitle(c) == title {
			m.open(place{kind: placeThread, mate: c.TeammateID, chat: c.ID, sandbox: sandbox})
			return settle(m)
		}
	}
	t.Fatalf("%s has no session %q", sandbox, title)
	return m
}

// openComputer opens a computer on its own, on the tab.
func openComputer(m *app, id string, tab computerTab) *app {
	m.open(place{kind: placeComputer, sandbox: id})
	m.comp.tab = tab
	return settle(m)
}

// settle runs what Update runs after every message, as a key that moved the cursor would.
func settle(m *app) *app {
	next, _ := m.Update(struct{}{})
	return next.(*app)
}

// onComputer puts the sidebar's cursor on the row of a computer of yours, the sidebar with the
// keys.
func onComputer(t testing.TB, m *app, id string) *app {
	t.Helper()
	visible := m.visible()
	for _, r := range m.homeRows() {
		if r.kind == atSandbox && r.sub < 0 && visible[r.sb].ID == id {
			toSidebar(m) // before the cursor moves: the drawer opens on the row of the place on show
			m.setCursor(r)
			return settle(m)
		}
	}
	t.Fatalf("the sidebar has no row of %s", id)
	return m
}

// sampleMorning is the morning of the TUI's mocks on the sample app: Iris waits to run npm test
// in Fix the login form and to reach one host; Finn waits to push and to reach three hosts while
// he ports the parser; Iris's Rename the guides ended and Update the deploy notes is a scheduled
// run; demo boots.
func sampleMorning(m *app) *app {
	ago := func(d time.Duration) time.Time { return m.now.Add(-d) }
	m.sess = sessionsState{
		env: m.env.ID,
		chats: map[string][]store.Chat{
			"sb1": {{ID: "c1", SandboxID: "sb1", TeammateID: "t1", Title: "Fix the login form", State: store.ChatIdle, UpdatedAt: ago(3 * time.Minute)}},
			"sb2": {{ID: "c2", SandboxID: "sb2", TeammateID: "t2", Title: "Port the parser to go", State: store.ChatRunning, UpdatedAt: ago(time.Minute)}},
			"sb3": {
				{ID: "c5", SandboxID: "sb3", TeammateID: "t1", Title: "Rename the guides", State: store.ChatOrphaned, UpdatedAt: ago(time.Hour)},
				{ID: "c3", SandboxID: "sb3", TeammateID: "t1", Title: "Update the deploy notes", State: store.ChatIdle, TaskID: "t9", UpdatedAt: ago(20 * time.Hour)},
			},
		},
		working: map[string]bool{"chat/c2": true},
	}
	sampleApprovals(m)
	m.approvals[0].Git.Push.TeammateName = "Finn"
	m.approvals[0].Teammate = &api.ApprovalTeammate{ID: "t2", Name: "Finn"}
	m.approvals = slices.Insert(m.approvals, len(m.approvals)-1, api.ApprovalView{ // newest first, as Studio lists them
		Approval: store.Approval{ID: "p1", EnvironmentID: "env1", SandboxID: "sb1", Kind: "chat.permission", Status: "pending", CreatedAt: ago(3 * time.Minute)},
		ChatPermission: &chat.PermissionPayload{
			ChatID: "c1", ChatTitle: "Fix the login form", SandboxName: "blub", TeammateName: "Iris", Title: "npm test", ToolKind: "execute",
			Options: []acp.PermissionOption{
				{OptionID: "o1", Name: "Allow once", Kind: acp.AllowOnce},
				{OptionID: "o2", Name: "Always allow npm test", Kind: acp.AllowAlways},
				{OptionID: "o3", Name: "Reject", Kind: acp.RejectOnce},
			},
		},
	})
	return m
}

// onComputersScreen shows the Computers screen with its cursor on the computer with the ID.
func onComputersScreen(t testing.TB, m *app, id string) *app {
	t.Helper()
	m.openComputers()
	rows, _ := m.computerRows()
	for k, i := range rows {
		if m.visible()[i].ID == id {
			m.computers.cursor = k
			m.computersMove(0)
			return settle(m)
		}
	}
	t.Fatalf("the Computers screen has no row of %s", id)
	return m
}

// quietHome keeps Home from reading the decided requests, the threads and the teammates' tasks on
// the next key, so that the command a key returns is the key's own.
func quietHome(m *app) {
	m.inbox.busy, m.home.busy, m.sess.busy = true, true, true
}

// threadWithCard is Iris's Fix the login form on the morning of the mocks, loaded: blub waits
// to reach a host, so the thread shows the request as its card.
func threadWithCard(m *app) *app {
	return settle(sampleChat(sampleMorning(m)))
}

// detailDataOf gives computer id the ports and history withDetailData gives sb2.
func detailDataOf(m *app, id string) {
	withDetailData(m)
	m.ports.sandbox, m.lineage.sandbox, m.lineage.data.SandboxID = id, id, id
}

// liveTerminal opens computer id on its own on the Terminal tab, on terminal term, attached to
// a fake terminal whose screen says the agent is ready.
func liveTerminal(t *testing.T, m *app, id, term string) *app {
	t.Helper()
	m.client = fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, _ *http.Request) {
		_ = c.Write(ctx, websocket.MessageBinary, []byte("codex is ready\r\n> "))
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	})
	m.open(place{kind: placeComputer, sandbox: id})
	m.comp.tab, m.comp.term = tabTerminal, term
	m = settle(m)
	m = run(m, m.openPane())
	if m.pane.term == nil {
		t.Fatalf("the terminal is not connected: %v", m.pane.err)
	}
	waitFor(t, "the agent's screen", func() bool { return strings.Contains(strings.Join(m.pane.term.lines(80, 10), ""), "codex is ready") })
	return m
}
