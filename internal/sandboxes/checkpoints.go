package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

var (
	// ErrBusy means another lifecycle mutation is active or a restore needs recovery.
	ErrBusy = errors.New("sandbox is busy")
	// ErrCheckpointState means an operation is not supported in the sandbox's current state.
	ErrCheckpointState = errors.New("checkpoint operation is not supported in this sandbox state")
	// ErrCheckpointName means a checkpoint name is empty, too long, or contains controls.
	ErrCheckpointName = errors.New("checkpoint name must be 1 to 80 characters without control characters")
)

const cleanupTimeout = 30 * time.Second

// Checkpoints lists the disk checkpoints of a sandbox. In-progress entries are visible so
// callers can identify a create or delete that needs retrying.
func (m *Manager) Checkpoints(ctx context.Context, envID, id string) ([]store.Checkpoint, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return nil, err
	}
	return m.Store.Checkpoints(ctx, envID, id)
}

// CreateCheckpoint captures the current sandbox disks, of a running or a stopped sandbox.
func (m *Manager) CreateCheckpoint(ctx context.Context, envID, id, name string) (store.Checkpoint, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return store.Checkpoint{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return store.Checkpoint{}, err
	}
	defer unlock()

	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return store.Checkpoint{}, err
	}
	if err := m.ensureNoRestore(ctx, envID, id, ""); err != nil {
		return store.Checkpoint{}, err
	}
	sb, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return store.Checkpoint{}, err
	}
	name, err = validateCheckpointName(name)
	if err != nil {
		return store.Checkpoint{}, err
	}
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return store.Checkpoint{}, err
	}
	if status != runtime.StatusStopped && status != runtime.StatusRunning {
		return store.Checkpoint{}, ErrCheckpointState
	}

	checkpoint, err := m.Store.CreateCheckpoint(ctx, envID, id, name)
	if err != nil {
		return store.Checkpoint{}, err
	}
	if err := m.Runtime.CreateCheckpoint(ctx, VMName(sb), sb.ID, checkpoint.ID); err != nil {
		return store.Checkpoint{}, m.rollbackCheckpointCreate(ctx, envID, id, checkpoint.ID, err)
	}
	if err := m.Store.SetCheckpointState(ctx, envID, id, checkpoint.ID, store.CheckpointStateReady); err != nil {
		publishCtx, cancel := cleanupContext(ctx)
		publishErr := m.Store.SetCheckpointState(publishCtx, envID, id, checkpoint.ID, store.CheckpointStateReady)
		cancel()
		if publishErr != nil {
			return checkpoint, fmt.Errorf("checkpoint %q was captured but ready-state publication failed; its creating record and artifact are retained: %w", checkpoint.Name, errors.Join(err, publishErr))
		}
	}
	checkpoint.State = store.CheckpointStateReady
	return checkpoint, nil
}

// DeleteCheckpoint removes the runtime snapshot before deleting its catalog record. A
// failed runtime removal leaves the row in deleting state so the caller can retry.
func (m *Manager) DeleteCheckpoint(ctx context.Context, envID, id, checkpointID string) error {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	if err := m.ensureNoRestore(ctx, envID, id, checkpointID); err != nil {
		return err
	}
	return m.deleteCheckpointLocked(ctx, envID, id, checkpointID)
}

func (m *Manager) deleteCheckpointLocked(ctx context.Context, envID, id, checkpointID string) error {
	cp, err := m.Store.Checkpoint(ctx, envID, id, checkpointID)
	if err != nil {
		return err
	}
	if err := m.Store.SetCheckpointState(ctx, envID, id, checkpointID, store.CheckpointStateDeleting); err != nil {
		return err
	}
	if err := m.Runtime.RemoveCheckpoint(ctx, id, checkpointID); err != nil {
		// A checkpoint that newer ones depend on was left as it was, so it isn't half deleted.
		if errors.Is(err, runtime.ErrCheckpointInUse) {
			resetCtx, cancel := cleanupContext(ctx)
			defer cancel()
			return errors.Join(err, m.Store.SetCheckpointState(resetCtx, envID, id, checkpointID, cp.State))
		}
		return err
	}
	return m.Store.DeleteCheckpoint(ctx, envID, id, checkpointID)
}

// deleteCheckpointsLocked removes every tracked artifact while respecting runtime snapshot
// dependencies. A failed parent is retried after any children removed in the same pass.
func (m *Manager) deleteCheckpointsLocked(ctx context.Context, envID, id string, checkpoints []store.Checkpoint) error {
	pending := checkpoints
	for len(pending) > 0 {
		remaining := make([]store.Checkpoint, 0, len(pending))
		var failures []error
		progress := false
		for _, checkpoint := range pending {
			if err := m.deleteCheckpointLocked(ctx, envID, id, checkpoint.ID); err != nil {
				remaining = append(remaining, checkpoint)
				failures = append(failures, fmt.Errorf("delete checkpoint %q: %w", checkpoint.Name, err))
				continue
			}
			progress = true
		}
		if len(remaining) == 0 {
			return nil
		}
		if !progress {
			return errors.Join(failures...)
		}
		pending = remaining
	}
	return nil
}

// RestoreCheckpoint restores a checkpoint into a new VM generation. The source must
// already be stopped; it remains intact until the stopped candidate is adopted in the
// catalog.
func (m *Manager) RestoreCheckpoint(ctx context.Context, envID, id, checkpointID string) (View, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()

	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if err := m.ensureNoRestore(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	sb, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	checkpoint, err := m.Store.Checkpoint(ctx, envID, id, checkpointID)
	if err != nil {
		return View{}, err
	}
	if checkpoint.State != store.CheckpointStateReady {
		return View{}, ErrCheckpointState
	}
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return View{}, err
	}
	// A paused/crashed/absent source is not accepted: restoration never stops or replaces
	// a live source, and this path requires an existing, explicitly stopped VM to preserve.
	if status != runtime.StatusStopped {
		return View{}, ErrCheckpointState
	}
	if !m.Runtime.CheckpointRestoreSupported() {
		return View{}, runtime.ErrCheckpointRestoreUnavailable
	}

	op, err := m.Store.BeginRestore(ctx, envID, id, checkpointID)
	if err != nil {
		return View{}, err
	}
	if op.FromGeneration != sb.Generation || op.ToGeneration != op.FromGeneration+1 ||
		op.SandboxID != sb.ID || op.EnvironmentID != envID || op.CheckpointID != checkpointID {
		return View{}, fmt.Errorf("invalid restore operation recorded for sandbox %s", sb.ID)
	}
	candidateName := vmNameAtGeneration(sb, op.ToGeneration)

	// Keep the sandbox's egress identity and the agent endpoint ready before restoring.
	egress, err := m.Egress.Attach(sb)
	if err != nil {
		return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
	}
	if err := m.resetHub(sb.ID); err != nil {
		return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
	}
	if err := m.Runtime.RestoreCheckpoint(ctx, sb.ID, checkpointID, candidateName, egress); err != nil {
		return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
	}

	candidateStatus, err := m.Runtime.Status(ctx, candidateName)
	if err != nil {
		return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
	}
	if candidateStatus != runtime.StatusStopped {
		if err := m.Runtime.Stop(ctx, candidateName); err != nil {
			return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
		}
		candidateStatus, err = m.Runtime.Status(ctx, candidateName)
		if err != nil {
			return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
		}
		if candidateStatus != runtime.StatusStopped {
			return View{}, m.rollbackRestore(ctx, sb, op, candidateName, ErrCheckpointState)
		}
	}
	// Drop any candidate session that connected during SDK restoration, then leave the
	// listener available for a later Start.
	if err := m.resetHub(sb.ID); err != nil {
		return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
	}

	if err := m.Store.CommitRestore(ctx, op); err != nil {
		cleanupCtx, cancel := cleanupContext(ctx)
		current, readErr := m.Store.Sandbox(cleanupCtx, envID, id)
		cancel()
		if readErr == nil && current.Generation == op.FromGeneration {
			return View{}, m.rollbackRestore(ctx, sb, op, candidateName, err)
		}
		// If the CAS took effect despite returning an error, keep the candidate and let
		// recovery finish source cleanup. Never risk deleting the adopted generation.
		m.Hub.Close(sb.ID)
		return View{}, errors.Join(err, readErr)
	}

	// Keep the adopted candidate disconnected until source cleanup and EndRestore both
	// succeed. Start will reopen the listener after this operation is complete.
	m.Hub.Close(sb.ID)
	cleanupCtx, cancel := cleanupContext(ctx)
	err = m.Runtime.Remove(cleanupCtx, vmNameAtGeneration(sb, op.FromGeneration))
	if err == nil {
		err = m.Store.EndRestore(cleanupCtx, op)
	}
	cancel()
	if err != nil {
		return View{}, fmt.Errorf("restore adopted but cleanup is pending: %w", err)
	}
	if err := m.Hub.Listen(sb.ID, m.Paths.AgentSocket(sb.ID)); err != nil {
		return View{}, err
	}

	adopted := sb
	adopted.Generation = op.ToGeneration
	return m.view(ctx, adopted)
}

// tryMutation claims a sandbox for one lifecycle change, or fails with ErrBusy. It is separate
// from the configuration lock, so a slow configuration push never blocks Stop or Delete.
func (m *Manager) tryMutation(id string) (func(), error) {
	v, _ := m.mutating.LoadOrStore(id, new(sync.Mutex))
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, ErrBusy
	}
	return mu.Unlock, nil
}

func (m *Manager) ensureNoRestore(ctx context.Context, envID, id, deletingCheckpointID string) error {
	ops, err := m.Store.Restores(ctx)
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
		if deletingCheckpointID != "" && op.CheckpointID == deletingCheckpointID {
			return store.ErrConflict
		}
		sb, err := m.Store.Sandbox(ctx, envID, id)
		if err != nil {
			return err
		}
		if err := m.recoverRestore(ctx, sb, op); err != nil {
			return fmt.Errorf("%w: restore recovery is pending: %v", ErrBusy, err)
		}
		return nil
	}
	return nil
}

func (m *Manager) recoverRestore(ctx context.Context, sb store.Sandbox, op store.RestoreOperation) error {
	m.Hub.Close(sb.ID)
	if op.SandboxID != sb.ID || op.EnvironmentID != sb.EnvironmentID || op.ToGeneration != op.FromGeneration+1 {
		return errors.New("restore record does not match sandbox generation")
	}
	var removeName string
	switch sb.Generation {
	case op.FromGeneration:
		removeName = vmNameAtGeneration(sb, op.ToGeneration) // Candidate was never adopted.
	case op.ToGeneration:
		removeName = vmNameAtGeneration(sb, op.FromGeneration) // Candidate is catalog-authoritative.
	default:
		return fmt.Errorf("catalog generation %d is neither restore source %d nor target %d", sb.Generation, op.FromGeneration, op.ToGeneration)
	}

	cleanupCtx, cancel := cleanupContext(ctx)
	if err := m.Runtime.Remove(cleanupCtx, removeName); err != nil {
		cancel()
		return err
	}
	if err := m.Store.EndRestore(cleanupCtx, op); err != nil {
		cancel()
		return err
	}
	cancel()
	return nil
}

func (m *Manager) rollbackCheckpointCreate(ctx context.Context, envID, id, checkpointID string, cause error) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	if err := m.Runtime.RemoveCheckpoint(cleanupCtx, id, checkpointID); err != nil {
		return errors.Join(cause, fmt.Errorf("checkpoint cleanup failed; creating record retained: %w", err))
	}
	if err := m.Store.DeleteCheckpoint(cleanupCtx, envID, id, checkpointID); err != nil {
		return errors.Join(cause, fmt.Errorf("checkpoint row cleanup failed: %w", err))
	}
	return cause
}

func (m *Manager) rollbackRestore(ctx context.Context, sb store.Sandbox, op store.RestoreOperation, candidateName string, cause error) error {
	m.Hub.Close(sb.ID)
	cleanupCtx, cancel := cleanupContext(ctx)
	removeErr := m.Runtime.Remove(cleanupCtx, candidateName)
	if removeErr != nil {
		cancel()
		return errors.Join(cause, fmt.Errorf("candidate cleanup failed; restore recovery retained: %w", removeErr))
	}
	current, err := m.Store.Sandbox(cleanupCtx, sb.EnvironmentID, sb.ID)
	if err != nil {
		cancel()
		return errors.Join(cause, fmt.Errorf("catalog recovery check failed; restore recovery retained: %w", err))
	}
	if current.Generation != op.FromGeneration {
		cancel()
		return errors.Join(cause, errors.New("catalog changed during restore; restore recovery retained"))
	}
	if err := m.Store.EndRestore(cleanupCtx, op); err != nil {
		cancel()
		return errors.Join(cause, fmt.Errorf("restore record cleanup failed: %w", err))
	}
	cancel()
	if err := m.Hub.Listen(sb.ID, m.Paths.AgentSocket(sb.ID)); err != nil {
		return errors.Join(cause, fmt.Errorf("agent listener recovery failed: %w", err))
	}
	return cause
}

func (m *Manager) resetHub(id string) error {
	m.Hub.Close(id)
	return m.Hub.Listen(id, m.Paths.AgentSocket(id))
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

func validateCheckpointName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return "", ErrCheckpointName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrCheckpointName
		}
	}
	return name, nil
}
