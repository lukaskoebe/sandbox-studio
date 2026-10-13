package runtime

import (
	"context"
	"errors"
	"fmt"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Size is a sandbox's CPU count and memory size in MiB.
type Size struct {
	CPUs      uint8
	MemoryMiB uint32
}

// Applied says how one resource of a sandbox changed. The zero value means the change was
// not applied, which is what a caller sees for every resource after a failed Resize.
type Applied string

// Applied values. A live change is in the running VM now; a next-start change is saved in the
// sandbox's config and takes effect when the sandbox next starts.
const (
	AppliedNone      Applied = ""
	AppliedUnchanged Applied = "unchanged"
	AppliedLive      Applied = "live"
	AppliedNextStart Applied = "nextStart"
)

// Resized says how each resource of a sandbox changed.
type Resized struct {
	CPUs      Applied
	MemoryMiB Applied
}

// dispositionLive is the disposition msb gives a change the running VM takes now.
const dispositionLive = "live"

// resizeChange is one resource's change, as a dry run of msb's modify reports it.
type resizeChange struct {
	changed bool // the value differs from the one the VM is configured with
	live    bool // the running VM takes the change now
}

// readResizePlan reads the CPU and memory changes of a dry run.
func readResizePlan(changes []msb.PlannedChange) (cpus, memory resizeChange) {
	for _, change := range changes {
		c := resizeChange{changed: true, live: change.Disposition == dispositionLive}
		switch change.Field {
		case "cpus":
			cpus = c
		case "memory":
			memory = c
		}
	}
	return cpus, memory
}

// Resize sets a sandbox's CPU count and memory size. msb's dry run decides which changes the
// running VM takes now (its boot-time capacity allows them) and which are saved for the next
// start. Live changes are applied first, one call per resource, and the rest in one call that
// never restarts the VM. A failed step leaves the earlier ones in effect: the returned Resized
// says what was applied, and the error names the step that failed.
func (r *Runtime) Resize(ctx context.Context, name string, size Size) (Resized, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return Resized{}, err
	}
	plan, err := h.Modify(ctx, msb.ModifyOptions{CPUs: size.CPUs, MemoryMiB: size.MemoryMiB, DryRun: true})
	if err != nil {
		return Resized{}, fmt.Errorf("plan resize: %w", err)
	}
	if len(plan.Conflicts) > 0 {
		return Resized{}, fmt.Errorf("plan resize: %w", errors.New(plan.Conflicts[0].Message))
	}
	cpus, memory := readResizePlan(plan.Changes)
	var out Resized
	if !cpus.changed {
		out.CPUs = AppliedUnchanged
	}
	if !memory.changed {
		out.MemoryMiB = AppliedUnchanged
	}

	if cpus.live {
		if _, err := h.Modify(ctx, msb.ModifyOptions{CPUs: size.CPUs, Policy: msb.ModificationPolicyNoRestart}); err != nil {
			return out, fmt.Errorf("set CPUs live: %w", err)
		}
		out.CPUs = AppliedLive
	}
	if memory.live {
		if _, err := h.Modify(ctx, msb.ModifyOptions{MemoryMiB: size.MemoryMiB, Policy: msb.ModificationPolicyNoRestart}); err != nil {
			return out, fmt.Errorf("set memory live: %w", err)
		}
		out.MemoryMiB = AppliedLive
	}

	next := msb.ModifyOptions{Policy: msb.ModificationPolicyNextStart}
	if cpus.changed && !cpus.live {
		next.CPUs = size.CPUs
	}
	if memory.changed && !memory.live {
		next.MemoryMiB = size.MemoryMiB
	}
	if next.CPUs != 0 || next.MemoryMiB != 0 {
		if _, err := h.Modify(ctx, next); err != nil {
			return out, fmt.Errorf("save resources for the next start: %w", err)
		}
		if next.CPUs != 0 {
			out.CPUs = AppliedNextStart
		}
		if next.MemoryMiB != 0 {
			out.MemoryMiB = AppliedNextStart
		}
	}
	return out, nil
}
