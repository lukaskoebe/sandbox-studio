package browser

import (
	"context"
	"encoding/json"

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
		pat, err := CheckPattern(store.BrowserPattern{EnvironmentID: a.EnvironmentID, PersonaID: p.PersonaID, Action: p.Action, Origin: p.Origin, Verdict: verdict})
		if err != nil {
			return err
		}
		if _, err := s.Store.AddBrowserPattern(ctx, pat); err != nil {
			return err
		}
	}
	if status == store.StatusApproved {
		s.addGrant(a.EnvironmentID, p.PersonaID, r)
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

// BrowserStatus is the state of a persona's browser, for the Browser page.
type BrowserStatus struct {
	PersonaID string `json:"personaId"`
	VM        string `json:"vm" enum:"absent,created,starting,running,draining,suspended,stopped,crashed" doc:"State of the browser VM"`
	Takeover  bool   `json:"takeover" doc:"The user drives; agent calls are paused"`
	SessionID string `json:"sessionId,omitempty" doc:"The current run's session in the action log"`
	URL       string `json:"url,omitempty" doc:"The page the broker last saw"`
	Driver    string `json:"driver,omitempty" doc:"Name of the agent sandbox that drove the browser last"`
}

// Status reports a persona's browser.
func (s *Service) Status(ctx context.Context, envID, personaID string) (BrowserStatus, error) {
	if _, err := s.Store.Persona(ctx, envID, personaID); err != nil {
		return BrowserStatus{}, err
	}
	_, vm, err := s.VMs.BrowserStatus(ctx, envID, personaID)
	if err != nil {
		return BrowserStatus{}, err
	}
	out := BrowserStatus{PersonaID: personaID, VM: string(vm)}
	s.mu.Lock()
	if st := s.states[personaID]; st != nil && st.env == envID {
		out.Takeover, out.SessionID, out.URL, out.Driver = st.takeover, st.session, st.url, st.driver.Name
	}
	s.mu.Unlock()
	return out, nil
}

// Start boots a persona's browser for the user, so the live view has something to show.
func (s *Service) Start(ctx context.Context, envID, personaID string) error {
	if _, err := s.Store.Persona(ctx, envID, personaID); err != nil {
		return err
	}
	st := s.state(envID, personaID)
	if err := s.boot(ctx, st); err != nil {
		return err
	}
	// Any command launches agent-browser's daemon, and with it Chromium and the live view.
	_, _, err := s.exec(ctx, st, agentBrowserGetURL)
	return err
}

// Stop stops a persona's browser VM; its profile stays.
func (s *Service) Stop(ctx context.Context, envID, personaID string) error {
	st := s.state(envID, personaID)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	s.endTakeover(st)
	if err := s.VMs.StopBrowser(ctx, envID, personaID); err != nil {
		return err
	}
	s.mu.Lock()
	st.reset()
	st.vm = ""
	s.mu.Unlock()
	return nil
}

// Remove deletes a persona's browser VM with its profile (cookies and logins). The
// action log stays.
func (s *Service) Remove(ctx context.Context, envID, personaID string) error {
	st := s.state(envID, personaID)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	s.endTakeover(st)
	if err := s.VMs.RemoveBrowser(ctx, envID, personaID); err != nil {
		return err
	}
	s.mu.Lock()
	st.reset()
	st.vm, st.driver = "", store.Sandbox{}
	s.mu.Unlock()
	return nil
}

// ForgetPersona removes a persona's browser and its action log; the persona can then be
// deleted.
func (s *Service) ForgetPersona(ctx context.Context, envID, personaID string) error {
	if err := s.Remove(ctx, envID, personaID); err != nil {
		return err
	}
	if err := s.DeleteHistory(ctx, envID, personaID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.states, personaID)
	s.mu.Unlock()
	return nil
}

// SetTakeover hands the browser to the user (on) or back to the agents. While the user
// drives, the live view forwards input, agent calls wait and then are refused, and the
// persona's rules decide the browser's connections.
func (s *Service) SetTakeover(ctx context.Context, envID, personaID string, on bool) error {
	if _, err := s.Store.Persona(ctx, envID, personaID); err != nil {
		return err
	}
	st := s.state(envID, personaID)
	if on {
		if _, vm, err := s.VMs.BrowserStatus(ctx, envID, personaID); err != nil || vm != runtime.StatusRunning {
			return ErrNotLive
		}
		s.mu.Lock()
		was := st.takeover
		if !was {
			st.takeover, st.resumed = true, make(chan struct{})
		}
		st.lastUsed = s.now()
		s.mu.Unlock()
		if !was {
			s.record(ctx, st, entry{actor: "user", action: "takeover", outcome: "ok", detail: "the user took over; agents paused"})
		}
		return nil
	}
	if !s.endTakeover(st) {
		return nil
	}
	// The page as the user left it.
	var shot []byte
	s.mu.Lock()
	vm := st.vm
	s.mu.Unlock()
	if res, err := s.Runner.Browser(ctx, vm, agentproto.BrowserCommand{Args: []string{"get", "url"}, Screenshot: true, TimeoutMS: 10000}); err == nil {
		shot = res.Screenshot
	}
	s.record(ctx, st, entry{actor: "user", action: "handback", outcome: "ok", detail: "the user handed back to the agents", shot: shot})
	return nil
}

// endTakeover ends the user's turn; it reports whether there was one.
func (s *Service) endTakeover(st *state) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !st.takeover {
		return false
	}
	st.takeover = false
	close(st.resumed)
	// What the user did is unknown to the broker: refs are stale and a credential the
	// user typed may show.
	st.refs = nil
	return true
}

// Patterns lists a persona's browser patterns.
func (s *Service) Patterns(ctx context.Context, envID, personaID string) ([]store.BrowserPattern, error) {
	if _, err := s.Store.Persona(ctx, envID, personaID); err != nil {
		return nil, err
	}
	return s.Store.BrowserPatterns(ctx, envID, personaID)
}

// AddPattern stores a pattern for a persona.
func (s *Service) AddPattern(ctx context.Context, p store.BrowserPattern) (store.BrowserPattern, error) {
	if _, err := s.Store.Persona(ctx, p.EnvironmentID, p.PersonaID); err != nil {
		return p, err
	}
	p, err := CheckPattern(p)
	if err != nil {
		return p, err
	}
	return s.Store.AddBrowserPattern(ctx, p)
}
