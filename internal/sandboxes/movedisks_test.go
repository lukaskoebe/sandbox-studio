package sandboxes

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// movingRuntime is the transfer fake with disks in msb's old layout: old names the VMs msb
// won't start or copy until MoveDisks moves their disks, which it refuses for a running one,
// as msb does for a writer it sees.
type movingRuntime struct {
	*transferFake
	layoutMu sync.Mutex
	old      map[string]bool
	moveFail map[string]error
	stopErr  error // what Stop answers, when set
}

func (r *movingRuntime) Stop(ctx context.Context, name string) error {
	if r.stopErr != nil {
		r.record("stop-failed:" + name)
		return r.stopErr
	}
	return r.transferFake.Stop(ctx, name)
}

func (r *movingRuntime) StorageStates(_ context.Context, prefix string) ([]runtime.StorageState, error) {
	r.layoutMu.Lock()
	defer r.layoutMu.Unlock()
	var out []runtime.StorageState
	for vm := range r.old {
		if !strings.HasPrefix(vm, prefix) {
			continue
		}
		status, _ := r.Status(context.Background(), vm)
		out = append(out, runtime.StorageState{VM: vm, Status: "maintenance_required", Running: status.IsRunning()})
	}
	return out, nil
}

func (r *movingRuntime) MoveDisks(ctx context.Context, name string) error {
	r.record("move:" + name)
	if status, _ := r.Status(ctx, name); status.IsRunning() {
		return errors.New("legacy storage migration on macOS cannot exclude a writer in sandbox '" + name + "'")
	}
	r.layoutMu.Lock()
	defer r.layoutMu.Unlock()
	if err := r.moveFail[name]; err != nil {
		return err
	}
	delete(r.old, name)
	return nil
}

func newMovingRuntime(f *checkpointFixture, old ...string) *movingRuntime {
	r := &movingRuntime{transferFake: newTransferFake(f), old: map[string]bool{}, moveFail: map[string]error{}}
	for _, vm := range old {
		r.old[vm] = true
	}
	f.manager.Runtime = r
	return r
}

func (f *checkpointFixture) secondSandbox(t *testing.T, name string, status runtime.Status) store.Sandbox {
	t.Helper()
	sb, err := f.store.CreateSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, Name: name, CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024, HomeMiB: DefaultHomeMiB})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.setStatus(VMName(sb), status)
	return sb
}

func calledAny(calls []string, prefix string) bool {
	return slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

func TestOldDiskLayoutStopsAStartOrRebaseBeforeItChangesAnything(t *testing.T) {
	f := newCheckpointFixture(t)
	vm := VMName(f.sandbox)
	rt := newMovingRuntime(f, vm)

	v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OldDiskLayout {
		t.Fatal("a sandbox msb won't start until its disks move doesn't say so")
	}
	if _, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, runtime.ErrMoveDisks) {
		t.Fatalf("Start = %v, want ErrMoveDisks", err)
	}
	if calledAny(rt.calls, "start:") {
		t.Fatalf("Start asked msb to start the VM: %v", rt.calls)
	}

	// One that still runs as the older msb started it keeps running: a rebase would stop it
	// and then fail to start it for the copy.
	rt.setStatus(vm, runtime.StatusRunning)
	rt.calls = nil
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, "", RebaseOptions{}); !errors.Is(err, runtime.ErrMoveDisks) {
		t.Fatalf("Rebase = %v, want ErrMoveDisks", err)
	}
	if calledAny(rt.calls, "stop:") || calledAny(rt.calls, "create-transfer:") {
		t.Fatalf("the refused rebase touched the VMs: %v", rt.calls)
	}
	if status, _ := rt.Status(f.ctx, vm); status != runtime.StatusRunning {
		t.Fatalf("after the refused rebase the sandbox is %s, want running", status)
	}
	if _, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy", ForkOptions{}); !errors.Is(err, runtime.ErrMoveDisks) {
		t.Fatalf("Fork = %v, want ErrMoveDisks", err)
	}
}

func TestMoveDisksStopsEverySandboxFirstAndStartsTheOnesThatRan(t *testing.T) {
	f := newCheckpointFixture(t)
	other := f.secondSandbox(t, "other-box", runtime.StatusStopped)
	fresh := f.secondSandbox(t, "fresh-box", runtime.StatusRunning)
	rt := newMovingRuntime(f, VMName(f.sandbox), VMName(other))
	rt.setStatus(VMName(f.sandbox), runtime.StatusRunning)

	if _, err := f.manager.MoveDisks(f.ctx, false); !errors.Is(err, ErrWritersRunning) {
		t.Fatalf("MoveDisks unconfirmed = %v, want ErrWritersRunning", err)
	}
	if calledAny(rt.calls, "move:") || calledAny(rt.calls, "stop:") {
		t.Fatalf("an unconfirmed move touched the VMs: %v", rt.calls)
	}

	rt.calls = nil
	moved, err := f.manager.MoveDisks(f.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 || moved[0].Name != "checkpoint-box" || moved[1].Name != "other-box" || moved[0].Error != "" || moved[1].Error != "" {
		t.Fatalf("moved %+v, want both sandboxes, in name order, without errors", moved)
	}
	stop := slices.Index(rt.calls, "stop:"+VMName(f.sandbox))
	firstMove := slices.IndexFunc(rt.calls, func(c string) bool { return strings.HasPrefix(c, "move:") })
	if stop < 0 || firstMove < stop {
		t.Fatalf("calls %v: the running sandbox must stop before any disk moves", rt.calls)
	}
	if calledAny(rt.calls, "stop:"+VMName(fresh)) {
		t.Fatalf("a sandbox already in the new layout was stopped: %v", rt.calls)
	}
	if status, _ := rt.Status(f.ctx, VMName(f.sandbox)); status != runtime.StatusRunning {
		t.Fatalf("the sandbox that ran is %s after the move, want running", status)
	}
	if status, _ := rt.Status(f.ctx, VMName(other)); status != runtime.StatusStopped {
		t.Fatalf("the stopped sandbox is %s after the move, want stopped", status)
	}
	for _, id := range []string{f.sandbox.ID, other.ID} {
		v, err := f.manager.Get(f.ctx, f.env.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if v.OldDiskLayout {
			t.Fatalf("%s still has its disks in the old layout after the move", v.Name)
		}
	}
	if moved, err := f.manager.MoveDisks(f.ctx, true); err != nil || len(moved) != 0 {
		t.Fatalf("a second move = %+v, %v; want nothing to do", moved, err)
	}
}

func TestMoveDisksThatFailsForOneSandboxReportsItAndMovesTheRest(t *testing.T) {
	f := newCheckpointFixture(t)
	other := f.secondSandbox(t, "other-box", runtime.StatusRunning)
	rt := newMovingRuntime(f, VMName(f.sandbox), VMName(other))
	rt.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	rt.moveFail[VMName(f.sandbox)] = errors.New("msb said no")

	moved, err := f.manager.MoveDisks(f.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 || !strings.Contains(moved[0].Error, "msb said no") || moved[1].Error != "" {
		t.Fatalf("moved %+v, want checkpoint-box failed and other-box moved", moved)
	}
	// The one that failed stays stopped: msb won't start it until its disks move.
	if status, _ := rt.Status(f.ctx, VMName(f.sandbox)); status != runtime.StatusStopped {
		t.Fatalf("the failed sandbox is %s, want stopped", status)
	}
	if status, _ := rt.Status(f.ctx, VMName(other)); status != runtime.StatusRunning {
		t.Fatalf("the moved sandbox is %s, want running again", status)
	}
	v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OldDiskLayout {
		t.Fatal("the sandbox whose move failed no longer says its disks must move")
	}
}

// A teammate's browser VM writes to its disks like any sandbox, so it stops and moves with
// them; it isn't started again, as Studio starts it when it next needs it. Move disks lists
// it by what it is for.
func TestMoveDisksMovesTheBrowserVMsWithoutStartingThem(t *testing.T) {
	f := newCheckpointFixture(t)
	tm, err := f.store.CreateTeammate(f.ctx, store.Teammate{EnvironmentID: f.env.ID, Name: "Iris", Harness: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	browser, err := f.store.CreateBrowserSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, TeammateID: tm.ID, Name: BrowserName(tm.ID), CPUs: 1, MemoryMiB: 512})
	if err != nil {
		t.Fatal(err)
	}
	rt := newMovingRuntime(f, VMName(f.sandbox), VMName(browser))
	rt.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	rt.setStatus(VMName(browser), runtime.StatusRunning)

	list, err := f.manager.DisksToMove(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "checkpoint-box" || list[0].Own || list[0].Status != "running" ||
		list[1].Name != "Iris's browser" || !list[1].Own || list[1].Status != "running" {
		t.Fatalf("DisksToMove = %+v, want the sandbox, then Iris's browser as Studio's own", list)
	}

	moved, err := f.manager.MoveDisks(f.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 || moved[0].Error != "" || moved[1].Error != "" {
		t.Fatalf("moved %+v, want both without errors", moved)
	}
	if status, _ := rt.Status(f.ctx, VMName(f.sandbox)); status != runtime.StatusRunning {
		t.Fatalf("the sandbox that ran is %s after the move, want running", status)
	}
	if status, _ := rt.Status(f.ctx, VMName(browser)); status != runtime.StatusStopped {
		t.Fatalf("the browser VM is %s after the move, want stopped until Studio needs it", status)
	}
	if list, err := f.manager.DisksToMove(f.ctx); err != nil || len(list) != 0 {
		t.Fatalf("DisksToMove after the move = %+v, %v; want nothing", list, err)
	}
}

// A VM that doesn't stop is reported, and the others still stop and move: msb refuses the
// moves it can't make while that one writes, and says so for each.
func TestMoveDisksGoesOnPastASandboxThatDoesNotStop(t *testing.T) {
	f := newCheckpointFixture(t)
	other := f.secondSandbox(t, "other-box", runtime.StatusStopped)
	rt := newMovingRuntime(f, VMName(f.sandbox), VMName(other))
	rt.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	rt.stopErr = errors.New("the VM won't stop")

	moved, err := f.manager.MoveDisks(f.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 || !strings.Contains(moved[0].Error, "it did not stop: the VM won't stop") || moved[1].Error != "" {
		t.Fatalf("moved %+v, want checkpoint-box reported and other-box moved", moved)
	}
	if calledAny(rt.calls, "move:"+VMName(f.sandbox)) {
		t.Fatalf("Studio asked msb to move the disks of a VM that didn't stop: %v", rt.calls)
	}
}
