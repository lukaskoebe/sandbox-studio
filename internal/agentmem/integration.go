package agentmem

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The service is an integration only to settle memory.share and memory.conflict
// approvals; it serves no hosts.
var _ integrations.Integration = (*Service)(nil)

func (s *Service) ID() string                    { return "memory" }
func (s *Service) Routes() []gateway.VirtualHost { return nil }

func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{{Kind: ApprovalKind, Decide: s.decide}, {Kind: ConflictApprovalKind, Decide: s.decideConflict}}
}

// decide settles a memory.share request: DecideShare marks it decided together with the
// write, so a failed write leaves it pending and a second click can't write it twice.
func (s *Service) decide(ctx context.Context, a store.Approval, d integrations.Decision) error {
	status := map[string]string{integrations.Allow: store.StatusApproved, integrations.Deny: store.StatusDenied,
		integrations.Dismiss: store.StatusDismissed}[d.Action]
	if status == "" {
		return integrations.ErrAction
	}
	if _, err := s.DecideShare(ctx, a, status); err != nil {
		return err
	}
	if s.Notify != nil {
		s.Notify(a.EnvironmentID)
	}
	return nil
}
