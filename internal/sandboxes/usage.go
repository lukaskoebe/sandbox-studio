package sandboxes

import (
	"context"
	"errors"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// ErrUsageUnavailable means the runtime can't report a sandbox's resource use.
var ErrUsageUnavailable = errors.New("this runtime does not report resource use")

// UsageRuntime is a runtime that reports a running VM's resource use (runtime.Runtime).
type UsageRuntime interface {
	Usage(ctx context.Context, name string) (runtime.Usage, error)
}

// ResourceUse is a running sandbox's CPU and memory use.
type ResourceUse struct {
	CPUPercent       float64 `json:"cpuPercent" minimum:"0" maximum:"100" doc:"The share of the sandbox's CPUs in use over the last few seconds, from 0 to 100"`
	CPUs             int     `json:"cpus" doc:"The sandbox's CPU count"`
	MemoryBytes      uint64  `json:"memoryBytes" doc:"The memory the guest uses"`
	MemoryTotalBytes uint64  `json:"memoryTotalBytes" doc:"The sandbox's memory size"`
}

// Usage returns the CPU and memory use of a running sandbox. A sandbox that doesn't run has
// none to report.
func (m *Manager) Usage(ctx context.Context, envID, id string) (ResourceUse, error) {
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return ResourceUse{}, err
	}
	rt, ok := m.Runtime.(UsageRuntime)
	if !ok {
		return ResourceUse{}, ErrUsageUnavailable
	}
	name := VMName(rec)
	status, err := m.Runtime.Status(ctx, name)
	if err != nil {
		return ResourceUse{}, err
	}
	if status != runtime.StatusRunning {
		return ResourceUse{}, lifecycleError("the sandbox is not running")
	}
	u, err := rt.Usage(ctx, name)
	if err != nil {
		return ResourceUse{}, err
	}
	return usageOf(u, rec.CPUs, rec.MemoryMiB), nil
}

// usageOf scales a VM's use to the sandbox: CPU as a share of all its CPUs, and memory
// against the size it was given, or the VM's limit when the catalog has none.
func usageOf(u runtime.Usage, cpus, memoryMiB int) ResourceUse {
	pct := u.CPUPercent
	if cpus > 0 {
		pct /= float64(cpus)
	}
	pct = min(max(pct, 0), 100)
	total := u.MemoryLimitBytes
	if memoryMiB > 0 {
		total = uint64(memoryMiB) << 20
	}
	return ResourceUse{CPUPercent: pct, CPUs: cpus, MemoryBytes: u.MemoryBytes, MemoryTotalBytes: total}
}
