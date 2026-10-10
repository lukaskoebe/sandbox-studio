package sandboxes

import (
	"context"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// resumeAgentWait bounds how long Resume waits for the guest agent to reconnect. The wait
// is best-effort: a slow reconnect shows up as a booting sandbox, not as an error.
var resumeAgentWait = 15 * time.Second

// lifecycleError is ErrLifecycleState with an operation-specific message.
type lifecycleError string

func (e lifecycleError) Error() string        { return string(e) }
func (e lifecycleError) Is(target error) bool { return target == ErrLifecycleState }

// Suspend pauses a running sandbox in place: its vCPUs stop, its memory stays in the host
// process, and Resume continues every process where it was. It is not a snapshot, so a
// suspended sandbox does not survive a host reboot. Fork, rebase and checkpoints refuse a
// suspended sandbox; Stop and Delete resume it first.
func (m *Manager) Suspend(ctx context.Context, envID, id string) (View, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	status, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		return View{}, err
	}
	if status != runtime.StatusRunning {
		return View{}, lifecycleError("only a running sandbox can be suspended")
	}
	if err := m.Runtime.Pause(ctx, VMName(rec)); err != nil {
		return View{}, err
	}
	// A frozen guest cannot answer: drop its session now so terminals end at once and
	// requests fail fast. The listener stays, and the agent reconnects after Resume.
	m.Hub.Disconnect(rec.ID)
	return m.view(ctx, rec)
}

// Resume continues a suspended sandbox and waits briefly for its agent to reconnect.
func (m *Manager) Resume(ctx context.Context, envID, id string) (View, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	status, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		return View{}, err
	}
	if status != runtime.StatusSuspended {
		return View{}, lifecycleError("only a suspended sandbox can be resumed")
	}
	// Studio may have restarted while the sandbox was suspended; make sure the agent and
	// network endpoints exist before the guest wakes up.
	if _, err := m.Egress.Attach(rec); err != nil {
		return View{}, err
	}
	if err := m.Hub.Listen(rec.ID, m.Paths.AgentSocket(rec.ID)); err != nil {
		return View{}, err
	}
	if err := m.Runtime.Resume(ctx, VMName(rec)); err != nil {
		return View{}, err
	}
	wait, cancel := context.WithTimeout(ctx, resumeAgentWait)
	if err := m.Hub.WaitConnected(wait, rec.ID); err != nil {
		m.Log.Warn("agent did not reconnect after resume yet", "sandbox", rec.ID, "err", err)
	}
	cancel()
	return m.view(ctx, rec)
}
