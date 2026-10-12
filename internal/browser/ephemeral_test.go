package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// clock is a Service clock that the test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// openEphemeral starts an ephemeral session from sb and returns its ID.
func (f *fixture) openEphemeral(sb store.Sandbox, url string) string {
	f.t.Helper()
	res, ok := f.mustCall(sb, agentproto.MethodBrowserOpen, fmt.Sprintf(`{"url":%q,"ephemeral":true}`, url)).(map[string]string)
	if !ok || !IsEphemeral(res["session"]) {
		f.t.Fatalf("open ephemeral: %#v", res)
	}
	return res["session"]
}

// lastCommand is the browser command that reached the VM last.
func (f *fixture) lastCommand() agentproto.BrowserCommand {
	f.t.Helper()
	cmds := f.fb.allCommands()
	if len(cmds) == 0 {
		f.t.Fatal("no command reached the browser")
	}
	return cmds[len(cmds)-1]
}

// sessionIDs lists the ephemeral sessions the Browser page shows for a teammate.
func (f *fixture) sessionIDs(teammate store.Teammate) []string {
	f.t.Helper()
	st, err := f.svc.Status(f.ctx, f.env, teammate.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	var ids []string
	for _, s := range st.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

// An ephemeral session is its own agent-browser session: its first command starts it fresh,
// later commands reach it by slot, and browser_close ends it for good.
func TestEphemeralSessionLifecycle(t *testing.T) {
	f := newFixture(t)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	if c := f.lastCommand(); c.Args[0] != "open" || c.Ephemeral != 1 || !c.Fresh {
		t.Fatalf("first command %+v", c)
	}
	if ids := f.sessionIDs(f.ada); len(ids) != 1 || ids[0] != id {
		t.Fatalf("sessions %v, want [%s]", ids, id)
	}

	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+id+`"}`)
	if c := f.lastCommand(); c.Ephemeral != 1 || c.Fresh {
		t.Errorf("ephemeral snapshot went to %+v", c)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if c := f.lastCommand(); c.Ephemeral != 0 {
		t.Errorf("persistent snapshot went to slot %d", c.Ephemeral)
	}

	// The action log files each session's actions under its ID and marks the ephemeral ones.
	f.mustCall(f.sbAda, agentproto.MethodBrowserOpen, `{"url":"https://example.com/p"}`)
	all, err := f.svc.Actions(f.ctx, f.env, f.ada.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var ephemeral, persistent int
	for _, a := range all {
		if a.Ephemeral != IsEphemeral(a.SessionID) {
			t.Errorf("action %s in %q is marked %v", a.Action, a.SessionID, a.Ephemeral)
		}
		if a.Ephemeral {
			ephemeral++
		} else {
			persistent++
		}
	}
	if ephemeral == 0 || persistent == 0 {
		t.Errorf("actions: %d ephemeral, %d persistent", ephemeral, persistent)
	}

	f.mustCall(f.sbAda, agentproto.MethodBrowserClose, `{"session":"`+id+`"}`)
	if c := f.lastCommand(); len(c.Args) != 1 || c.Args[0] != "close" || c.Ephemeral != 1 {
		t.Errorf("close sent %+v", c)
	}
	if ids := f.sessionIDs(f.ada); len(ids) != 0 {
		t.Errorf("sessions after close: %v", ids)
	}
	for _, method := range []string{agentproto.MethodBrowserSnapshot, agentproto.MethodBrowserClose} {
		if _, err := f.call(f.sbAda, method, `{"session":"`+id+`"}`); err == nil || !strings.Contains(err.Error(), "there is no ephemeral session") {
			t.Errorf("%s after close: %v", method, err)
		}
	}
}

// Each teammate has two ephemeral sessions, whichever of its sandboxes opened them.
func TestEphemeralSessionLimit(t *testing.T) {
	f := newFixture(t)
	first := f.openEphemeral(f.sbAda, "https://example.com/a")
	f.openEphemeral(f.sbAda2, "https://example.com/b")
	before := len(f.fb.allCommands())
	_, err := f.call(f.sbAda, agentproto.MethodBrowserOpen, `{"url":"https://example.com/c","ephemeral":true}`)
	if err == nil || !strings.Contains(err.Error(), "already has 2 ephemeral browser sessions") {
		t.Fatalf("third session: %v", err)
	}
	if n := len(f.fb.allCommands()); n != before {
		t.Errorf("a refused session reached the browser: %d commands", n-before)
	}
	// Bob's teammate has its own two.
	f.openEphemeral(f.sbBob, "https://example.com/d")
	// Closing one frees its place.
	f.mustCall(f.sbAda, agentproto.MethodBrowserClose, `{"session":"`+first+`"}`)
	f.openEphemeral(f.sbAda, "https://example.com/c")
}

// An ephemeral session ends after EphemeralIdle without a call; calls keep it open.
func TestEphemeralSessionIdleExpiry(t *testing.T) {
	f := newFixture(t)
	now := &clock{now: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}
	f.svc.Now = now.Now
	f.svc.EphemeralIdle = 10 * time.Minute
	used := f.openEphemeral(f.sbAda, "https://example.com/a")
	idle := f.openEphemeral(f.sbAda, "https://example.com/b")

	now.Advance(6 * time.Minute)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+used+`"}`)
	now.Advance(6 * time.Minute)
	f.svc.endIdleEphemeral(f.ctx, f.svc.EphemeralIdle)

	if ids := f.sessionIDs(f.ada); len(ids) != 1 || ids[0] != used {
		t.Fatalf("sessions after expiry: %v, want only %s", ids, used)
	}
	if c := f.lastCommand(); len(c.Args) != 1 || c.Args[0] != "close" || c.Ephemeral != 2 {
		t.Errorf("expiry sent %+v", c)
	}
	if list, _ := f.svc.Actions(f.ctx, f.env, f.ada.ID, idle, 10); len(list) == 0 || list[0].Action != "close" || !strings.Contains(list[0].Detail, "unused") {
		t.Errorf("expiry not in the action log: %+v", list)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+used+`"}`)
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+idle+`"}`); err == nil {
		t.Error("an expired session still answers")
	}

	// A session whose sandbox was deleted ends at the next sweep, even when it is fresh.
	gone := f.openEphemeral(f.sbAda2, "https://example.com/e")
	if err := f.st.DeleteSandbox(f.ctx, f.env, f.sbAda2.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.endIdleEphemeral(f.ctx, time.Hour)
	if ids := f.sessionIDs(f.ada); len(ids) != 1 || ids[0] != used {
		t.Errorf("sessions after its sandbox went: %v, want only %s (%s ended)", ids, used, gone)
	}
}

// Stopping the browser VM ends its ephemeral sessions with it.
func TestEphemeralSessionsEndWithVM(t *testing.T) {
	f := newFixture(t)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	if err := f.svc.Stop(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	if ids := f.sessionIDs(f.ada); len(ids) != 0 {
		t.Errorf("sessions after stop: %v", ids)
	}
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+id+`"}`); err == nil {
		t.Error("a session of a stopped browser answers")
	}
}

// Another sandbox of the teammate cannot drive a session. The user's takeover of one session
// pauses agent calls to that session only, and an unknown session cannot be taken over.
func TestEphemeralSessionOwnerAndTakeover(t *testing.T) {
	f := newFixture(t)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	if _, err := f.call(f.sbAda2, agentproto.MethodBrowserSnapshot, `{"session":"`+id+`"}`); err == nil || !strings.Contains(err.Error(), "belongs to another sandbox") {
		t.Errorf("another sandbox: %v", err)
	}
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+id+`"}`); err == nil || !strings.Contains(err.Error(), "user is driving") {
		t.Errorf("paused session: %v", err)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, id, false); err != nil {
		t.Fatal(err)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+id+`"}`)
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "eph-0000000f", true); !errors.Is(err, ErrNoSession) {
		t.Errorf("takeover of an unknown session: %v", err)
	}
}

// The live view of an ephemeral session streams from its own port, and a session that is
// gone has no view.
func TestEphemeralLiveViewPort(t *testing.T) {
	f := newFixture(t)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	var ports []int
	f.fb.dial = func(port int) (net.Conn, error) {
		ports = append(ports, port)
		return nil, errors.New("no stream in this test")
	}
	f.svc.Relay(f.ctx, f.env, f.ada.ID, id, nil) // the stream never comes up
	if len(ports) == 0 || ports[0] != agentproto.BrowserStreamPort+1 {
		t.Errorf("the session streamed on %v", ports)
	}
	if c := f.lastCommand(); c.Ephemeral != 1 {
		t.Errorf("the stream started in slot %d", c.Ephemeral)
	}
	ports = nil
	f.svc.Relay(f.ctx, f.env, f.ada.ID, "", nil)
	if len(ports) == 0 || ports[0] != agentproto.BrowserStreamPort {
		t.Errorf("the persistent profile streamed on %v", ports)
	}
	if err := f.svc.Relay(f.ctx, f.env, f.ada.ID, "eph-0000000f", nil); !errors.Is(err, ErrNoSession) {
		t.Errorf("view of an unknown session: %v", err)
	}
}

// Tool arguments that do not name a session cleanly are refused before anything reaches the
// browser.
func TestBrowserToolArguments(t *testing.T) {
	f := newFixture(t)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	before := len(f.fb.allCommands())
	for _, c := range []struct{ method, params, want string }{
		{agentproto.MethodBrowserOpen, `{"url":"https://example.com/b","ephemeral":true,"session":"` + id + `"}`, "not both"},
		{agentproto.MethodBrowserOpen, `{"url":"https://example.com/b","ephemeral":"yes"}`, "ephemeral must be true or false"},
		{agentproto.MethodBrowserSnapshot, `{"session":"snap"}`, "session must be an ID"},
		{agentproto.MethodBrowserSnapshot, `{"session":7}`, "session must be a string"},
		{agentproto.MethodBrowserSnapshot, `{"ephemeral":true}`, "bad arguments"},
		{agentproto.MethodBrowserSnapshot, `{"session":"eph-0000000f"}`, "there is no ephemeral session"},
		{agentproto.MethodBrowserClose, `{}`, "give its session ID"},
		{agentproto.MethodBrowserClose, `{"session":""}`, "give its session ID"},
		{agentproto.MethodBrowserClose, `{"session":"` + id + `","extra":1}`, "bad arguments"},
	} {
		if _, err := f.call(f.sbAda, c.method, c.params); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: %v, want %q", c.method, c.params, err, c.want)
		}
	}
	if n := len(f.fb.allCommands()); n != before {
		t.Errorf("refused calls reached the browser: %d commands", n-before)
	}
	// The session survived all of them.
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+id+`"}`)
}

// An approval for an ephemeral session's action names the session. An "always" pattern is
// per origin, so it covers the persistent profile and the sessions alike.
func TestEphemeralApprovals(t *testing.T) {
	f := newFixture(t)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{"session":"`+id+`"}`)
	decide := func(a store.Approval, d integrations.Decision) {
		for _, k := range f.svc.ApprovalKinds() {
			if k.Kind == a.Kind {
				if err := k.Decide(f.ctx, a, d); err != nil {
					t.Error(err)
				}
			}
		}
	}
	go func() {
		a := f.pending(KindAction)
		var p ActionPayload
		json.Unmarshal(a.Payload, &p)
		if p.Session != id || !strings.Contains(a.Subject, "(ephemeral session "+id+")") {
			t.Errorf("approval %+v, subject %q", p, a.Subject)
		}
		decide(a, integrations.Decision{Action: integrations.Allow, Remember: true})
	}()
	f.mustCall(f.sbAda, agentproto.MethodBrowserClick, `{"session":"`+id+`","ref":"e4"}`)

	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	f.mustCall(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`)
	if list, _ := f.st.Approvals(f.ctx, f.env, store.StatusPending, 10); len(list) != 0 {
		t.Errorf("pending approvals left: %v", list)
	}
}
