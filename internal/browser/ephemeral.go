package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Ephemeral sessions (docs/browser.md#ephemeral-sessions): browser_open(url, ephemeral)
// starts a second agent-browser session in the teammate's browser VM, with a throwaway
// profile on the VM's tmpfs. The other tools reach it with its session ID; without one they
// drive the persistent profile. The same policy, approvals and credentials apply; only
// storage differs. A session ends on browser_close, when it was not used for
// EphemeralIdle, when the sandbox that opened it is gone, and with the VM.

// ephemeralPrefix starts every ephemeral session ID. The action log files an ephemeral
// session's actions under its ID, so the prefix also marks them there.
const ephemeralPrefix = "eph-"

var sessionPattern = regexp.MustCompile(`^eph-[0-9a-f]{8}$`)

// IsEphemeral reports whether an action log session is an ephemeral browser session.
func IsEphemeral(sessionID string) bool { return sessionPattern.MatchString(sessionID) }

func newSessionID() string {
	var b [4]byte
	rand.Read(b[:])
	return ephemeralPrefix + hex.EncodeToString(b[:])
}

// address is the session a tool call addresses, taken out of its arguments.
type address struct {
	session   string // "" for the persistent profile
	ephemeral bool   // browser_open: start a new ephemeral session
}

// takeSession removes the session arguments from a call's params: session on every tool,
// ephemeral on browser_open. The tools decode the rest strictly.
func takeSession(method string, params json.RawMessage) (address, json.RawMessage, error) {
	var t address
	if len(bytes.TrimSpace(params)) == 0 {
		return t, params, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil {
		return t, nil, refuse("bad arguments: %v", err)
	}
	if raw, ok := m["session"]; ok {
		if err := json.Unmarshal(raw, &t.session); err != nil {
			return t, nil, refuse("session must be a string")
		}
		if t.session != "" && !sessionPattern.MatchString(t.session) {
			return t, nil, refuse("session must be an ID from browser_open, like eph-1a2b3c4d")
		}
		delete(m, "session")
	}
	if raw, ok := m["ephemeral"]; ok && method == agentproto.MethodBrowserOpen {
		if err := json.Unmarshal(raw, &t.ephemeral); err != nil {
			return t, nil, refuse("ephemeral must be true or false")
		}
		delete(m, "ephemeral")
	}
	if t.ephemeral && t.session != "" {
		return t, nil, refuse("give ephemeral to open a new session, or session to use one, not both")
	}
	rest, err := json.Marshal(m)
	return t, rest, err
}

// agentPage finds the session a call addresses. An ephemeral session belongs to the
// sandbox that opened it.
func (s *Service) agentPage(st *state, id string, sb store.Sandbox) (*page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pg := st.pageFor(id)
	if pg == nil {
		return nil, refuse("there is no ephemeral session %s: it was closed, unused for %s, or its browser stopped; open a new one with browser_open(ephemeral: true)", id, or(s.EphemeralIdle, defaultEphemeral))
	}
	if pg.slot > 0 && pg.owner.ID != sb.ID {
		return nil, refuse("ephemeral session %s belongs to another sandbox", id)
	}
	return pg, nil
}

// reserveEphemeral takes a free slot for a new ephemeral session of sb. The caller holds
// the command slot; the session starts with its first command, sent Fresh.
func (s *Service) reserveEphemeral(st *state, sb store.Sandbox) (*page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	used := map[int]bool{}
	for _, pg := range st.eph {
		used[pg.slot] = true
	}
	for slot := 1; slot <= agentproto.MaxBrowserEphemeral; slot++ {
		if used[slot] {
			continue
		}
		if st.eph == nil {
			st.eph = map[string]*page{}
		}
		now := s.now()
		pg := &page{id: newSessionID(), slot: slot, owner: sb, opened: now, used: now}
		st.eph[pg.id] = pg
		return pg, nil
	}
	return nil, refuse("this teammate already has %d ephemeral browser sessions open; browser_close one first", agentproto.MaxBrowserEphemeral)
}

// dropLocked forgets an ephemeral session and wakes calls waiting for its takeover to end.
func (s *Service) dropLocked(st *state, pg *page) {
	if pg.slot == 0 || st.eph[pg.id] != pg {
		return
	}
	delete(st.eph, pg.id)
	if pg.takeover {
		pg.takeover = false
		close(pg.resumed)
	}
}

// closeEphemeral ends an ephemeral session: the guest closes its browser and deletes its
// profile. The caller holds the command slot. why goes to the action log.
func (s *Service) closeEphemeral(ctx context.Context, st *state, pg *page, why string) error {
	s.mu.Lock()
	vm, current := st.vm, st.eph[pg.id] == pg
	s.mu.Unlock()
	if !current {
		return nil
	}
	var err error
	if vm != "" {
		_, err = s.Runner.Browser(ctx, vm, agentproto.BrowserCommand{Args: []string{"close"}, Ephemeral: pg.slot, TimeoutMS: 20000})
	}
	if err != nil {
		// The slot is wiped again before its next use.
		s.log().Warn("closing an ephemeral browser session failed", "teammate", st.teammate, "session", pg.id, "err", err)
	}
	s.mu.Lock()
	s.dropLocked(st, pg)
	s.mu.Unlock()
	s.record(ctx, st, pg, entry{actor: "agent", sandbox: pg.owner.ID, action: "close", outcome: "ok", detail: why})
	return err
}

// endIdleEphemeral ends ephemeral sessions unused for idle, or whose sandbox is gone;
// never one the user drives.
func (s *Service) endIdleEphemeral(ctx context.Context, idle time.Duration) {
	type due struct {
		st *state
		pg *page
	}
	var list []due
	s.mu.Lock()
	for _, st := range s.states {
		for _, pg := range st.eph {
			if !pg.takeover {
				list = append(list, due{st, pg})
			}
		}
	}
	s.mu.Unlock()
	for _, d := range list {
		s.mu.Lock()
		unused := s.now().Sub(d.pg.used) > idle
		s.mu.Unlock()
		why := fmt.Sprintf("ended after %s unused", idle)
		if !unused {
			if _, err := s.Store.LookupSandbox(ctx, d.pg.owner.ID); err == nil {
				continue
			}
			why = "ended because its sandbox is gone"
		}
		select {
		case d.st.run <- struct{}{}:
		default:
			continue // a command is running; try again next time
		}
		s.mu.Lock()
		still := !d.pg.takeover && (!unused || s.now().Sub(d.pg.used) > idle)
		s.mu.Unlock()
		if still {
			s.closeEphemeral(ctx, d.st, d.pg, why)
		}
		<-d.st.run
	}
}

// BrowserSession is an ephemeral session, for the Browser page.
type BrowserSession struct {
	ID       string    `json:"id"`
	Sandbox  string    `json:"sandbox" doc:"Name of the agent sandbox that opened it"`
	URL      string    `json:"url,omitempty" doc:"The page the broker last saw"`
	Takeover bool      `json:"takeover" doc:"The user drives this session; agent calls to it are paused"`
	Opened   time.Time `json:"opened"`
}

// closeSession is browser_close: it ends the ephemeral session the call names.
func (c *call) closeSession(ctx context.Context, params json.RawMessage) (any, error) {
	if err := decode(params, &struct{}{}); err != nil {
		return nil, err
	}
	if c.pg.slot == 0 {
		return nil, refuse("browser_close ends an ephemeral session: give its session ID from browser_open. The persistent profile stays open.")
	}
	if err := c.s.closeEphemeral(ctx, c.st, c.pg, "closed by the agent"); err != nil {
		return nil, userError{fmt.Errorf("the session was closed, but its browser did not confirm: %w", err)}
	}
	return fmt.Sprintf("closed ephemeral session %s; its cookies, storage and downloads are deleted", c.pg.id), nil
}
