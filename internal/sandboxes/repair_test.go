package sandboxes

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// adoptingRuntime is the fake runtime with the disks of lost VMs: disks names the VMs whose
// disks are on hand, and AdoptDisks gives one a stopped record.
type adoptingRuntime struct {
	*fakeSandboxRuntime
	disks   map[string]bool
	adopted []string
	spec    runtime.Spec
	fail    error
}

func (r *adoptingRuntime) LostDisks(name string) (string, error) {
	if r.disks[name] {
		return "/msb/sandboxes/" + name, nil
	}
	return "", runtime.ErrNoDisks
}

func (r *adoptingRuntime) AdoptDisks(_ context.Context, name string, spec runtime.Spec, _ string, _ map[string]string) error {
	if r.fail != nil {
		return r.fail
	}
	r.adopted, r.spec = append(r.adopted, name), spec
	r.setStatus(name, runtime.StatusStopped)
	return nil
}

func TestRepairGivesALostVMANewRecordAroundItsDisks(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &adoptingRuntime{fakeSandboxRuntime: f.runtime, disks: map[string]bool{VMName(f.sandbox): true}}
	f.manager.Runtime = rt
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusAbsent)
	f.sandbox.BaseImage = "ghcr.io/example/base@sha256:old"
	if err := f.store.SetSandboxBase(f.ctx, f.env.ID, f.sandbox.ID, f.sandbox.BaseImage, "v0.19.0"); err != nil {
		t.Fatal(err)
	}

	v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Repairable {
		t.Fatal("a lost VM with its disks on hand isn't repairable")
	}
	v, err = f.manager.RepairVM(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.adopted) != 1 || rt.adopted[0] != VMName(f.sandbox) {
		t.Fatalf("adopted %v", rt.adopted)
	}
	if rt.spec.WorkspaceMiB != uint32(f.sandbox.WorkspaceMiB) || rt.spec.HomeMiB != uint32(f.sandbox.HomeMiB) || rt.spec.DockerMiB != uint32(f.sandbox.DockerMiB) {
		t.Fatalf("spec disks %+v, want the sandbox's", rt.spec)
	}
	if rt.spec.BaseImage != f.sandbox.BaseImage {
		t.Fatalf("spec image %q, want the one the VM was made from, %q", rt.spec.BaseImage, f.sandbox.BaseImage)
	}
	if !v.Status.IsRunning() || v.Repairable {
		t.Fatalf("after the repair: status %s, repairable %v", v.Status, v.Repairable)
	}
	// There's nothing left to repair.
	if _, err := f.manager.RepairVM(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("a second repair = %v, want a lifecycle error", err)
	}
}

func TestRepairRefusesWithoutDisks(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &adoptingRuntime{fakeSandboxRuntime: f.runtime}
	f.manager.Runtime = rt
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusAbsent)
	v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Repairable {
		t.Fatal("a lost VM without disks is repairable")
	}
	if _, err := f.manager.RepairVM(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrLifecycleState) || len(rt.adopted) != 0 {
		t.Fatalf("RepairVM = %v, adopted %v; want a lifecycle error and nothing adopted", err, rt.adopted)
	}
}

func TestRepairThatFailsLeavesTheSandboxLost(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &adoptingRuntime{fakeSandboxRuntime: f.runtime, disks: map[string]bool{VMName(f.sandbox): true}, fail: errors.New("msb said no")}
	f.manager.Runtime = rt
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusAbsent)
	if _, err := f.manager.RepairVM(f.ctx, f.env.ID, f.sandbox.ID); err == nil {
		t.Fatal("a failed adoption reported success")
	}
	v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != runtime.StatusAbsent || !v.Repairable {
		t.Fatalf("after a failed repair: status %s, repairable %v; want it lost and repairable", v.Status, v.Repairable)
	}
}
