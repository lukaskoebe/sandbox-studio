package sandboxes

import (
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// resizeFixture is a checkpoint fixture whose VM is configured with 1 CPU and 512 MiB, under a
// 2048 MiB ceiling, in the given runtime status.
func resizeFixture(t *testing.T, status runtime.Status, liveResize bool) *checkpointFixture {
	t.Helper()
	f := newCheckpointFixture(t)
	if err := f.store.SetSandboxResources(f.ctx, f.env.ID, f.sandbox.ID, store.SandboxSize{CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 2048}); err != nil {
		t.Fatal(err)
	}
	vm := VMName(f.sandbox)
	f.runtime.mu.Lock()
	f.runtime.sizes[vm] = runtime.Size{CPUs: 1, MemoryMiB: 512}
	f.runtime.statuses[vm] = status
	f.runtime.liveResize = liveResize
	f.runtime.mu.Unlock()
	return f
}

func sandboxSizes(t *testing.T, f *checkpointFixture) store.Sandbox {
	t.Helper()
	sb, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

func TestResizeRunningSandboxAppliesLiveWhereItCan(t *testing.T) {
	f := resizeFixture(t, runtime.StatusRunning, true)
	got, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 4, MemoryMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied.CPUs != runtime.AppliedLive || got.Applied.MemoryMiB != runtime.AppliedLive {
		t.Fatalf("applied = %+v, want both live", got.Applied)
	}
	sb := sandboxSizes(t, f)
	if sb.CPUs != 4 || sb.MemoryMiB != 1024 || sb.MaxMemoryMiB != 2048 {
		t.Fatalf("catalog after resize = cpus %d memory %d max %d", sb.CPUs, sb.MemoryMiB, sb.MaxMemoryMiB)
	}
	if got := f.runtime.configuredSize(VMName(f.sandbox)); got != (runtime.Size{CPUs: 4, MemoryMiB: 1024}) {
		t.Fatalf("VM size = %+v", got)
	}
	if got.CPUs != 4 || got.MemoryMiB != 1024 {
		t.Fatalf("view = cpus %d memory %d", got.CPUs, got.MemoryMiB)
	}
}

func TestResizeRunningSandboxSavesWhatIsNotLive(t *testing.T) {
	f := resizeFixture(t, runtime.StatusRunning, false)
	got, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 512})
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied.CPUs != runtime.AppliedNextStart || got.Applied.MemoryMiB != runtime.AppliedUnchanged {
		t.Fatalf("applied = %+v, want cpus next start and memory unchanged", got.Applied)
	}
	if sb := sandboxSizes(t, f); sb.CPUs != 2 || sb.MemoryMiB != 512 {
		t.Fatalf("catalog after resize = cpus %d memory %d", sb.CPUs, sb.MemoryMiB)
	}
	if n := f.runtime.countCall("resize-cpus-live:" + VMName(f.sandbox)); n != 0 {
		t.Fatalf("live step ran %d times when nothing can apply live", n)
	}
}

func TestResizeStoppedSandboxSavesForNextStartAndRaisesCeiling(t *testing.T) {
	f := resizeFixture(t, runtime.StatusStopped, true)
	got, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 3, MemoryMiB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied.CPUs != runtime.AppliedNextStart || got.Applied.MemoryMiB != runtime.AppliedNextStart {
		t.Fatalf("applied = %+v, want both next start", got.Applied)
	}
	sb := sandboxSizes(t, f)
	if sb.CPUs != 3 || sb.MemoryMiB != 4096 || sb.MaxMemoryMiB != 4096 {
		t.Fatalf("catalog = cpus %d memory %d max %d, want the ceiling raised to the memory size", sb.CPUs, sb.MemoryMiB, sb.MaxMemoryMiB)
	}
	if got.Status != runtime.StatusStopped {
		t.Fatalf("status = %s, a stopped sandbox must stay stopped", got.Status)
	}
}

func TestResizeSuspendedSandboxSavesForNextStart(t *testing.T) {
	f := resizeFixture(t, runtime.StatusSuspended, true)
	got, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied.CPUs != runtime.AppliedNextStart || got.Applied.MemoryMiB != runtime.AppliedNextStart {
		t.Fatalf("applied = %+v, want both next start", got.Applied)
	}
	if got.Status != runtime.StatusSuspended {
		t.Fatalf("status = %s, a suspended sandbox must stay suspended", got.Status)
	}
}

func TestResizeRecordsWhatTookEffectBeforeAFailedStep(t *testing.T) {
	f := resizeFixture(t, runtime.StatusRunning, true)
	f.runtime.fail("resize-memory-live:"+VMName(f.sandbox), errors.New("guest refused memory"))
	if _, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 4, MemoryMiB: 1024}); err == nil {
		t.Fatal("a failed memory step must fail the resize")
	}
	sb := sandboxSizes(t, f)
	if sb.CPUs != 4 || sb.MemoryMiB != 512 {
		t.Fatalf("catalog after a partial resize = cpus %d memory %d, want the CPUs that took effect and the memory unchanged", sb.CPUs, sb.MemoryMiB)
	}
	if got := f.runtime.configuredSize(VMName(f.sandbox)); got.CPUs != 4 || got.MemoryMiB != 512 {
		t.Fatalf("VM size = %+v, catalog and VM must agree", got)
	}
}

func TestResizeRefusesInvalidSizesAndUnsettledSandboxes(t *testing.T) {
	for _, req := range []ResizeRequest{{CPUs: 0, MemoryMiB: 512}, {CPUs: 65, MemoryMiB: 512}, {CPUs: 2, MemoryMiB: 256}} {
		f := resizeFixture(t, runtime.StatusRunning, true)
		if _, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, req); !errors.Is(err, resources.ErrInvalid) {
			t.Fatalf("resize %+v error = %v, want ErrInvalid", req, err)
		}
	}

	f := resizeFixture(t, runtime.StatusStarting, true)
	if _, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 1024}); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("resize while starting error = %v, want ErrLifecycleState", err)
	}
	if n := f.runtime.countCall("resize:" + VMName(f.sandbox)); n != 0 {
		t.Fatalf("the runtime resized a starting sandbox %d times", n)
	}
}

func TestResizeWaitsForTheSandboxLock(t *testing.T) {
	f := resizeFixture(t, runtime.StatusRunning, true)
	unlock, err := f.manager.tryMutation(f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 1024}); !errors.Is(err, ErrBusy) {
		t.Fatalf("resize of a busy sandbox error = %v, want ErrBusy", err)
	}
}

// A change that waits for the next start leaves what the running VM has in the catalog, for
// "after restart"; a live change and a start clear it.
func TestResizeRecordsWhatTheRunningVMHas(t *testing.T) {
	f := resizeFixture(t, runtime.StatusRunning, false)
	got, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUs != 2 || got.MemoryMiB != 1024 || got.LiveCPUs != 1 || got.LiveMemoryMiB != 512 {
		t.Fatalf("view = cpus %d memory %d, live %d %d", got.CPUs, got.MemoryMiB, got.LiveCPUs, got.LiveMemoryMiB)
	}
	// A second change before the restart keeps what the VM has.
	if got, err = f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 3, MemoryMiB: 1024}); err != nil {
		t.Fatal(err)
	}
	if sb := sandboxSizes(t, f); sb.CPUs != 3 || sb.LiveCPUs != 1 || sb.LiveMemoryMiB != 512 {
		t.Fatalf("catalog = cpus %d, live %d %d", sb.CPUs, sb.LiveCPUs, sb.LiveMemoryMiB)
	}
	// Setting it back to what the VM has leaves nothing waiting.
	if got, err = f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 1, MemoryMiB: 512}); err != nil {
		t.Fatal(err)
	}
	if got.LiveCPUs != 0 || got.LiveMemoryMiB != 0 {
		t.Fatalf("live after setting back = %d %d", got.LiveCPUs, got.LiveMemoryMiB)
	}

	if _, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 512}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Stop(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	v, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.LiveCPUs != 0 || sandboxSizes(t, f).LiveCPUs != 0 {
		t.Fatalf("live after a start = %d", v.LiveCPUs)
	}
}

func TestResizeAndRestart(t *testing.T) {
	f := resizeFixture(t, runtime.StatusRunning, false)
	vm := VMName(f.sandbox)
	got, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 1024, Restart: true})
	if err != nil {
		t.Fatal(err)
	}
	if f.runtime.countCall("stop:"+vm) != 1 || f.runtime.countCall("start:"+vm) != 1 {
		t.Fatalf("calls %v", f.runtime.calls)
	}
	if got.LiveCPUs != 0 || got.LiveMemoryMiB != 0 || got.CPUs != 2 || got.StartedAt == nil {
		t.Fatalf("view after restart = %+v", got.Sandbox)
	}

	// Nothing waits, so nothing restarts.
	f = resizeFixture(t, runtime.StatusRunning, true)
	vm = VMName(f.sandbox)
	if _, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 1024, Restart: true}); err != nil {
		t.Fatal(err)
	}
	if f.runtime.countCall("stop:"+vm) != 0 {
		t.Fatal("a live resize restarted the sandbox")
	}
}

func TestResizeDryRunChangesNothing(t *testing.T) {
	f := resizeFixture(t, runtime.StatusRunning, false)
	got, err := f.manager.Resize(f.ctx, f.env.ID, f.sandbox.ID, ResizeRequest{CPUs: 2, MemoryMiB: 512, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied.CPUs != runtime.AppliedNextStart || got.Applied.MemoryMiB != runtime.AppliedUnchanged {
		t.Fatalf("plan = %+v", got.Applied)
	}
	if sb := sandboxSizes(t, f); sb.CPUs != 1 || sb.LiveCPUs != 0 || f.runtime.countCall("resize:"+VMName(f.sandbox)) != 0 {
		t.Fatalf("a dry run changed the sandbox: %+v", sb)
	}
}
