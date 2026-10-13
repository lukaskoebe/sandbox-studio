package sandboxes

import (
	"context"
	"errors"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// ResizeRequest sets a sandbox's CPUs and memory.
type ResizeRequest struct {
	CPUs      int `json:"cpus" minimum:"1" maximum:"64" doc:"The CPU count"`
	MemoryMiB int `json:"memoryMiB" minimum:"512" doc:"The memory size in MiB"`
}

// ResizeApplied says how each resource changed: live (the running VM has it now), nextStart
// (saved for the next start) or unchanged.
type ResizeApplied struct {
	CPUs      runtime.Applied `json:"cpus" enum:"unchanged,live,nextStart"`
	MemoryMiB runtime.Applied `json:"memoryMiB" enum:"unchanged,live,nextStart"`
}

// Resized is a sandbox after a resize.
type Resized struct {
	View
	Applied ResizeApplied `json:"applied"`
}

// Resize sets a sandbox's CPUs and memory. A running sandbox takes each change live where its
// VM's boot-time capacity allows; the rest is saved for the next start, as is every change to
// a stopped or suspended sandbox. The memory ceiling rises with the memory size when needed,
// since it can't be lower.
func (m *Manager) Resize(ctx context.Context, envID, id string, req ResizeRequest) (Resized, error) {
	defer m.publish(envID, id)
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return Resized{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return Resized{}, err
	}
	defer unlock()
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return Resized{}, err
	}
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return Resized{}, err
	}
	if m.failedCreation(id) {
		return Resized{}, lifecycleError("the sandbox could not be created; delete it")
	}
	maxMemory := max(rec.MaxMemoryMiB, req.MemoryMiB)
	if err := (resources.Resources{CPUs: int64(req.CPUs), MemoryMiB: int64(req.MemoryMiB), MaxMemoryMiB: int64(maxMemory)}).ValidateSize(); err != nil {
		return Resized{}, err
	}
	status, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		return Resized{}, err
	}
	switch status {
	case runtime.StatusRunning, runtime.StatusSuspended, runtime.StatusStopped, runtime.StatusCrashed:
	default:
		return Resized{}, lifecycleError(fmt.Sprintf("the sandbox is %s; resize it once it has settled", status))
	}

	applied, resizeErr := m.Runtime.Resize(ctx, VMName(rec), runtime.Size{CPUs: uint8(req.CPUs), MemoryMiB: uint32(req.MemoryMiB)})
	// Record what took effect even when a later step failed, so the catalog matches the VM.
	cpus, memory, maxMemoryMiB := rec.CPUs, rec.MemoryMiB, rec.MaxMemoryMiB
	if applied.CPUs != runtime.AppliedNone {
		cpus = req.CPUs
	}
	if applied.MemoryMiB != runtime.AppliedNone {
		memory, maxMemoryMiB = req.MemoryMiB, maxMemory
	}
	var storeErr error
	if applied.CPUs != runtime.AppliedNone || applied.MemoryMiB != runtime.AppliedNone {
		storeErr = m.Store.SetSandboxResources(ctx, envID, id, cpus, memory, maxMemoryMiB)
	}
	if err := errors.Join(resizeErr, storeErr); err != nil {
		return Resized{}, err
	}
	rec.CPUs, rec.MemoryMiB, rec.MaxMemoryMiB = cpus, memory, maxMemoryMiB
	v, err := m.view(ctx, rec)
	if err != nil {
		return Resized{}, err
	}
	return Resized{View: v, Applied: ResizeApplied{CPUs: applied.CPUs, MemoryMiB: applied.MemoryMiB}}, nil
}
