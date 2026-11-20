package lead

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Lead settles lead.plan approvals: Allow carries out the plan as it stands, Deny and Dismiss
// cancel it. It serves no hosts, tools or jobs.

func (s *Service) ID() string                    { return "lead" }
func (s *Service) Routes() []gateway.VirtualHost { return nil }
func (s *Service) Tools() []integrations.Tool    { return nil }
func (s *Service) Jobs() []integrations.Job      { return nil }
func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{{Kind: ApprovalKind, Decide: s.decide}}
}

func (s *Service) decide(ctx context.Context, a store.Approval, d integrations.Decision) error {
	switch d.Action {
	case integrations.Allow:
		return s.Approve(ctx, a)
	case integrations.Deny:
		return s.Refuse(ctx, a, store.StatusDenied)
	case integrations.Dismiss:
		return s.Refuse(ctx, a, store.StatusDismissed)
	}
	return integrations.ErrAction
}
