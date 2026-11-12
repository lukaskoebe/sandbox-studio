package sandboxes

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// SetTeammate gives a computer another owner, or with "" none; the caller checks the teammate
// is the environment's. The computer has to be stopped: the teammate's rules, secrets and
// harness files follow the owner, and Studio applies them when the computer next connects, so
// nothing running keeps the old owner's. The home stays as it is, the old owner's harness history
// and logins included.
func (m *Manager) SetTeammate(ctx context.Context, envID, id, teammateID string) (View, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	if m.failedCreation(id) {
		return View{}, lifecycleError("the sandbox could not be created; delete it")
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if sb.TeammateID == teammateID {
		return m.Get(ctx, envID, id)
	}
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return View{}, err
	}
	if status != runtime.StatusStopped {
		return View{}, lifecycleError("stop the computer to change its teammate")
	}
	if err := m.Store.SetSandboxTeammate(ctx, envID, id, teammateID); err != nil {
		return View{}, err
	}
	m.publish(envID, id)
	return m.Get(ctx, envID, id)
}
