// Package policy decides what happens to the network connections of sandboxes. It matches
// rules and, when none matches, asks the user and holds the connection for the answer.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Approval kinds of network requests. KindNetwork is a connection to a host no rule covers.
// KindRequest is a request on a connection the gateway terminates (the browser VM's HTTPS): it
// names the request, and its subject is the host and the top-level site it belongs to.
const (
	KindNetwork = "network"
	KindRequest = "network.request"
)

// IsNetworkKind reports whether an approval kind is a network request.
func IsNetworkKind(kind string) bool { return kind == KindNetwork || kind == KindRequest }

// Network decisions beyond allow, deny and dismiss (store.ActionAllow and ActionDeny, and
// dismiss). The "once" ones answer the held connections of one request, without a rule.
const (
	ActionAllowOnce = "allow_once"
	ActionDenyOnce  = "deny_once"
	// ActionAllowSite approves the top-level site of a browser navigation for the browser session.
	ActionAllowSite = "allow_site"
)

// Errors returned by Decide and Resolve.
var (
	ErrUndecided    = errors.New("the request is waiting for approval")
	ErrDismissed    = errors.New("the request was dismissed")
	ErrDenied       = errors.New("the request was denied for this connection")
	ErrDecided      = errors.New("the request was already decided")
	ErrDoesNotCover = errors.New("the rule must cover the requested host and port")
	ErrNoTeammate   = errors.New("the sandbox has no teammate")
	ErrExpired      = errors.New("a rule must expire in the future")
	// ErrPerRequest is what Decide returns for a deferred connection that no rule covers: the
	// connection is not held, and its requests are decided one at a time (HTTP set).
	ErrPerRequest = errors.New("the browser's requests are decided one at a time")
	ErrNotSite    = errors.New("only a browser navigation to a new site can be allowed for its site")
)

// Request is a connection attempt from a sandbox, or a request on a connection the gateway
// terminated.
type Request struct {
	EnvironmentID string
	SandboxID     string
	SandboxName   string
	Host          string // DNS name, or an IP address when no name is known
	Port          int
	// Deferred is set for a connection the gateway terminates and decides request by request
	// (a browser VM's HTTPS): with no rule for the connection it is not held.
	Deferred bool
	// HTTP is the request, for a request on a terminated connection; nil for a connection.
	HTTP *HTTPRequest
}

// HTTPRequest is a request as the gateway reads it on a terminated connection. The headers
// describe the request and never decide it: Chromium's Sec-Fetch headers tell a top-level
// navigation from a subresource, and the Referer is only shown.
type HTTPRequest struct {
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty" doc:"The request path, without the query"`
	Navigation bool   `json:"navigation,omitempty" doc:"A top-level navigation: the page moves to another site"`
	Site       string `json:"site,omitempty" doc:"The top-level site the request belongs to: the navigation's own, or the browser's page"`
	Initiator  string `json:"initiator,omitempty" doc:"Origin of the Referer header, as the browser sent it; shown, never trusted"`
}

// NetworkRequest is the payload of a network approval.
type NetworkRequest struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	SandboxName string   `json:"sandboxName"`
	Patterns    []string `json:"patterns" doc:"Suggested host patterns, most specific first"`
	// The request, for network.request approvals.
	HTTPRequest
}

// Browsers is what the policy asks of teammates' browsers (internal/browser).
type Browsers interface {
	// TopLevel returns the site of the page the browser of teammateID drove last, which the
	// requests of the browser are attributed to, or "" if it has no page open.
	TopLevel(ctx context.Context, envID, teammateID string) string
	// Allows reports whether the sites the user approved for the browser session cover r: a
	// top-level navigation to an approved site, or a subresource while every page the browser
	// has open is on an approved site.
	Allows(ctx context.Context, envID, teammateID string, r HTTPRequest) bool
	// Approve approves a site for the browser session of teammateID.
	Approve(ctx context.Context, envID, teammateID, site string)
}

// Engine decides connections. Every change to rules or approvals must go through it (or
// be followed by Changed) so held connections see it.
type Engine struct {
	Store *store.Store
	Bus   *events.Bus
	Hold  time.Duration    // how long an unanswered connection is held
	Now   func() time.Time // the clock rules expire by; nil uses time.Now
	Log   *slog.Logger     // for housekeeping errors; nil uses slog.Default

	// Browsers, if set, tells which page a browser's requests belong to.
	Browsers Browsers

	mu    sync.Mutex
	rules map[string][]store.Rule  // per environment, loaded on demand
	wake  map[string]chan struct{} // closed when an environment's rules or approvals change
	hits  map[string]ruleHits      // connections rules decided, not yet written to the store
}

// ruleHits are the connections one rule decided since the hits were last written.
type ruleHits struct {
	count int
	last  time.Time
}

// Decide returns the rule for req, asking the user and waiting up to Hold if no rule
// matches. It returns ErrUndecided when time runs out and ErrDismissed when the user
// dismissed the request without a rule. A deferred connection that no rule covers returns
// ErrPerRequest instead: its requests are asked for one at a time.
func (e *Engine) Decide(ctx context.Context, req Request) (store.Rule, error) {
	teammate, err := e.teammate(ctx, req.EnvironmentID, req.SandboxID)
	if err != nil {
		return store.Rule{}, err
	}
	rules, wake, err := e.snapshot(ctx, req.EnvironmentID)
	if err != nil {
		return store.Rule{}, err
	}
	if r, ok := e.covered(ctx, rules, req, teammate); ok {
		return r, nil
	}
	if req.Deferred && req.HTTP == nil {
		return store.Rule{}, ErrPerRequest
	}
	host := Normalize(req.Host)
	kind, subject, payload := e.approvalOf(ctx, req, host, teammate)
	a, _, err := e.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Kind:          kind,
		Subject:       subject,
		Payload:       payload,
	})
	if err != nil {
		return store.Rule{}, err
	}
	e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: req.EnvironmentID, ID: a.ID})

	timer := time.NewTimer(e.Hold)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return store.Rule{}, ctx.Err()
		case <-timer.C:
			return store.Rule{}, ErrUndecided
		case <-wake:
		}
		if rules, wake, err = e.snapshot(ctx, req.EnvironmentID); err != nil {
			return store.Rule{}, err
		}
		if r, ok := e.covered(ctx, rules, req, teammate); ok {
			return r, nil
		}
		cur, err := e.Store.Approval(ctx, req.EnvironmentID, a.ID)
		if err == nil && cur.Status == store.StatusApproved && cur.RuleID == "" && !cur.Once {
			// Approved for its site (decideSite): no rule was made, and none is needed.
			return store.Rule{EnvironmentID: req.EnvironmentID, Host: host, Action: store.ActionAllow}, nil
		}
		if err != nil || cur.Status != store.StatusPending {
			return onceAnswer(cur, err, host, req.Port)
		}
	}
}

// covered returns the rule that decides req now: a matching rule, or else the site the user
// approved for the browser session, which decides with no rule behind it.
func (e *Engine) covered(ctx context.Context, rules []store.Rule, req Request, teammate string) (store.Rule, bool) {
	if r, ok := e.match(rules, req.SandboxID, teammate, req.Host, req.Port); ok {
		return r, true
	}
	if req.HTTP != nil && e.Browsers != nil && e.Browsers.Allows(ctx, req.EnvironmentID, teammate, *req.HTTP) {
		return store.Rule{EnvironmentID: req.EnvironmentID, Host: Normalize(req.Host), Action: store.ActionAllow}, true
	}
	return store.Rule{}, false
}

// approvalOf describes the approval for a request that no rule covers: its kind, its subject
// (which repeated attempts share) and its payload. A request on a terminated connection is
// about its top-level site: a navigation's own site, else the site of the browser's page.
func (e *Engine) approvalOf(ctx context.Context, req Request, host, teammate string) (kind, subject string, payload []byte) {
	n := NetworkRequest{Host: host, Port: req.Port, SandboxName: req.SandboxName, Patterns: Suggestions(host)}
	kind, subject = KindNetwork, host+":"+strconv.Itoa(req.Port)
	if req.HTTP != nil {
		kind, n.HTTPRequest = KindRequest, *req.HTTP
		if !n.Navigation && e.Browsers != nil {
			n.Site = e.Browsers.TopLevel(ctx, req.EnvironmentID, teammate)
		}
		if n.Site != "" {
			subject += " " + n.Site
		}
	}
	payload, _ = json.Marshal(n)
	return kind, subject, payload
}

// onceAnswer is what a held connection gets when its request is no longer pending without a
// rule covering it: a once decision answers this connection, anything else dismisses it.
func onceAnswer(a store.Approval, err error, host string, port int) (store.Rule, error) {
	switch {
	case err != nil || !a.Once:
		return store.Rule{}, ErrDismissed
	case a.Status == store.StatusApproved:
		// Not stored: a once decision creates no rule, so there is no rule ID to report.
		return store.Rule{EnvironmentID: a.EnvironmentID, Host: host, Ports: []int{port}, Action: store.ActionAllow}, nil
	case a.Status == store.StatusDenied:
		return store.Rule{}, ErrDenied
	}
	return store.Rule{}, ErrDismissed
}

// Decision is the user's answer to a network approval.
type Decision struct {
	Action    string    // allow, deny or dismiss
	Host      string    // host pattern; empty means the requested host
	Ports     []int     // nil means DefaultPorts of the requested port
	Scope     string    // sandbox, teammate (the requesting sandbox's owner) or environment
	ExpiresAt time.Time // when the rule stops matching; zero means it never expires
}

// Resolve answers a pending network approval, creating a rule unless it is dismissed.
// Other pending requests the new rule covers are settled with it.
func (e *Engine) Resolve(ctx context.Context, envID, id string, d Decision) (store.Approval, error) {
	a, err := e.Store.Approval(ctx, envID, id)
	if err != nil {
		return a, err
	}
	if a.Status != store.StatusPending {
		return a, ErrDecided
	}
	if d.Action == "dismiss" {
		if err := e.Store.DecideApproval(ctx, envID, id, store.StatusDismissed, ""); err != nil {
			return a, err
		}
		e.changed(envID, false)
		return e.Store.Approval(ctx, envID, id)
	}
	if d.Action == ActionAllowOnce || d.Action == ActionDenyOnce {
		return e.decideOnce(ctx, a, d.Action == ActionAllowOnce)
	}
	if d.Action == ActionAllowSite {
		return e.decideSite(ctx, a)
	}
	if d.Action != store.ActionAllow && d.Action != store.ActionDeny {
		return a, fmt.Errorf("unsupported decision %q", d.Action)
	}
	var req NetworkRequest
	if err := json.Unmarshal(a.Payload, &req); err != nil {
		return a, fmt.Errorf("approval %s: %w", id, err)
	}
	rule := store.Rule{EnvironmentID: envID, Host: req.Host, Ports: d.Ports, Action: d.Action, Origin: store.OriginApproval}
	if d.Host != "" {
		rule.Host = d.Host
	}
	if rule.Ports == nil {
		rule.Ports = DefaultPorts(req.Port)
	}
	owner, err := e.teammate(ctx, envID, a.SandboxID)
	if err != nil {
		return a, err
	}
	switch d.Scope {
	case "sandbox":
		rule.SandboxID = a.SandboxID
	case "teammate":
		if owner == "" {
			return a, ErrNoTeammate
		}
		rule.TeammateID = owner
	}
	if rule.Host, err = ValidPattern(rule.Host); err != nil {
		return a, err
	}
	if !Covers(rule.Host, req.Host) || (len(rule.Ports) > 0 && !slices.Contains(rule.Ports, req.Port)) {
		return a, ErrDoesNotCover
	}
	if !d.ExpiresAt.IsZero() {
		if !d.ExpiresAt.After(e.now()) {
			return a, ErrExpired
		}
		rule.ExpiresAt = &d.ExpiresAt
	}
	if _, err := e.Store.CreateRule(ctx, rule); err != nil {
		return a, err
	}
	// The owner's next card starts with the pattern level chosen here.
	if level := PatternLevel(rule.Host, req.Host); owner != "" && level != "" {
		if err := e.Store.SetApprovalPattern(ctx, envID, owner, level); err != nil {
			return a, err
		}
	}
	if err := e.Settle(ctx, envID); err != nil {
		return a, err
	}
	return e.Store.Approval(ctx, envID, id)
}

// decideOnce answers a pending network request for its one connection. No rule is created, so
// the connections held for the request get the answer and the next connection asks again.
func (e *Engine) decideOnce(ctx context.Context, a store.Approval, allow bool) (store.Approval, error) {
	status := store.StatusDenied
	if allow {
		status = store.StatusApproved
	}
	if err := e.Store.DecideApprovalOnce(ctx, a.EnvironmentID, a.ID, status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return a, ErrDecided
		}
		return a, err
	}
	e.changed(a.EnvironmentID, false)
	return e.Store.Approval(ctx, a.EnvironmentID, a.ID)
}

// decideSite approves the site a browser navigation goes to, for the browser session: no rule is
// made. Pending requests of that site that the browser made from its page are approved with it,
// so the page's requests don't ask again.
func (e *Engine) decideSite(ctx context.Context, a store.Approval) (store.Approval, error) {
	var req NetworkRequest
	if a.Kind != KindRequest || json.Unmarshal(a.Payload, &req) != nil || !req.Navigation || req.Site == "" {
		return a, ErrNotSite
	}
	teammate, err := e.teammate(ctx, a.EnvironmentID, a.SandboxID)
	if err != nil {
		return a, err
	}
	if teammate == "" || e.Browsers == nil {
		return a, ErrNoTeammate
	}
	if err := e.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusApproved, ""); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return a, ErrDecided
		}
		return a, err
	}
	e.Browsers.Approve(ctx, a.EnvironmentID, teammate, req.Site)
	pending, err := e.Store.Approvals(ctx, a.EnvironmentID, store.StatusPending, 10000)
	if err != nil {
		return a, err
	}
	for _, p := range pending {
		var pr NetworkRequest
		if p.Kind != KindRequest || json.Unmarshal(p.Payload, &pr) != nil || pr.Navigation || pr.Site != req.Site {
			continue
		}
		if err := e.Store.DecideApproval(ctx, a.EnvironmentID, p.ID, store.StatusApproved, ""); err != nil && !errors.Is(err, store.ErrNotFound) {
			return a, err
		}
	}
	e.changed(a.EnvironmentID, false)
	return e.Store.Approval(ctx, a.EnvironmentID, a.ID)
}

// Settle decides every pending network approval of an environment that a rule now covers,
// then wakes held connections. Call it after changing rules.
func (e *Engine) Settle(ctx context.Context, envID string) error {
	rules, err := e.Store.Rules(ctx, envID)
	if err != nil {
		return err
	}
	pending, err := e.Store.Approvals(ctx, envID, store.StatusPending, 10000)
	if err != nil {
		return err
	}
	teammates, err := e.Store.SandboxTeammates(ctx, envID)
	if err != nil {
		return err
	}
	for _, a := range pending {
		var req NetworkRequest
		if !IsNetworkKind(a.Kind) || json.Unmarshal(a.Payload, &req) != nil {
			continue
		}
		r, ok := Match(e.active(rules), a.SandboxID, teammates[a.SandboxID], req.Host, req.Port)
		if !ok {
			continue
		}
		status := store.StatusApproved
		if r.Action == store.ActionDeny {
			status = store.StatusDenied
		}
		if err := e.Store.DecideApproval(ctx, envID, a.ID, status, r.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	e.changed(envID, true)
	return nil
}

// teammate returns the teammate owning a sandbox, or "" for an unowned or unknown one. It is
// read from the catalog, not from the caller, so a teammate rule can only reach that
// teammate's sandboxes.
func (e *Engine) teammate(ctx context.Context, envID, sandboxID string) (string, error) {
	if sandboxID == "" {
		return "", nil
	}
	sb, err := e.Store.Sandbox(ctx, envID, sandboxID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return sb.TeammateID, err
}

// snapshot returns the rules of an environment and a channel closed on the next change.
func (e *Engine) snapshot(ctx context.Context, envID string) ([]store.Rule, <-chan struct{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.wake == nil {
		e.wake, e.rules = map[string]chan struct{}{}, map[string][]store.Rule{}
	}
	ch, ok := e.wake[envID]
	if !ok {
		ch = make(chan struct{})
		e.wake[envID] = ch
	}
	rules, ok := e.rules[envID]
	if !ok {
		var err error
		if rules, err = e.Store.Rules(ctx, envID); err != nil {
			return nil, nil, err
		}
		e.rules[envID] = rules
	}
	return rules, ch, nil
}

func (e *Engine) changed(envID string, rules bool) {
	e.mu.Lock()
	delete(e.rules, envID)
	if ch, ok := e.wake[envID]; ok {
		close(ch)
		delete(e.wake, envID)
	}
	e.mu.Unlock()
	e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID})
	if rules {
		e.Bus.Publish(events.Event{Topic: events.TopicRules, EnvironmentID: envID})
	}
}

// now is the time rules expire by.
func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// active returns the rules that match at the current time. A rule stops matching at its
// ExpiresAt, before the sweep deletes it.
func (e *Engine) active(rules []store.Rule) []store.Rule {
	now := e.now()
	var live []store.Rule
	for _, r := range rules {
		if r.ExpiresAt == nil || now.Before(*r.ExpiresAt) {
			live = append(live, r)
		}
	}
	return live
}

// match returns the rule that decides a connection now, and counts the connection as a hit of
// that rule.
func (e *Engine) match(rules []store.Rule, sandboxID, teammateID, host string, port int) (store.Rule, bool) {
	r, ok := Match(e.active(rules), sandboxID, teammateID, host, port)
	if ok {
		e.hit(r.ID)
	}
	return r, ok
}

// hit counts a connection a rule decided. The counts reach the store in batches (flushHits),
// so a connection never writes.
func (e *Engine) hit(ruleID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hits == nil {
		e.hits = map[string]ruleHits{}
	}
	h := e.hits[ruleID]
	h.count++
	h.last = e.now()
	e.hits[ruleID] = h
}

// flushHits writes the hits counted since the last flush. If the write fails, the hits are
// kept for the next one.
func (e *Engine) flushHits(ctx context.Context) error {
	e.mu.Lock()
	pending := e.hits
	e.hits = nil
	e.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	batch := make([]store.RuleHit, 0, len(pending))
	for id, h := range pending {
		batch = append(batch, store.RuleHit{ID: id, Count: h.count, Last: h.last})
	}
	if err := e.Store.AddRuleHits(ctx, batch); err != nil {
		e.mu.Lock()
		if e.hits == nil {
			e.hits = map[string]ruleHits{}
		}
		for id, h := range pending {
			cur := e.hits[id]
			cur.count += h.count
			if h.last.After(cur.last) {
				cur.last = h.last
			}
			e.hits[id] = cur
		}
		e.mu.Unlock()
		return err
	}
	return nil
}

// Run does the engine's housekeeping until ctx ends: once a minute it writes the rule hits and
// deletes the expired rules. On the way out it writes the hits once more.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// Shutdown: a bounded write, since ctx is done.
			flush, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := e.flushHits(flush); err != nil {
				e.log().Error("network rules: write hits", "err", err)
			}
			return
		case <-t.C:
			if err := e.flushHits(ctx); err != nil && ctx.Err() == nil {
				e.log().Error("network rules: write hits", "err", err)
			}
			if err := e.sweep(ctx); err != nil && ctx.Err() == nil {
				e.log().Error("network rules: delete expired rules", "err", err)
			}
		}
	}
}

// sweep deletes the rules that have expired and tells their environments.
func (e *Engine) sweep(ctx context.Context) error {
	envs, err := e.Store.DeleteExpiredRules(ctx, e.now())
	for _, env := range envs {
		e.changed(env, true)
	}
	return err
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}
