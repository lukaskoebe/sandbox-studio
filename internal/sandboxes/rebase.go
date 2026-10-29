package sandboxes

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Rebase moves a sandbox onto another ready template in its environment, or onto the default
// image, the base new sandboxes get, when templateID is empty. It copies the workspace into a
// new VM generation built from the template, with a fresh Docker disk and the template's
// resources; onto the default image the sandbox keeps its resources. The home comes along
// when the target has a home disk, as the default image does: a
// sandbox with one can't move to a legacy template, which has none. Docker state and tmux
// sessions end. The sandbox keeps its identity and egress, and its checkpoints stay with
// their generations.
// A sandbox that was running is booted again; a stopped one stays stopped. Until the
// catalog switches, any failure removes the new VM and returns the source to its state.
//
// Rebase checks the request and answers at once with the sandbox in state creating. The
// rebase runs in the background, holding the sandbox, and publishes its steps as a fork
// does; Wait returns its outcome. A failed rebase leaves the sandbox on its old template,
// and its view keeps the step that failed until the next rebase, ClearProgress or a delete.
func (m *Manager) Rebase(ctx context.Context, envID, id, templateID string) (View, error) {
	return m.startRebase(ctx, envID, id, OperationRebase, func(sb store.Sandbox) (store.RebaseOperation, error) {
		resolved := resources.Resources{
			CPUs: int64(sb.CPUs), MemoryMiB: int64(sb.MemoryMiB), MaxMemoryMiB: int64(sb.MaxMemoryMiB),
			WorkspaceMiB: int64(sb.WorkspaceMiB), DockerMiB: int64(sb.DockerMiB), HomeMiB: int64(sb.HomeMiB),
		}
		if templateID != "" {
			var err error
			if resolved, err = m.templateResources(ctx, envID, templateID); err != nil {
				return store.RebaseOperation{}, err
			}
			// A template that names no home size keeps the sandbox's.
			resolved.HomeMiB = cmp.Or(resolved.HomeMiB, int64(sb.HomeMiB))
		}
		// The home disk moves with the sandbox only onto a template that has one. A sandbox with
		// a home disk is refused rather than losing its home.
		targetHome, err := m.homeFor(ctx, envID, templateID, resolved.Home())
		if err != nil {
			return store.RebaseOperation{}, err
		}
		if sb.HomeMiB > 0 && targetHome == 0 {
			return store.RebaseOperation{}, ErrTemplateWithoutHome
		}
		return store.RebaseOperation{
			Kind: store.RebaseTemplate, SandboxID: sb.ID, EnvironmentID: envID, TemplateID: templateID,
			CPUs: int(resolved.CPUs), MemoryMiB: int(resolved.MemoryMiB), MaxMemoryMiB: int(resolved.MaxMemoryMiB),
			WorkspaceMiB: int(resolved.WorkspaceMiB), DockerMiB: int(resolved.DockerMiB), HomeMiB: targetHome,
			BaseImage: m.BaseImage, BaseVersion: m.BaseVersion,
		}, nil
	})
}

// startRebase runs a rebase or a disk resize, operation, of a running or stopped sandbox:
// plan says where it goes, and the rest is the same.
func (m *Manager) startRebase(ctx context.Context, envID, id, operation string, plan func(store.Sandbox) (store.RebaseOperation, error)) (View, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			unlock()
		}
	}()
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
	if m.failedCreation(id) {
		return View{}, lifecycleError("the sandbox could not be created; delete it")
	}
	rt, err := m.transfer()
	if err != nil {
		return View{}, err
	}
	req, err := plan(sb)
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
	var image *runtime.ImageSource // the default image
	if req.TemplateID != "" {
		if image, err = m.templateImage(ctx, envID, req.TemplateID); err != nil {
			return View{}, err
		}
	}
	wasRunning := status == runtime.StatusRunning
	steps := rebaseStoppedSteps
	if wasRunning {
		steps = rebaseRunningSteps
	}
	if err := m.begin(ctx, sb, operation, steps); err != nil {
		return View{}, err
	}
	handedOff = true
	timeout := transferTimeout
	if req.Kind == store.RebaseDisks {
		timeout = diskResizeTimeout
	}
	m.background(sb, timeout, unlock, func(ctx context.Context) error {
		return m.rebase(ctx, rt, sb, req, image, wasRunning)
	})
	return m.acceptedView(ctx, sb), nil
}

// rebase runs a rebase that Rebase accepted: it records it durably, builds and fills the
// new generation and switches the catalog to it, or rolls back.
func (m *Manager) rebase(ctx context.Context, rt transferRuntime, sb store.Sandbox, req store.RebaseOperation, image *runtime.ImageSource, wasRunning bool) error {
	op, err := m.Store.BeginRebase(ctx, req)
	if err != nil {
		return err
	}
	if op.FromGeneration != sb.Generation {
		return m.rollbackRebase(ctx, sb, op, wasRunning, errors.New("sandbox generation changed"))
	}
	target := rebaseTarget(sb, op)
	if err := m.rebaseInto(ctx, rt, sb, target, image, wasRunning, op.Kind == store.RebaseDisks); err != nil {
		return m.rollbackRebase(ctx, sb, op, wasRunning, err)
	}

	if err := m.Store.CommitRebase(ctx, op); err != nil {
		cleanupCtx, cancel := cleanupContext(ctx)
		current, readErr := m.Store.Sandbox(cleanupCtx, sb.EnvironmentID, sb.ID)
		cancel()
		if readErr == nil && current.Generation == op.FromGeneration {
			return m.rollbackRebase(ctx, sb, op, wasRunning, err)
		}
		// The switch may have taken effect. Recovery finishes it; the target is never removed.
		return errors.Join(err, readErr)
	}
	if wasRunning {
		m.markStarted(ctx, target)
	}
	cleanupCtx, cancel := cleanupContext(ctx)
	err = m.Runtime.Remove(cleanupCtx, VMName(sb))
	if err == nil {
		err = m.Store.EndRebase(cleanupCtx, op)
	}
	cancel()
	if err != nil {
		return fmt.Errorf("rebase done but cleanup is pending: %w", err)
	}
	return nil
}

func rebaseTarget(sb store.Sandbox, op store.RebaseOperation) store.Sandbox {
	sb.Generation, sb.TemplateID = op.ToGeneration, op.TemplateID
	sb.BaseImage, sb.BaseVersion = op.BaseImage, op.BaseVersion
	sb.CPUs, sb.MemoryMiB, sb.MaxMemoryMiB = op.CPUs, op.MemoryMiB, op.MaxMemoryMiB
	sb.WorkspaceMiB, sb.DockerMiB, sb.HomeMiB = op.WorkspaceMiB, op.DockerMiB, op.HomeMiB
	return sb
}

// rebaseInto builds and fills target, copying Docker's data too when docker is set.
func (m *Manager) rebaseInto(ctx context.Context, rt transferRuntime, sb, target store.Sandbox, image *runtime.ImageSource, wasRunning, docker bool) error {
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
	m.advance(sb.ID, StepPull)
	sock := m.Paths.AgentSocket(sb.ID)
	spec := transferSpec(target, image, egress)
	spec.Progress = m.runtimeProgress(sb.ID)
	if err := rt.CreateForTransfer(ctx, VMName(target), spec, sock, sandboxLabels(target)); err != nil {
		return err
	}
	m.advance(sb.ID, StepCopy)
	if err := copyTrees(ctx, rt, ownedVM(sb), ownedVM(target), target.WorkspaceMiB, target.HomeMiB); err != nil {
		return err
	}
	if docker {
		limit := &limitWriter{left: int64(target.DockerMiB) << 20, err: ErrDockerTooLarge}
		if err := copyTree(ctx, rt, ownedVM(sb), ownedVM(target), runtime.TreeDocker, limit); err != nil {
			return err
		}
	}
	if err := m.Runtime.Stop(ctx, VMName(sb)); err != nil {
		return err
	}
	if wasRunning {
		m.advance(sb.ID, StepBoot)
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
	m.markStarted(cleanupCtx, current)
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
