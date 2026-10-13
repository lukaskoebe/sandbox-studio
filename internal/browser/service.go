package browser

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The broker is an integration only to settle its approvals; it serves no hosts.
var _ integrations.Integration = (*Service)(nil)

func (s *Service) ID() string                    { return "browser" }
func (s *Service) Routes() []gateway.VirtualHost { return nil }

func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{
		{Kind: KindAction, Decide: s.decideAction},
		{Kind: KindCredential, Decide: s.decideCredentialKind},
	}
}

// Tools and Jobs: the browser's tools are answered by HandleCall, and it runs no integration job.
func (s *Service) Tools() []integrations.Tool { return nil }
func (s *Service) Jobs() []integrations.Job   { return nil }

func decisionStatus(d integrations.Decision) (string, error) {
	status := map[string]string{integrations.Allow: store.StatusApproved, integrations.Deny: store.StatusDenied,
		integrations.Dismiss: store.StatusDismissed}[d.Action]
	if status == "" {
		return "", integrations.ErrAction
	}
	return status, nil
}

// decideAction settles a browser.action approval. Allow lets the waiting call go on, or
// its retry within five minutes; with Remember it stores a pattern that allows (or, on
// deny, denies) the action on the origin from now on.
func (s *Service) decideAction(ctx context.Context, a store.Approval, d integrations.Decision) error {
	status, err := decisionStatus(d)
	if err != nil {
		return err
	}
	var p ActionPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, status, ""); err != nil {
		return err
	}
	s.notify(a.EnvironmentID)
	r := Request{Action: p.Action, Origin: p.Origin, Role: p.Role, Label: p.Label}
	if d.Remember && status != store.StatusDismissed {
		verdict := store.BrowserAllow
		if status == store.StatusDenied {
			verdict = store.BrowserDeny
		}
		pat, err := CheckPattern(store.BrowserPattern{EnvironmentID: a.EnvironmentID, TeammateID: p.TeammateID, Action: p.Action, Origin: p.Origin, Verdict: verdict})
		if err != nil {
			return err
		}
		if _, err := s.Store.AddBrowserPattern(ctx, pat); err != nil {
			return err
		}
	}
	if status == store.StatusApproved {
		s.addGrant(a.EnvironmentID, p.TeammateID, p.Session, r)
	}
	return nil
}

// decideCredentialKind settles a browser.credential approval. Allowing it is always for
// this one fill; there is no pattern for credentials.
func (s *Service) decideCredentialKind(ctx context.Context, a store.Approval, d integrations.Decision) error {
	status, err := decisionStatus(d)
	if err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, status, ""); err != nil {
		return err
	}
	s.notify(a.EnvironmentID)
	return s.decideCredential(ctx, a, status == store.StatusApproved)
}

// BrowserStatus is the state of a teammate's browser, for the Browser page. Takeover and
// URL are the persistent profile's; Sessions lists the ephemeral sessions.
type BrowserStatus struct {
	TeammateID string           `json:"teammateId"`
	VM         string           `json:"vm" enum:"absent,created,starting,running,draining,suspended,stopped,crashed" doc:"State of the browser VM"`
	Takeover   bool             `json:"takeover" doc:"The user drives the persistent profile; agent calls to it are paused"`
	SessionID  string           `json:"sessionId,omitempty" doc:"The current run's session in the action log"`
	URL        string           `json:"url,omitempty" doc:"The page the broker last saw"`
	Driver     string           `json:"driver,omitempty" doc:"Name of the agent sandbox that drove the browser last"`
	Sessions   []BrowserSession `json:"sessions" doc:"Ephemeral sessions, oldest first"`
}

// statusLocked describes st's browser in a VM state.
func (st *state) statusLocked(vm string) BrowserStatus {
	out := BrowserStatus{TeammateID: st.teammate, VM: vm, Takeover: st.takeover, SessionID: st.session, URL: st.url,
		Driver: st.driver.Name, Sessions: []BrowserSession{}}
	for _, pg := range st.eph {
		out.Sessions = append(out.Sessions, BrowserSession{ID: pg.id, Sandbox: pg.owner.Name, URL: pg.url, Takeover: pg.takeover, Opened: pg.opened})
	}
	slices.SortFunc(out.Sessions, func(a, b BrowserSession) int { return cmp.Or(a.Opened.Compare(b.Opened), strings.Compare(a.ID, b.ID)) })
	return out
}

// Status reports a teammate's browser.
func (s *Service) Status(ctx context.Context, envID, teammateID string) (BrowserStatus, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return BrowserStatus{}, err
	}
	_, vm, err := s.VMs.BrowserStatus(ctx, envID, teammateID)
	if err != nil {
		return BrowserStatus{}, err
	}
	out := BrowserStatus{TeammateID: teammateID, VM: string(vm), Sessions: []BrowserSession{}}
	s.mu.Lock()
	if st := s.states[teammateID]; st != nil && st.env == envID {
		out = st.statusLocked(string(vm))
	}
	s.mu.Unlock()
	return out, nil
}

// Start boots a teammate's browser for the user, so the live view has something to show.
func (s *Service) Start(ctx context.Context, envID, teammateID string) error {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return err
	}
	st := s.state(envID, teammateID)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	if err := s.boot(ctx, st); err != nil {
		return err
	}
	// Any command launches agent-browser's daemon, and with it Chromium and the live view.
	data, _, err := s.exec(ctx, st, &st.page, agentBrowserGetURL)
	if err != nil {
		return err
	}
	if url := returnedURL(data); url != "" {
		s.mu.Lock()
		st.observedURL(url)
		st.lastUsed = s.now()
		s.mu.Unlock()
	}
	return nil
}

// Stop stops a teammate's browser VM; its profile stays.
func (s *Service) Stop(ctx context.Context, envID, teammateID string) error {
	st := s.state(envID, teammateID)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	s.endTakeovers(st)
	if err := s.VMs.StopBrowser(ctx, envID, teammateID); err != nil {
		return err
	}
	s.mu.Lock()
	st.reset()
	st.vm = ""
	s.mu.Unlock()
	return nil
}

// Remove deletes a teammate's browser VM with its profile (cookies and logins). The
// action log stays.
func (s *Service) Remove(ctx context.Context, envID, teammateID string) error {
	st := s.state(envID, teammateID)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	s.endTakeovers(st)
	if err := s.VMs.RemoveBrowser(ctx, envID, teammateID); err != nil {
		return err
	}
	s.mu.Lock()
	st.reset()
	st.vm, st.driver = "", store.Sandbox{}
	s.mu.Unlock()
	return nil
}

// ForgetTeammate removes a teammate's browser and its action log; the teammate can then be
// deleted.
func (s *Service) ForgetTeammate(ctx context.Context, envID, teammateID string) error {
	if err := s.Remove(ctx, envID, teammateID); err != nil {
		return err
	}
	if err := s.DeleteHistory(ctx, envID, teammateID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.states, teammateID)
	s.mu.Unlock()
	return nil
}

// SetTakeover hands a session of the browser ("" for the persistent profile) to the user
// (on) or back to the agents. While the user drives, the live view of that session
// forwards input, agent calls to it wait and then are refused, and the teammate's rules
// decide the browser's connections.
func (s *Service) SetTakeover(ctx context.Context, envID, teammateID, session string, on bool) error {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return err
	}
	st := s.state(envID, teammateID)
	s.mu.Lock()
	pg := st.pageFor(session)
	s.mu.Unlock()
	if pg == nil {
		return ErrNoSession
	}
	if on {
		if _, vm, err := s.VMs.BrowserStatus(ctx, envID, teammateID); err != nil || vm != runtime.StatusRunning {
			return ErrNotLive
		}
		s.mu.Lock()
		if st.pageFor(session) != pg {
			s.mu.Unlock()
			return ErrNoSession
		}
		was := pg.takeover
		if !was {
			pg.takeover, pg.resumed = true, make(chan struct{})
		}
		st.lastUsed, pg.used = s.now(), s.now()
		s.mu.Unlock()
		if !was {
			s.record(ctx, st, pg, entry{actor: "user", action: "takeover", outcome: "ok", detail: "the user took over; agents paused"})
		}
		return nil
	}
	s.mu.Lock()
	takingOver := pg.takeover
	s.mu.Unlock()
	if !takingOver {
		return nil
	}
	// Keep paused agents blocked until any user command has finished. Waiting on the raw
	// command slot (rather than acquire, which waits for handback) avoids a takeover cycle.
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	if !s.endTakeover(pg) {
		return nil
	}
	// The page as the user left it.
	var shot []byte
	s.mu.Lock()
	vm := st.vm
	s.mu.Unlock()
	if res, err := s.Runner.Browser(ctx, vm, agentproto.BrowserCommand{Args: []string{"get", "url"}, Ephemeral: pg.slot, Screenshot: true, TimeoutMS: 10000}); err == nil {
		shot = res.Screenshot
	}
	s.record(ctx, st, pg, entry{actor: "user", action: "handback", outcome: "ok", detail: "the user handed back to the agents", shot: shot})
	return nil
}

// endTakeover ends the user's turn in a session; it reports whether there was one.
func (s *Service) endTakeover(pg *page) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pg.takeover {
		return false
	}
	pg.takeover = false
	close(pg.resumed)
	// What the user did is unknown to the broker: refs are stale and a credential the
	// user typed may show.
	pg.refs = nil
	return true
}

// endTakeovers ends the user's turn in every session, before the VM stops.
func (s *Service) endTakeovers(st *state) {
	s.mu.Lock()
	pages := []*page{&st.page}
	for _, pg := range st.eph {
		pages = append(pages, pg)
	}
	s.mu.Unlock()
	for _, pg := range pages {
		s.endTakeover(pg)
	}
}

// Patterns lists a teammate's browser patterns.
func (s *Service) Patterns(ctx context.Context, envID, teammateID string) ([]store.BrowserPattern, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return nil, err
	}
	return s.Store.BrowserPatterns(ctx, envID, teammateID)
}

// AddPattern stores a pattern for a teammate.
func (s *Service) AddPattern(ctx context.Context, p store.BrowserPattern) (store.BrowserPattern, error) {
	if _, err := s.Store.Teammate(ctx, p.EnvironmentID, p.TeammateID); err != nil {
		return p, err
	}
	p, err := CheckPattern(p)
	if err != nil {
		return p, err
	}
	return s.Store.AddBrowserPattern(ctx, p)
}
