package sandboxes

import (
	"context"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// DeleteEnvironment removes every computer of an environment, of every kind (a teammate's
// browser, logins, builds), with its VM, disks and checkpoints, then calls remove for the rest
// while it still holds all of them, so none starts or forks in between. It refuses while one of
// them is busy, a template build is queued or running, or an import runs.
func (m *Manager) DeleteEnvironment(ctx context.Context, envID string, remove func(context.Context) error) error {
	all, err := m.Store.Sandboxes(ctx, envID)
	if err != nil {
		return err
	}
	var unlocks []func()
	defer func() {
		for _, u := range unlocks {
			u()
		}
	}()
	for _, sb := range all {
		unlock, err := m.tryMutation(sb.ID)
		if err != nil {
			return fmt.Errorf("%w: %s is busy; wait for what it is doing to finish", ErrBusy, sb.Name)
		}
		unlocks = append(unlocks, unlock)
	}
	if m.imports.running() > 0 {
		return fmt.Errorf("%w: a computer is being imported", ErrBusy)
	}
	jobs, err := m.Store.RecoverableBuildJobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		// Terminal jobs come back while their VM awaits cleanup, which the removal below does.
		if j.EnvironmentID == envID && j.Status != store.BuildReady && j.Status != store.BuildFailed && j.Status != store.BuildCancelled {
			return fmt.Errorf("%w: a template is being built", ErrBusy)
		}
	}
	for _, sb := range all {
		if err := m.ensureSettled(ctx, envID, sb.ID, ""); err != nil {
			return err
		}
	}
	for _, sb := range all {
		checkpoints, err := m.Store.Checkpoints(ctx, envID, sb.ID)
		if err != nil {
			return err
		}
		if err := m.deleteCheckpointsLocked(ctx, envID, sb.ID, checkpoints); err != nil {
			return err
		}
		if err := m.Runtime.Remove(ctx, VMName(sb)); err != nil {
			return fmt.Errorf("remove %s: %w", sb.Name, err)
		}
		m.Hub.Close(sb.ID)
		m.Egress.Detach(sb.ID)
	}
	if err := remove(ctx); err != nil {
		return err
	}
	for _, sb := range all {
		m.forgetProgress(ctx, sb.ID)
		m.forget(envID, sb.ID)
		m.publish(envID, sb.ID)
	}
	return nil
}
