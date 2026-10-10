package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Credentials: fill_credential(ref, name) asks the user with a browser.credential approval.
// When the user allows it, Studio unseals the vault secret and fills it into the field
// itself, through a batch on stdin so the value is in no process list. The agent sees the
// credential's name only; the value goes into no reply, log line, action log entry or
// snapshot (Redact blanks the field, Scrub every echo of the value).

// ActionPayload is the request of a browser.action approval.
type ActionPayload struct {
	PersonaID   string `json:"personaId"`
	PersonaName string `json:"personaName"`
	SandboxID   string `json:"sandboxId"`
	SandboxName string `json:"sandboxName"`
	Action      string `json:"action" doc:"The browser action, such as click or fill"`
	Origin      string `json:"origin" doc:"scheme://host[:port] of the page"`
	URL         string `json:"url"`
	Role        string `json:"role,omitempty" doc:"Accessibility role of the element"`
	Label       string `json:"label,omitempty" doc:"Accessible name of the element"`
	Text        string `json:"text,omitempty" doc:"What the agent wants to type or select"`
}

// CredentialPayload is the request of a browser.credential approval.
type CredentialPayload struct {
	PersonaID   string `json:"personaId"`
	PersonaName string `json:"personaName"`
	SandboxID   string `json:"sandboxId"`
	SandboxName string `json:"sandboxName"`
	Credential  string `json:"credential" doc:"Name of the vault secret"`
	Ref         string `json:"ref"`
	Origin      string `json:"origin"`
	URL         string `json:"url"`
	Role        string `json:"role,omitempty"`
	Label       string `json:"label,omitempty"`
}

// credOutcome is the result of filling one approved credential, for the agent call that
// waits for it or its retry.
type credOutcome struct {
	done chan struct{}
	err  error
	at   time.Time
	key  string // persona, sandbox and subject, to find it on a retry
}

func (s *Service) outcome(approvalID, key string) *credOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcomes == nil {
		s.outcomes = map[string]*credOutcome{}
	}
	now := s.now()
	for id, o := range s.outcomes {
		if now.Sub(o.at) > grantTTL {
			delete(s.outcomes, id)
		}
	}
	o := s.outcomes[approvalID]
	if o == nil {
		o = &credOutcome{done: make(chan struct{}), at: now, key: key}
		s.outcomes[approvalID] = o
	}
	if key != "" {
		o.key = key
	}
	return o
}

// finishedOutcome takes the finished outcome of an earlier approval of the same request.
func (s *Service) finishedOutcome(key string) (*credOutcome, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, o := range s.outcomes {
		if o.key != key {
			continue
		}
		select {
		case <-o.done:
			delete(s.outcomes, id)
			return o, s.now().Sub(o.at) <= grantTTL
		default:
		}
	}
	return nil, false
}

// credential finds a vault secret the page may receive.
func (s *Service) credential(ctx context.Context, envID, name, pageURL string) (store.Secret, error) {
	u, err := url.Parse(pageURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return store.Secret{}, refuse("credentials are filled into https pages only")
	}
	list, err := s.Store.Secrets(ctx, envID)
	if err != nil {
		return store.Secret{}, err
	}
	for _, sec := range list {
		if sec.Name != name {
			continue
		}
		if sec.StudioOnly {
			break
		}
		for _, h := range sec.Hosts {
			if policy.Covers(h, u.Hostname()) {
				return sec, nil
			}
		}
		return store.Secret{}, refuse("credential %q is not bound to %s; the user can add the host to it in the vault", name, u.Hostname())
	}
	return store.Secret{}, refuse("there is no credential named %q", name)
}

func (c *call) fillCredential(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Ref  string `json:"ref"`
		Name string `json:"name"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	pageURL, err := c.currentURL(ctx)
	if err != nil {
		return nil, err
	}
	ref, info, err := c.element(in.Ref)
	if err != nil {
		return nil, err
	}
	origin := Origin(pageURL)
	if _, err := c.s.credential(ctx, c.st.env, in.Name, pageURL); err != nil {
		return nil, err
	}
	patterns, err := c.s.Store.BrowserPatterns(ctx, c.st.env, c.st.persona)
	if err != nil {
		return nil, err
	}
	r := Request{Action: "fill_credential", Origin: origin, Role: info.Role, Label: info.Name}
	if v, reason := Decide(r, patterns); v == Deny {
		c.s.record(ctx, c.st, entry{actor: "agent", sandbox: c.sb.ID, action: r.Action, target: target(info.Role, info.Name), url: pageURL, outcome: "denied", detail: reason})
		return nil, refuse("%s", reason)
	}
	persona, _ := c.s.Store.Persona(ctx, c.st.env, c.st.persona)
	subject := clip(fmt.Sprintf("%s: credential %s into %s on %s", persona.Name, in.Name, target(info.Role, info.Name), origin), 500)
	key := c.st.persona + "\x00" + c.sb.ID + "\x00" + subject + "\x00" + ref
	result := map[string]string{"filled": ref, "credential": in.Name}
	// A retry after the user approved while the agent was not waiting.
	if o, ok := c.s.finishedOutcome(key); ok {
		if o.err != nil {
			return nil, userError{o.err}
		}
		return result, nil
	}
	raw, _ := json.Marshal(CredentialPayload{PersonaID: c.st.persona, PersonaName: persona.Name, SandboxID: c.sb.ID, SandboxName: c.sb.Name,
		Credential: in.Name, Ref: ref, Origin: origin, URL: pageURL, Role: info.Role, Label: info.Name})
	a, created, err := c.s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: c.st.env, SandboxID: c.sb.ID, Kind: KindCredential, Subject: subject, Payload: raw})
	if err != nil {
		return nil, err
	}
	o := c.s.outcome(a.ID, key)
	if created {
		c.s.notify(c.st.env)
		c.s.record(ctx, c.st, entry{actor: "agent", sandbox: c.sb.ID, action: r.Action, target: target(info.Role, info.Name), url: pageURL, outcome: "pending", detail: "credential " + in.Name})
	}
	// Studio fills the field when the user approves (decideCredential); this call holds
	// the browser meanwhile, so the decision must not wait for it.
	release := func() { <-c.st.run }
	release()
	defer func() { c.st.run <- struct{}{} }()
	hold := time.NewTimer(or(c.s.Hold, defaultHold))
	defer hold.Stop()
	select {
	case <-o.done:
	case <-hold.C:
		st, _ := c.s.Store.Approval(ctx, c.st.env, a.ID)
		if st.Status == store.StatusPending {
			return nil, refuse("waiting for the user to approve credential %s in Studio; call again in a minute to retry", in.Name)
		}
		if st.Status != store.StatusApproved {
			return nil, refuse("the user did not allow credential %s here", in.Name)
		}
		return nil, refuse("the credential is being filled; call again to see the result")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.s.finishedOutcome(key) // consumed here
	if o.err != nil {
		return nil, userError{o.err}
	}
	return result, nil
}

// errDenied ends a credential request the user refused.
var errDenied = errors.New("the user did not allow the credential")

// decideCredential settles a browser.credential approval: on allow, Studio fills the
// value into the page, provided it is still on the approved origin.
func (s *Service) decideCredential(ctx context.Context, a store.Approval, allow bool) error {
	var p CredentialPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	o := s.outcome(a.ID, "")
	if !allow {
		o.err = errDenied
		close(o.done)
		return nil
	}
	err := s.fillApproved(ctx, a.EnvironmentID, p)
	o.err = err
	close(o.done)
	return err
}

func (s *Service) fillApproved(ctx context.Context, envID string, p CredentialPayload) error {
	st := s.state(envID, p.PersonaID)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	fail := func(err error) error {
		s.record(ctx, st, entry{actor: "agent", sandbox: p.SandboxID, action: "fill_credential", target: target(p.Role, p.Label), url: p.URL, outcome: "failed", detail: "credential " + p.Credential + ": " + err.Error()})
		return err
	}
	data, _, err := s.exec(ctx, st, agentBrowserGetURL)
	if err != nil {
		return fail(err)
	}
	var cur struct {
		URL string `json:"url"`
	}
	json.Unmarshal(data, &cur)
	if Origin(cur.URL) != p.Origin {
		return fail(refuse("the page moved to %s since the request; ask again", Origin(cur.URL)))
	}
	sec, err := s.credential(ctx, envID, p.Credential, cur.URL)
	if err != nil {
		return fail(err)
	}
	value, err := s.Keys.Value(ctx, envID, sec.ID)
	if err != nil {
		return fail(errors.New("the credential could not be read from the vault"))
	}
	// Known before the command runs, so even its error messages are scrubbed.
	s.mu.Lock()
	st.secrets = append(st.secrets, value)
	if st.credRefs == nil {
		st.credRefs = map[string]bool{}
	}
	st.credRefs[p.Ref] = true
	s.mu.Unlock()
	items, err := s.batch(ctx, st, [][]string{{"fill", "@" + p.Ref, value}, {"get", "attr", "@" + p.Ref, "type"}})
	if err != nil {
		return fail(err)
	}
	if !items[0].Success {
		return fail(errors.New(Scrub(or(items[0].Error, "the fill failed"), []string{value})))
	}
	if !items[1].Success || attrValue(items[1].Result) != "password" {
		// The value shows in the field: no screenshots of this page.
		s.mu.Lock()
		st.visible = true
		s.mu.Unlock()
	}
	s.record(ctx, st, entry{actor: "agent", sandbox: p.SandboxID, action: "fill_credential", target: target(p.Role, p.Label), url: cur.URL, outcome: "ok", detail: "credential " + p.Credential})
	return nil
}

var agentBrowserGetURL = agentproto.BrowserCommand{Args: []string{"get", "url"}}
