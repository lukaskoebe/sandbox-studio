package runtime

import (
	"context"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Usage is a running VM's resource use, as msb last sampled it.
type Usage struct {
	// CPUPercent is the guest's vCPU time over the last sample window: 100 is one vCPU busy,
	// so a VM with four vCPUs reaches 400.
	CPUPercent float64
	// MemoryBytes is the memory the guest uses.
	MemoryBytes uint64
	// MemoryLimitBytes is the VM's configured memory limit.
	MemoryLimitBytes uint64
}

// Usage returns the resource use of the running VM name. msb only reports it for a VM that
// runs or drains.
func (r *Runtime) Usage(ctx context.Context, name string) (Usage, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return Usage{}, err
	}
	m, err := h.Metrics(ctx)
	if err != nil {
		return Usage{}, err
	}
	return Usage{CPUPercent: m.CPUPercent, MemoryBytes: m.MemoryBytes, MemoryLimitBytes: m.MemoryLimitBytes}, nil
}
