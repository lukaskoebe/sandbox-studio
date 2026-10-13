package login

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// sweep runs when Studio starts: the login VMs of an earlier process are removed, since no
// sign-in survives a restart, and the utility VMs are stopped; they start again when used.
func (s *Service) sweep(ctx context.Context) {
	rows, err := s.Store.SystemSandboxes(ctx)
	if err != nil {
		s.Log.Warn("listing system VMs", "err", err)
		return
	}
	for _, sb := range rows {
		switch sb.Kind {
		case store.SandboxKindClaudeLogin, store.SandboxKindCodexLogin:
			if err := s.VMs.RemoveSystem(ctx, sb.EnvironmentID, sb.Kind, sb.Name); err != nil {
				s.Log.Warn("removing a login VM left behind", "sandbox", sb.ID, "err", err)
			}
		case store.SandboxKindClaudeUtility, store.SandboxKindCodexUtility:
			if err := s.VMs.StopSystem(ctx, sb.EnvironmentID, sb.Kind, sb.Name); err != nil {
				s.Log.Warn("stopping a utility VM", "sandbox", sb.ID, "err", err)
			}
		}
	}
}
