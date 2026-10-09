package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

var ErrCheckpointRestoreUnavailable = errors.New("checkpoint restore is unavailable with this microsandbox version because it loses systemd initialization")

// ErrCheckpointInUse means a newer indexed checkpoint depends on this one.
// Delete dependent checkpoints first; Studio never forces removal of a parent.
var ErrCheckpointInUse = errors.New("checkpoint has dependent newer checkpoints; delete those checkpoints first")

// CheckpointRestoreSupported reports whether this runtime preserves systemd initialization
// while restoring a disk snapshot. Keep restore disabled for SDK v0.7.7 until upstream
// issue #1676 is fixed: https://github.com/superradcompany/microsandbox/issues/1676.
func (*Runtime) CheckpointRestoreSupported() bool { return false }

// CreateCheckpoint captures the disk state of a sandbox into its deterministic
// snapshot group. The manager requires a stopped source because SDK guest flushing
// cannot freeze all workloads in Studio's systemd PID 1 guest; automatic guest
// flushing stays enabled.
// Force is deliberately false so a retry cannot replace an existing artifact.
func (r *Runtime) CreateCheckpoint(ctx context.Context, vmName, sandboxID, checkpointID string) error {
	_, err := msb.Snapshot.Create(ctx, msb.SnapshotCreateOptions{
		FromSandbox: vmName,
		Group:       checkpointGroup(sandboxID),
		Name:        checkpointID,
		Full:        false,
		Force:       false,
	})
	if err != nil {
		return fmt.Errorf("create checkpoint: %w", err)
	}
	return nil
}

// RemoveCheckpoint removes one exact checkpoint. It refuses to remove a
// checkpoint with indexed children. If the target is the group head and has a
// surviving member, it first moves the head to the target's parent (or another
// member in the same Studio-owned group), then removes the target without force.
// A singleton head is removed directly; the SDK clears that group head.
func (r *Runtime) RemoveCheckpoint(ctx context.Context, sandboxID, checkpointID string) error {
	group := checkpointGroup(sandboxID)
	handle, err := msb.Snapshot.Get(ctx, checkpointSelector(sandboxID, checkpointID))
	if err != nil {
		if msb.IsKind(err, msb.ErrSnapshotNotFound) {
			return nil
		}
		return fmt.Errorf("look up checkpoint for removal: %w", err)
	}
	if handle == nil {
		return errors.New("look up checkpoint for removal: SDK returned an empty handle")
	}
	handleGroup := handle.Group()
	if handleGroup == nil || *handleGroup != group {
		return errors.New("look up checkpoint for removal: checkpoint is outside its Studio-owned snapshot group")
	}

	indexed, err := msb.Snapshot.List(ctx)
	if err != nil {
		return fmt.Errorf("list indexed checkpoints for removal: %w", err)
	}
	records, err := checkpointRecords(indexed)
	if err != nil {
		return fmt.Errorf("inspect indexed checkpoints for removal: %w", err)
	}
	parentID, err := checkpointRemovalParent(records, group, handle.ID())
	if err != nil {
		return err
	}

	currentHead, err := msb.Snapshot.GroupHead(ctx, group)
	if err != nil {
		return fmt.Errorf("read checkpoint group head: %w", err)
	}
	if currentHead == nil || currentHead.Group != group {
		return errors.New("read checkpoint group head: SDK returned an invalid group head")
	}

	nextHead := checkpointRemovalHead(records, group, handle.ID(), parentID, currentHead.Head)
	headMoved := false
	if nextHead != "" {
		if err := selectCheckpointGroupHead(ctx, group, nextHead); err != nil {
			return fmt.Errorf("move checkpoint group head before removal: %w", err)
		}
		headMoved = true
	}

	// force=false is required here: SDK removal will continue to reject a parent
	// if a child appeared after the list above (for example, through another SDK
	// process). Never trade the integrity of a surviving checkpoint for cleanup.
	if err := handle.Remove(ctx, false); err != nil {
		if msb.IsKind(err, msb.ErrSnapshotNotFound) {
			return nil
		}
		removeErr := fmt.Errorf("remove checkpoint: %w", err)
		// A different SDK process can publish a child after our preflight list.
		// Re-list after the SDK's non-forced refusal so that this race still
		// reaches callers as the typed dependency conflict.
		if latest, listErr := msb.Snapshot.List(ctx); listErr == nil {
			if latestRecords, recordsErr := checkpointRecords(latest); recordsErr == nil {
				removeErr = checkpointRemovalError(latestRecords, group, handle.ID(), err)
			}
		}
		if !headMoved {
			return removeErr
		}

		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		rollbackErr := restoreCheckpointGroupHead(rollbackCtx, group, nextHead, handle.ID())
		cancel()
		if rollbackErr != nil {
			return errors.Join(removeErr, fmt.Errorf("restore checkpoint group head after failed removal: %w", rollbackErr))
		}
		return removeErr
	}
	return nil
}

type checkpointRecord struct {
	id       string
	group    string
	parentID string
}

func checkpointRecords(handles []*msb.SnapshotHandle) ([]checkpointRecord, error) {
	records := make([]checkpointRecord, 0, len(handles))
	for _, handle := range handles {
		if handle == nil {
			return nil, errors.New("SDK returned an empty indexed checkpoint")
		}
		record := checkpointRecord{id: handle.ID()}
		if group := handle.Group(); group != nil {
			record.group = *group
		}
		// The v0.7.7 field is named ParentDigest but contains the parent snapshot
		// ID, which is also the member selector accepted by GroupHead.
		if parentID := handle.ParentDigest(); parentID != nil {
			record.parentID = *parentID
		}
		records = append(records, record)
	}
	return records, nil
}

func checkpointRemovalParent(records []checkpointRecord, group, targetID string) (string, error) {
	var parentID string
	targetFound := false
	for _, record := range records {
		if record.group == group && record.id == targetID {
			targetFound = true
			parentID = record.parentID
		}
	}
	for _, record := range records {
		if record.id != targetID && record.parentID == targetID {
			return "", fmt.Errorf("%w: %q", ErrCheckpointInUse, targetID)
		}
	}
	if !targetFound {
		return "", errors.New("inspect indexed checkpoints for removal: target is missing from its Studio-owned snapshot group")
	}
	return parentID, nil
}

func checkpointRemovalError(records []checkpointRecord, group, targetID string, removeErr error) error {
	if _, dependencyErr := checkpointRemovalParent(records, group, targetID); errors.Is(dependencyErr, ErrCheckpointInUse) {
		return dependencyErr
	}
	return fmt.Errorf("remove checkpoint: %w", removeErr)
}

func checkpointRemovalHead(records []checkpointRecord, group, targetID, parentID, currentHeadID string) string {
	if currentHeadID != targetID {
		return ""
	}
	if parentID != "" {
		for _, record := range records {
			if record.group == group && record.id == parentID && record.id != targetID {
				return parentID
			}
		}
	}

	// The recorded parent may no longer be indexed. Select a deterministic
	// surviving member, but never cross into another sandbox's group.
	survivors := make([]string, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.group != group || record.id == targetID || record.id == "" {
			continue
		}
		if _, ok := seen[record.id]; ok {
			continue
		}
		seen[record.id] = struct{}{}
		survivors = append(survivors, record.id)
	}
	sort.Strings(survivors)
	if len(survivors) == 0 {
		return ""
	}
	return survivors[0]
}

func selectCheckpointGroupHead(ctx context.Context, group, memberID string) error {
	update, err := msb.Snapshot.GroupHead(ctx, group+":"+memberID)
	if err != nil {
		return err
	}
	if update == nil || update.Group != group || update.Head != memberID {
		return errors.New("SDK did not select the requested checkpoint group head")
	}
	return nil
}

func restoreCheckpointGroupHead(ctx context.Context, group, selectedHeadID, previousHeadID string) error {
	current, err := msb.Snapshot.GroupHead(ctx, group)
	if err != nil {
		return err
	}
	if current == nil || current.Group != group {
		return errors.New("SDK returned an invalid checkpoint group head during rollback")
	}
	// Do not overwrite a newer head choice made by another SDK process after
	// Studio selected selectedHeadID.
	if current.Head != selectedHeadID {
		return nil
	}
	return selectCheckpointGroupHead(ctx, group, previousHeadID)
}

// RestoreCheckpoint creates a cold candidate from the named checkpoint. The
// group selector is scoped by this Studio sandbox ID, so resource inheritance
// can only refer to the same Studio identity. RestoreSandbox returns detached
// sandboxes; Stop observes their graceful transition to stopped before this
// method reports success. The checkpoint source remains untouched.
func (r *Runtime) RestoreCheckpoint(ctx context.Context, sandboxID, checkpointID, newVMName string) error {
	if !r.CheckpointRestoreSupported() {
		return ErrCheckpointRestoreUnavailable
	}

	handle, err := msb.Snapshot.Get(ctx, checkpointSelector(sandboxID, checkpointID))
	if err != nil {
		return fmt.Errorf("look up checkpoint for restore: %w", err)
	}
	if handle == nil {
		return errors.New("look up checkpoint for restore: SDK returned an empty handle")
	}

	sandbox, err := msb.RestoreSandbox(ctx, handle, newVMName, msb.WithDangerouslyInheritResources())
	if err != nil {
		return fmt.Errorf("restore checkpoint: %w", err)
	}
	if sandbox == nil {
		return errors.New("restore checkpoint: SDK returned an empty sandbox")
	}

	// A disk restore must preserve the PID-1 handoff on subsequent cold starts.
	// Do not let the manager adopt a VM that has silently lost systemd and its services.
	restored, err := msb.GetSandbox(ctx, newVMName)
	var config *msb.SandboxConfig
	if err == nil {
		config, err = restored.Config()
	}
	if err != nil || config == nil || config.Init == nil {
		detachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		detachErr := sandbox.Detach(detachCtx)
		cancel()
		return errors.Join(errors.New("the runtime did not preserve systemd initialization; checkpoint restore was refused and the original sandbox is retained"), err, detachErr)
	}

	if err := sandbox.Stop(ctx); err != nil {
		// The manager's durable restore record owns candidate cleanup. Detach the
		// SDK handle without closing (which can stop the candidate as a side
		// effect) so reconciliation can inspect and remove it by generation.
		detachErr := sandbox.Detach(context.Background())
		if detachErr != nil {
			return errors.Join(fmt.Errorf("stop restored checkpoint candidate: %w", err), fmt.Errorf("detach restored checkpoint candidate: %w", detachErr))
		}
		return fmt.Errorf("stop restored checkpoint candidate: %w", err)
	}
	if err := sandbox.Close(); err != nil {
		return fmt.Errorf("close stopped checkpoint candidate: %w", err)
	}
	return nil
}

func checkpointGroup(sandboxID string) string {
	return "sbx-" + sandboxID
}

// The SDK's installed group/member selector syntax uses a colon. Studio keeps
// the corresponding opaque identity as sbx-<sandboxID>/<checkpointID> at its
// higher layers and derives this SDK-only selector from the same components.
func checkpointSelector(sandboxID, checkpointID string) string {
	return checkpointGroup(sandboxID) + ":" + checkpointID
}
