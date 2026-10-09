// Package policy decides what happens to the network connections of sandboxes. It matches
// rules and, when none matches, asks the user and holds the connection for the answer.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// KindNetwork is the approval kind for connections to a host no rule covers.
const KindNetwork = "network"

// Errors returned by Decide and Resolve.
var (
	ErrUndecided    = errors.New("the request is waiting for approval")
	ErrDismissed    = errors.New("the request was dismissed")
	ErrDecided      = errors.New("the request was already decided")
	ErrDoesNotCover = errors.New("the rule must cover the requested host and port")
)

// Request is a connection attempt from a sandbox.
type Request struct {
	EnvironmentID string
	SandboxID     string
	SandboxName   string
	Host          string // DNS name, or an IP address when no name is known
	Port          int
}

// NetworkRequest is the payload of a network approval.
type NetworkRequest struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	SandboxName string   `json:"sandboxName"`
	Patterns    []string `json:"patterns" doc:"Suggested host patterns, most specific first"`
}

// Engine decides connections. Every change to rules or approvals must go through it (or
// be followed by Changed) so held connections see it.
type Engine struct {
	Store *store.Store
	Bus   *events.Bus
	Hold  time.Duration // how long an unanswered connection is held

	mu    sync.Mutex
	rules map[string][]store.Rule  // per environment, loaded on demand
	wake  map[string]chan struct{} // closed when an environment's rules or approvals change
}

// Decide returns the rule for req, asking the user and waiting up to Hold if no rule
// matches. It returns ErrUndecided when time runs out and ErrDismissed when the user
// dismissed the request without a rule.
func (e *Engine) Decide(ctx context.Context, req Request) (store.Rule, error) {
	rules, wake, err := e.snapshot(ctx, req.EnvironmentID)
	if err != nil {
		return store.Rule{}, err
	}
	if r, ok := Match(rules, req.SandboxID, req.Host, req.Port); ok {
		return r, nil
	}
	host := Normalize(req.Host)
	payload, _ := json.Marshal(NetworkRequest{Host: host, Port: req.Port, SandboxName: req.SandboxName, Patterns: Suggestions(host)})
	a, _, err := e.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Kind:          KindNetwork,
		Subject:       host + ":" + strconv.Itoa(req.Port),
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
		if r, ok := Match(rules, req.SandboxID, req.Host, req.Port); ok {
			return r, nil
		}
		if cur, err := e.Store.Approval(ctx, req.EnvironmentID, a.ID); err != nil || cur.Status != store.StatusPending {
			return store.Rule{}, ErrDismissed
		}
	}
}

// Decision is the user's answer to a network approval.
type Decision struct {
	Action string // allow, deny or dismiss
	Host   string // host pattern; empty means the requested host
	Ports  []int  // nil means DefaultPorts of the requested port
	Scope  string // sandbox or environment
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
	if d.Action != store.ActionAllow && d.Action != store.ActionDeny {
		return a, fmt.Errorf("unsupported decision %q", d.Action)
	}
	var req NetworkRequest
	if err := json.Unmarshal(a.Payload, &req); err != nil {
		return a, fmt.Errorf("approval %s: %w", id, err)
	}
	rule := store.Rule{EnvironmentID: envID, Host: req.Host, Ports: d.Ports, Action: d.Action}
	if d.Host != "" {
		rule.Host = d.Host
	}
	if rule.Ports == nil {
		rule.Ports = DefaultPorts(req.Port)
	}
	if d.Scope == "sandbox" {
		rule.SandboxID = a.SandboxID
	}
	if rule.Host, err = ValidPattern(rule.Host); err != nil {
		return a, err
	}
	if !Covers(rule.Host, req.Host) || (len(rule.Ports) > 0 && !slices.Contains(rule.Ports, req.Port)) {
		return a, ErrDoesNotCover
	}
	if _, err := e.Store.CreateRule(ctx, rule); err != nil {
		return a, err
	}
	if err := e.Settle(ctx, envID); err != nil {
		return a, err
	}
	return e.Store.Approval(ctx, envID, id)
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
	for _, a := range pending {
		var req NetworkRequest
		if a.Kind != KindNetwork || json.Unmarshal(a.Payload, &req) != nil {
			continue
		}
		r, ok := Match(rules, a.SandboxID, req.Host, req.Port)
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
