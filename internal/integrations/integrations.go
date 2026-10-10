// Package integrations is the seam between Studio's core and the services it offers
// sandboxes on the host (PLAN.md §6.9). An integration serves virtual hosts under
// studio.internal through the gateway and settles the approvals it raises; the core
// knows nothing else about it. The git review remote is the first one.
package integrations

import (
	"context"
	"errors"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Decision actions, as the inbox sends them.
const (
	Allow   = "allow"
	Deny    = "deny"
	Dismiss = "dismiss"
)

// ErrAction marks a decision an approval kind does not support.
var ErrAction = errors.New("unsupported action")

// Decision is the user's answer to an approval.
type Decision struct {
	Action string // Allow, Deny or Dismiss
	Note   string // optional, passed on to the sandbox where the integration can
}

// ApprovalKind settles approvals of one kind. Decide moves a pending approval out of
// pending (store.ErrNotFound if it is not pending) and does whatever the decision implies.
type ApprovalKind struct {
	Kind   string
	Decide func(ctx context.Context, a store.Approval, d Decision) error
}

// Integration is a host-side service for sandboxes.
type Integration interface {
	ID() string
	Routes() []gateway.VirtualHost
	ApprovalKinds() []ApprovalKind
}

// Set is the integrations Studio runs.
type Set []Integration

// Routes collects every integration's virtual hosts.
func (s Set) Routes() []gateway.VirtualHost {
	var out []gateway.VirtualHost
	for _, i := range s {
		out = append(out, i.Routes()...)
	}
	return out
}

// Kind returns the settler for an approval kind, if an integration owns it.
func (s Set) Kind(kind string) (ApprovalKind, bool) {
	for _, i := range s {
		for _, k := range i.ApprovalKinds() {
			if k.Kind == kind {
				return k, true
			}
		}
	}
	return ApprovalKind{}, false
}
