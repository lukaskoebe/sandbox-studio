package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The AI reviewer decides the requests that no rule covers at the assisted level, before the user
// is asked (docs/network.md). It reads what the request says and answers allow, ask or deny. An
// allow or a deny makes a rule that expires after a day, and Activity shows it with a revert.

// Reviewer answers the reviewer's question about a request. internal/netreview implements it on the
// environment's utility model.
type Reviewer interface {
	Review(ctx context.Context, envID string, in ReviewInput) (Review, error)
}

// ErrNoReviewer is returned by a reviewer when the environment has no utility model to ask.
var ErrNoReviewer = errors.New("no AI reviewer: no provider of the environment has a utility model")

// ErrNotReviewed is returned by Revert for an approval the AI reviewer didn't decide, or that was
// reverted already.
var ErrNotReviewed = errors.New("only a decision of the AI reviewer that is still in place can be reverted")

// ReviewInput is what the reviewer sees of a request. Nothing else: no secrets, bodies, headers or
// placeholders. Method and Path are set only for requests Studio reads; Site is the page a browser
// request belongs to.
type ReviewInput struct {
	Host   string
	Port   int
	Method string
	Path   string
	Site   string
	Task   string // the sandbox's current task: its last user prompt, truncated
}

// Review is the reviewer's answer: allow, ask or deny, and why.
type Review struct {
	Decision string
	Reason   string
}

// The decisions a reviewer can make.
const (
	DecisionAllow = "allow"
	DecisionAsk   = "ask"
	DecisionDeny  = "deny"
)

const (
	// reviewTimeout bounds one question. A reviewer that doesn't answer in time leaves the request
	// to the user.
	reviewTimeout = 8 * time.Second
	// reviewRuleLife is how long a rule the reviewer made lasts.
	reviewRuleLife = 24 * time.Hour
	// reviewTaskRunes bounds the task the reviewer sees.
	reviewTaskRunes = 500
	// maxReviews bounds the answers kept for a session; past it they are forgotten.
	maxReviews = 10000
)

// question is one question to the reviewer, shared by the connections that arrive while it is open,
// and kept for the session so the same host is not asked about again for the same task.
type question struct {
	done chan struct{} // closed when the answer is in
}

// reviewKey names the question: the sandbox, a hash of its task, and the host and port.
func reviewKey(sandboxID, task, host string, port int) string {
	sum := sha256.Sum256([]byte(task))
	return sandboxID + "|" + hex.EncodeToString(sum[:8]) + "|" + host + ":" + strconv.Itoa(port)
}

// reviewWith decides req by the AI reviewer, for a request that no rule covers at the assisted
// level. A question is asked once per sandbox, task and host: a connection that comes while it is
// open waits for the answer and then takes what the answer made, if it made a rule. The reviewer's
// rule is found by the rules, so one that was deleted (or expired, or reverted) asks the user.
// ok is true when a rule now decides req.
func (e *Engine) reviewWith(ctx context.Context, req Request, teammate, host string) (store.Rule, bool, error) {
	task, err := e.Store.LastUserPrompt(ctx, req.EnvironmentID, req.SandboxID)
	if err != nil {
		return store.Rule{}, false, err
	}
	key := reviewKey(req.SandboxID, task, host, req.Port)

	e.reviewMu.Lock()
	if e.reviews == nil || len(e.reviews) >= maxReviews {
		e.reviews = map[string]*question{}
	}
	rv, seen := e.reviews[key]
	if !seen {
		rv = &question{done: make(chan struct{})}
		e.reviews[key] = rv
	}
	e.reviewMu.Unlock()

	if seen {
		select {
		case <-rv.done:
		case <-ctx.Done():
			return store.Rule{}, false, ctx.Err()
		}
		return e.liveRule(ctx, req, teammate, host)
	}

	rule, err := e.askReviewer(ctx, req, teammate, host, task)
	if err != nil {
		// No answer: the question is not kept, so the next connection asks the reviewer again.
		e.reviewMu.Lock()
		delete(e.reviews, key)
		e.reviewMu.Unlock()
		close(rv.done)
		e.log().Info("network reviewer", "host", host, "err", err)
		return store.Rule{}, false, nil
	}
	close(rv.done)
	return rule, rule.ID != "", nil
}

// liveRule returns the rule that decides req now, if there is one, for a connection that waited on
// a question. Only rules count: an answer that was reverted or has expired decides nothing.
func (e *Engine) liveRule(ctx context.Context, req Request, teammate, host string) (store.Rule, bool, error) {
	rules, _, err := e.snapshot(ctx, req.EnvironmentID)
	if err != nil {
		return store.Rule{}, false, err
	}
	r, ok := matchRule(e.active(rules), req.SandboxID, teammate, host, req.Port, req.HTTP)
	return r, ok, nil
}

// askReviewer asks the reviewer about req. An allow or a deny makes its rule, which is recorded in
// Activity with the rule's ID so that it can be reverted. An ask leaves the request to the user,
// and an error is returned when there was no answer.
func (e *Engine) askReviewer(ctx context.Context, req Request, teammate, host, task string) (store.Rule, error) {
	if e.Reviewer == nil {
		return store.Rule{}, ErrNoReviewer
	}
	in := ReviewInput{Host: host, Port: req.Port, Task: clip(task, reviewTaskRunes)}
	if req.HTTP != nil {
		// As approvalOf names it: a navigation's own site, else the site of the browser's page.
		in.Method, in.Path, in.Site = req.HTTP.Method, req.HTTP.Path, req.HTTP.Site
		if !req.HTTP.Navigation && e.Browsers != nil {
			in.Site = e.Browsers.TopLevel(ctx, req.EnvironmentID, teammate)
		}
	}
	rctx, cancel := context.WithTimeout(ctx, reviewTimeout)
	answer, err := e.Reviewer.Review(rctx, req.EnvironmentID, in)
	cancel()
	if err != nil {
		return store.Rule{}, err
	}
	if answer.Decision != DecisionAllow && answer.Decision != DecisionDeny {
		return store.Rule{}, nil // ask: the user decides, and the answer is kept
	}

	rule := store.Rule{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Host:          host,
		Ports:         DefaultPorts(req.Port),
		Action:        store.ActionAllow,
		Origin:        store.OriginReviewer,
		Note:          clip(answer.Reason, 500),
	}
	status := store.StatusApproved
	if answer.Decision == DecisionDeny {
		rule.Action, status = store.ActionDeny, store.StatusDenied
	}
	expires := e.now().Add(reviewRuleLife)
	rule.ExpiresAt = &expires
	rule, err = e.Store.CreateRule(ctx, rule)
	if err != nil {
		return store.Rule{}, err
	}
	kind, subject, n := e.approvalOf(ctx, req, host, teammate)
	n.Decided, n.Reason = DecidedReviewer, answer.Reason
	payload, _ := json.Marshal(n)
	added, err := e.Store.RecordDecided(ctx, store.Approval{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Kind:          kind,
		Subject:       subject,
		Payload:       payload,
	}, status, rule.ID)
	if err != nil {
		e.log().Error("network rules: record the reviewer's decision", "err", err)
	} else if added {
		e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: req.EnvironmentID})
	}
	if err := e.Settle(ctx, req.EnvironmentID); err != nil {
		return store.Rule{}, err
	}
	return rule, nil
}

// Revert undoes a decision of the AI reviewer: its rule is deleted, and the request is asked about
// from then on, since the question is not asked again this session. The approval is marked
// reverted in Activity.
func (e *Engine) Revert(ctx context.Context, envID, id string) (store.Approval, error) {
	a, err := e.Store.Approval(ctx, envID, id)
	if err != nil {
		return a, err
	}
	var n NetworkRequest
	if !IsNetworkKind(a.Kind) || a.Reverted || a.Status == store.StatusPending || json.Unmarshal(a.Payload, &n) != nil || n.Decided != DecidedReviewer {
		return a, ErrNotReviewed
	}
	if a.RuleID != "" {
		if err := e.Store.DeleteRule(ctx, envID, a.RuleID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return a, err
		}
	}
	if err := e.Store.RevertApproval(ctx, envID, id); err != nil {
		return a, err
	}
	e.changed(envID, true)
	return e.Store.Approval(ctx, envID, id)
}

// clip shortens s to n runes.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
