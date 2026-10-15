package sandboxes

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// usageRuntime is the fake runtime with resource use, as msb reports it for running VMs.
type usageRuntime struct {
	*fakeSandboxRuntime
	use runtime.Usage
}

func (u usageRuntime) Usage(context.Context, string) (runtime.Usage, error) { return u.use, nil }

func TestUsageOfScalesToTheSandbox(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        runtime.Usage
		cpus, mib int
		want      ResourceUse
	}{
		{"share of all CPUs", runtime.Usage{CPUPercent: 150, MemoryBytes: 1 << 30, MemoryLimitBytes: 8 << 30}, 4, 4096,
			ResourceUse{CPUPercent: 37.5, CPUs: 4, MemoryBytes: 1 << 30, MemoryTotalBytes: 4 << 30}},
		{"capped at all CPUs busy", runtime.Usage{CPUPercent: 450}, 2, 1024,
			ResourceUse{CPUPercent: 100, CPUs: 2, MemoryTotalBytes: 1 << 30}},
		{"the VM's limit without a size", runtime.Usage{CPUPercent: 30, MemoryLimitBytes: 2 << 30}, 0, 0,
			ResourceUse{CPUPercent: 30, MemoryTotalBytes: 2 << 30}},
	} {
		if got := usageOf(tc.in, tc.cpus, tc.mib); got != tc.want {
			t.Errorf("%s: usageOf = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestUsageOnlyForARunningSandbox(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := usageRuntime{fakeSandboxRuntime: f.runtime, use: runtime.Usage{CPUPercent: 50, MemoryBytes: 256 << 20}}

	// A runtime without resource use says so.
	if _, err := f.manager.Usage(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrUsageUnavailable) {
		t.Fatalf("Usage without support = %v, want ErrUsageUnavailable", err)
	}
	f.manager.Runtime = rt
	if _, err := f.manager.Usage(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("Usage of a stopped sandbox = %v, want ErrLifecycleState", err)
	}
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	got, err := f.manager.Usage(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := ResourceUse{CPUPercent: 50, CPUs: 1, MemoryBytes: 256 << 20, MemoryTotalBytes: 512 << 20}
	if got != want {
		t.Fatalf("Usage = %+v, want %+v", got, want)
	}
}
