package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A security level decides what happens to a request that no rule covers (docs/network.md). It
// comes after the rules, the tracker list and the sites a browser session was approved for, and
// before the user is asked. Explicit rules, in either direction, always win.

// verdict is what a level decides for a request no rule covers.
type verdict int

const (
	ask          verdict = iota // the user decides
	allowCurated                // the curated list covers the host
	allowOpen                   // the level open allows a public address
)

// levelInput is what the decision table reads.
type levelInput struct {
	level       string
	curated     bool // the host is on the curated list, and its entry is on
	placeholder bool // the request carries a placeholder of a secret the host isn't bound to
	public      bool // the host is a name, which the dialer checks, or a public address
}

// decide is the decision table. Locked asks for everything, as Studio did before levels. A request
// that carries a placeholder to a host the secret isn't bound to, and an address that isn't public,
// are asked at every level: no level allows them, and the dialer refuses private addresses anyway.
func (in levelInput) decide() verdict {
	switch {
	case in.level == store.LevelLocked, in.placeholder, !in.public:
		return ask
	case in.level == store.LevelOpen:
		return allowOpen
	case in.curated && (in.level == store.LevelGuarded || in.level == store.LevelAssisted):
		return allowCurated
	}
	return ask
}

// publicHost reports whether host is a name, which the dialer checks when it connects, or a public
// address. A private address is never allowed by a level: a sandbox can't reach one by naming it.
func publicHost(host string) bool {
	ip, err := netip.ParseAddr(host)
	return err != nil || Public(ip)
}

// level returns the security level that decides a sandbox's requests, and the curated entries the
// environment turned off: the teammate's override, or else the environment's level.
func (e *Engine) level(ctx context.Context, envID, teammateID string) (string, []string, error) {
	env, err := e.Store.Environment(ctx, envID)
	if err != nil {
		return "", nil, err
	}
	level := env.SecurityLevel
	if teammateID != "" {
		tm, err := e.Store.Teammate(ctx, envID, teammateID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", nil, err
		}
		if tm.SecurityLevel != "" {
			level = tm.SecurityLevel
		}
	}
	return level, env.CuratedOff, nil
}

// byLevel decides req by the environment's security level, for a request no rule covers. ok is
// true when the level allows it: the returned allow decides the connection, and the allow is
// recorded in Activity. Otherwise the user decides.
func (e *Engine) byLevel(ctx context.Context, req Request, teammate, host string) (allow store.Rule, ok bool, err error) {
	level, off, err := e.level(ctx, req.EnvironmentID, teammate)
	if err != nil {
		return store.Rule{}, false, err
	}
	entry, listed := curatedMatch(host, req.Port, off)
	in := levelInput{level: level, curated: listed, placeholder: req.Placeholder, public: publicHost(host)}
	switch in.decide() {
	case allowOpen:
		e.recordAllowed(ctx, req, teammate, host, "open", "")
	case allowCurated:
		e.recordAllowed(ctx, req, teammate, host, "curated", entry.Name)
	default:
		return store.Rule{}, false, nil
	}
	return store.Rule{EnvironmentID: req.EnvironmentID, Host: host, Action: store.ActionAllow}, true, nil
}

// recordAllowed records a connection a level or the curated list allowed, so Activity shows why it
// went through. Repeated connections of one subject count as attempts; a failure to record doesn't
// stop the connection, which the level already allowed.
func (e *Engine) recordAllowed(ctx context.Context, req Request, teammate, host, decided, reason string) {
	kind, subject, n := e.approvalOf(ctx, req, host, teammate)
	n.Decided, n.Reason = decided, reason
	payload, _ := json.Marshal(n)
	added, err := e.Store.RecordDecided(ctx, store.Approval{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Kind:          kind,
		Subject:       subject,
		Payload:       payload,
	}, store.StatusApproved, "")
	if err != nil {
		e.log().Error("network rules: record an allow", "err", err)
		return
	}
	if added {
		e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: req.EnvironmentID})
	}
}
