package sandboxes

import (
	"context"
	"errors"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Rebase moves a sandbox onto another ready template in its environment. It copies the
// workspace into a new VM generation built from the template, with fresh Docker and
// workspace disks and the template's resources. Docker state and tmux sessions end. The
// sandbox keeps its identity and egress, and its checkpoints stay with their generations.
// A sandbox that was running is booted again; a stopped one stays stopped. Until the
// catalog switches, any failure removes the new VM and returns the source to its state.
func (m *Manager) Rebase(ctx context.Context, envID, id, templateID string) (View, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	rt, err := m.transfer()
	if err != nil {
		return View{}, err
	}
	resolved, err := m.templateResources(ctx, envID, templateID)
	if err != nil {
		return View{}, err
	}
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return View{}, err
	}
	if status != runtime.StatusRunning && status != runtime.StatusStopped {
		return View{}, ErrLifecycleState
	}
	image, err := m.templateImage(ctx, envID, templateID)
	if err != nil {
		return View{}, err
	}

	op, err := m.Store.BeginRebase(ctx, store.RebaseOperation{
		SandboxID: sb.ID, EnvironmentID: envID, TemplateID: templateID,
		CPUs: int(resolved.CPUs), MemoryMiB: int(resolved.MemoryMiB), MaxMemoryMiB: int(resolved.MaxMemoryMiB),
		WorkspaceMiB: int(resolved.WorkspaceMiB), DockerMiB: int(resolved.DockerMiB),
	})
	if err != nil {
		return View{}, err
	}
	wasRunning := status == runtime.StatusRunning
	if op.FromGeneration != sb.Generation {
		return View{}, m.rollbackRebase(ctx, sb, op, wasRunning, errors.New("sandbox generation changed"))
	}
	target := rebaseTarget(sb, op)
	if err := m.rebaseInto(ctx, rt, sb, target, image, wasRunning); err != nil {
		return View{}, m.rollbackRebase(ctx, sb, op, wasRunning, err)
	}

	if err := m.Store.CommitRebase(ctx, op); err != nil {
		cleanupCtx, cancel := cleanupContext(ctx)
		current, readErr := m.Store.Sandbox(cleanupCtx, envID, id)
		cancel()
		if readErr == nil && current.Generation == op.FromGeneration {
			return View{}, m.rollbackRebase(ctx, sb, op, wasRunning, err)
		}
		// The switch may have taken effect. Recovery finishes it; the target is never removed.
		return View{}, errors.Join(err, readErr)
	}
	cleanupCtx, cancel := cleanupContext(ctx)
	err = m.Runtime.Remove(cleanupCtx, VMName(sb))
	if err == nil {
		err = m.Store.EndRebase(cleanupCtx, op)
	}
	cancel()
	if err != nil {
		return View{}, fmt.Errorf("rebase done but cleanup is pending: %w", err)
	}
	return m.view(ctx, target)
}

func rebaseTarget(sb store.Sandbox, op store.RebaseOperation) store.Sandbox {
	sb.Generation, sb.TemplateID = op.ToGeneration, op.TemplateID
	sb.CPUs, sb.MemoryMiB, sb.MaxMemoryMiB = op.CPUs, op.MemoryMiB, op.MaxMemoryMiB
	sb.WorkspaceMiB, sb.DockerMiB = op.WorkspaceMiB, op.DockerMiB
	return sb
}

func (m *Manager) rebaseInto(ctx context.Context, rt transferRuntime, sb, target store.Sandbox, image *runtime.ImageSource, wasRunning bool) error {
	if wasRunning {
		if err := m.Runtime.Stop(ctx, VMName(sb)); err != nil {
			return err
		}
	}
	egress, err := m.Egress.Attach(sb)
	if err != nil {
		return err
	}
	// Drop the source agent's session; the target's agent connects on Boot.
	if err := m.resetHub(sb.ID); err != nil {
		return err
	}
	if err := rt.StartForTransfer(ctx, VMName(sb)); err != nil {
		return err
	}
	sock := m.Paths.AgentSocket(sb.ID)
	if err := rt.CreateForTransfer(ctx, VMName(target), transferSpec(target, image, egress), sock, sandboxLabels(target)); err != nil {
		return err
	}
	if err := copyWorkspace(ctx, rt, ownedVM(sb), ownedVM(target), int64(target.WorkspaceMiB)<<20); err != nil {
		return err
	}
	if err := m.Runtime.Stop(ctx, VMName(sb)); err != nil {
		return err
	}
	if wasRunning {
		return rt.Boot(ctx, VMName(target))
	}
	return m.Runtime.Stop(ctx, VMName(target))
}

// rollbackRebase removes the target VM, stops the source and ends the record. The record
// stays for recovery if any of that fails. A source that was running is then started
// again through the normal start path.
func (m *Manager) rollbackRebase(ctx context.Context, sb store.Sandbox, op store.RebaseOperation, wasRunning bool, cause error) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	if err := m.Runtime.Remove(cleanupCtx, vmNameAtGeneration(sb, op.ToGeneration)); err != nil {
		return errors.Join(cause, fmt.Errorf("target cleanup failed; rebase recovery retained: %w", err))
	}
	if err := m.Runtime.Stop(cleanupCtx, vmNameAtGeneration(sb, op.FromGeneration)); err != nil {
		return errors.Join(cause, fmt.Errorf("source stop failed; rebase recovery retained: %w", err))
	}
	current, err := m.Store.Sandbox(cleanupCtx, sb.EnvironmentID, sb.ID)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("catalog recovery check failed; rebase recovery retained: %w", err))
	}
	if current.Generation != op.FromGeneration {
		return errors.Join(cause, errors.New("catalog changed during rebase; rebase recovery retained"))
	}
	if err := m.Store.EndRebase(cleanupCtx, op); err != nil {
		return errors.Join(cause, fmt.Errorf("rebase record cleanup failed: %w", err))
	}
	if err := m.resetHub(sb.ID); err != nil {
		return errors.Join(cause, fmt.Errorf("agent listener recovery failed: %w", err))
	}
	if !wasRunning {
		return cause
	}
	if _, err := m.Egress.Attach(current); err != nil {
		return errors.Join(cause, fmt.Errorf("source restart failed: %w", err))
	}
	if err := m.Runtime.Start(cleanupCtx, VMName(current)); err != nil {
		return errors.Join(cause, fmt.Errorf("source restart failed: %w", err))
	}
	return cause
}

// recoverRebase finishes an interrupted rebase from its record: before the catalog switch
// it removes the target and stops the source, after it it removes the source.
func (m *Manager) recoverRebase(ctx context.Context, sb store.Sandbox, op store.RebaseOperation) error {
	if op.SandboxID != sb.ID || op.EnvironmentID != sb.EnvironmentID || op.ToGeneration != op.FromGeneration+1 {
		return errors.New("rebase record does not match sandbox")
	}
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	switch sb.Generation {
	case op.FromGeneration:
		if err := m.Runtime.Remove(cleanupCtx, vmNameAtGeneration(sb, op.ToGeneration)); err != nil {
			return err
		}
		if err := m.Runtime.Stop(cleanupCtx, vmNameAtGeneration(sb, op.FromGeneration)); err != nil {
			return err
		}
	case op.ToGeneration:
		if err := m.Runtime.Remove(cleanupCtx, vmNameAtGeneration(sb, op.FromGeneration)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("catalog generation %d is neither rebase source %d nor target %d", sb.Generation, op.FromGeneration, op.ToGeneration)
	}
	return m.Store.EndRebase(cleanupCtx, op)
}

// ensureSettled finishes any interrupted restore or rebase of a sandbox before another
// lifecycle change. It fails with ErrBusy while recovery cannot complete.
func (m *Manager) ensureSettled(ctx context.Context, envID, id, deletingCheckpointID string) error {
	if err := m.ensureNoRestore(ctx, envID, id, deletingCheckpointID); err != nil {
		return err
	}
	ops, err := m.Store.Rebases(ctx)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.SandboxID != id {
			continue
		}
		if op.EnvironmentID != envID {
			return store.ErrNotFound
		}
		sb, err := m.Store.Sandbox(ctx, envID, id)
		if err != nil {
			return err
		}
		if err := m.recoverRebase(ctx, sb, op); err != nil {
			return fmt.Errorf("%w: rebase recovery is pending: %v", ErrBusy, err)
		}
	}
	return nil
}
