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

// ResizeRequest sets a sandbox's CPUs and memory.
type ResizeRequest struct {
	CPUs      int  `json:"cpus" minimum:"1" maximum:"64" doc:"The CPU count"`
	MemoryMiB int  `json:"memoryMiB" minimum:"512" doc:"The memory size in MiB"`
	Restart   bool `json:"restart,omitempty" doc:"Restart a running sandbox when part of the change waits for its next start, so it takes effect now. Running processes and terminal sessions end"`
	DryRun    bool `json:"dryRun,omitempty" doc:"Only say how each resource would change, in applied, and change nothing"`
}

// ResizePlanner is a runtime that tells how a resize would apply without making it.
type ResizePlanner interface {
	PlanResize(ctx context.Context, name string, size runtime.Size) (runtime.Resized, error)
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
// a stopped or suspended sandbox, and the catalog keeps what the running VM has meanwhile
// (LiveCPUs, LiveMemoryMiB). With Restart, a running sandbox with a change that waits restarts
// so it takes effect now. The memory ceiling rises with the memory size when needed, since it
// can't be lower.
func (m *Manager) Resize(ctx context.Context, envID, id string, req ResizeRequest) (Resized, error) {
	if req.DryRun {
		return m.planResize(ctx, envID, id, req)
	}
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
	size := store.SandboxSize{CPUs: cpus, MemoryMiB: memory, MaxMemoryMiB: maxMemoryMiB}
	if status == runtime.StatusRunning || status == runtime.StatusSuspended {
		// The VM keeps what it has where the change waits for its next start.
		liveCPUs, liveMemory := cmp.Or(rec.LiveCPUs, rec.CPUs), cmp.Or(rec.LiveMemoryMiB, rec.MemoryMiB)
		if applied.CPUs == runtime.AppliedLive {
			liveCPUs = req.CPUs
		}
		if applied.MemoryMiB == runtime.AppliedLive {
			liveMemory = req.MemoryMiB
		}
		if liveCPUs != cpus {
			size.LiveCPUs = liveCPUs
		}
		if liveMemory != memory {
			size.LiveMemoryMiB = liveMemory
		}
	}
	var storeErr error
	if applied.CPUs != runtime.AppliedNone || applied.MemoryMiB != runtime.AppliedNone {
		storeErr = m.Store.SetSandboxResources(ctx, envID, id, size)
	}
	if err := errors.Join(resizeErr, storeErr); err != nil {
		return Resized{}, err
	}
	rec.CPUs, rec.MemoryMiB, rec.MaxMemoryMiB = cpus, memory, maxMemoryMiB
	rec.LiveCPUs, rec.LiveMemoryMiB = size.LiveCPUs, size.LiveMemoryMiB
	if req.Restart && status == runtime.StatusRunning && (rec.LiveCPUs != 0 || rec.LiveMemoryMiB != 0) {
		if err := m.Runtime.Stop(ctx, VMName(rec)); err != nil {
			return Resized{}, fmt.Errorf("restart: %w", err)
		}
		if rec, err = m.startVM(ctx, rec); err != nil {
			return Resized{}, fmt.Errorf("restart: %w", err)
		}
	}
	v, err := m.view(ctx, rec)
	if err != nil {
		return Resized{}, err
	}
	return Resized{View: v, Applied: ResizeApplied{CPUs: applied.CPUs, MemoryMiB: applied.MemoryMiB}}, nil
}

// ErrPlanUnavailable means the runtime can't tell how a resize would apply.
var ErrPlanUnavailable = errors.New("this runtime can't plan a resize")

// planResize answers a dry run. It takes no lock: it changes nothing, and the answer is a
// forecast either way.
func (m *Manager) planResize(ctx context.Context, envID, id string, req ResizeRequest) (Resized, error) {
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return Resized{}, err
	}
	if err := (resources.Resources{CPUs: int64(req.CPUs), MemoryMiB: int64(req.MemoryMiB), MaxMemoryMiB: int64(max(rec.MaxMemoryMiB, req.MemoryMiB))}).ValidateSize(); err != nil {
		return Resized{}, err
	}
	planner, ok := m.Runtime.(ResizePlanner)
	if !ok {
		return Resized{}, ErrPlanUnavailable
	}
	plan, err := planner.PlanResize(ctx, VMName(rec), runtime.Size{CPUs: uint8(req.CPUs), MemoryMiB: uint32(req.MemoryMiB)})
	if err != nil {
		return Resized{}, err
	}
	v, err := m.view(ctx, rec)
	return Resized{View: v, Applied: ResizeApplied{CPUs: plan.CPUs, MemoryMiB: plan.MemoryMiB}}, err
}
