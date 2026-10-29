package sandboxes

import (
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func resizeDisksNow(f *checkpointFixture, id string, sizes DiskSizes) (View, error) {
	if _, err := f.manager.ResizeDisks(f.ctx, f.env.ID, id, sizes); err != nil {
		return View{}, err
	}
	if err := f.manager.Wait(f.ctx, id); err != nil {
		return View{}, err
	}
	return f.manager.Get(f.ctx, f.env.ID, id)
}

// A disk resize moves the sandbox to a new generation with the new sizes, copying the
// workspace, the home and Docker's data, and keeps its CPUs, memory and status.
func TestResizeDisksCopiesDockerData(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	setHomeMiB(t, f, DefaultHomeMiB)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	tf.setHome(source, "dotfiles")
	tf.dockers[source] = []byte("images")

	view, err := resizeDisksNow(f, f.sandbox.ID, DiskSizes{WorkspaceMiB: 8192, DockerMiB: 16384, HomeMiB: 6144})
	if err != nil {
		t.Fatal(err)
	}
	target := VMName(view.Sandbox)
	if view.Generation != f.sandbox.Generation+1 || view.Status != runtime.StatusRunning || view.TemplateID != "" {
		t.Fatalf("view = %+v", view)
	}
	if view.WorkspaceMiB != 8192 || view.DockerMiB != 16384 || view.HomeMiB != 6144 || view.CPUs != f.sandbox.CPUs || view.MemoryMiB != f.sandbox.MemoryMiB {
		t.Fatalf("view resources = %+v", view.Sandbox)
	}
	spec := tf.specs[target]
	if spec.WorkspaceMiB != 8192 || spec.DockerMiB != 16384 || spec.HomeMiB != 6144 || spec.Image != nil {
		t.Fatalf("target spec = %+v", spec)
	}
	if tf.workspace(target) != "files" || tf.home(target) != "dotfiles" || string(tf.dockers[target]) != "images" || !tf.booted[target] {
		t.Fatalf("target workspace %q, home %q, docker %q, booted %v", tf.workspace(target), tf.home(target), tf.dockers[target], tf.booted[target])
	}
	if tf.hasVM(source) {
		t.Fatal("source VM remains")
	}
	evs, err := f.store.SandboxEvents(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(evs) == 0 || evs[len(evs)-1].Kind != store.EventDisks {
		t.Fatalf("events = %+v, %v", evs, err)
	}
}

// A template sandbox stays on its template, with the CPUs it was resized to rather than the
// template's, and a size left out stays.
func TestResizeDisksKeepsTheTemplate(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	sb, err := f.store.CreateSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, Name: "templated", TemplateID: template.ID,
		CPUs: 6, MemoryMiB: 1024, MaxMemoryMiB: 2048, WorkspaceMiB: 4096, DockerMiB: 2048, HomeMiB: DefaultHomeMiB})
	if err != nil {
		t.Fatal(err)
	}
	tf.setStatus(VMName(sb), runtime.StatusStopped)
	tf.setWorkspace(VMName(sb), "files")

	view, err := resizeDisksNow(f, sb.ID, DiskSizes{DockerMiB: 8192})
	if err != nil {
		t.Fatal(err)
	}
	target := VMName(view.Sandbox)
	if view.TemplateID != template.ID || view.CPUs != 6 || view.WorkspaceMiB != 4096 || view.DockerMiB != 8192 || view.HomeMiB != DefaultHomeMiB {
		t.Fatalf("view = %+v", view.Sandbox)
	}
	if view.Status != runtime.StatusStopped || tf.booted[target] || tf.specs[target].Image == nil || tf.workspace(target) != "files" {
		t.Fatalf("view = %+v, booted %v, spec %+v", view, tf.booted[target], tf.specs[target])
	}
}

// A request that changes nothing, sizes out of range, or a home size for a sandbox without
// a home disk are refused before anything changes.
func TestResizeDisksRefusesBadSizes(t *testing.T) {
	f := newCheckpointFixture(t)
	newTransferFake(f)
	setHomeMiB(t, f, 0) // a legacy template's sandbox
	for name, sizes := range map[string]DiskSizes{
		"unchanged": {WorkspaceMiB: f.sandbox.WorkspaceMiB},
		"too small": {DockerMiB: 512},
		"home":      {HomeMiB: 8192},
	} {
		_, err := f.manager.ResizeDisks(f.ctx, f.env.ID, f.sandbox.ID, sizes)
		if !errors.Is(err, resources.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
		if name == "home" && !errors.Is(err, ErrNoHomeDisk) {
			t.Errorf("home: err = %v, want ErrNoHomeDisk", err)
		}
	}
	if ops, err := f.store.Rebases(f.ctx); err != nil || len(ops) != 0 {
		t.Fatalf("rebase records = %+v, %v", ops, err)
	}
}

// A failed Docker copy leaves the sandbox as it was, with a report that can be dismissed.
func TestResizeDisksFailureRollsBack(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	target := vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)
	tf.fail("import-docker:"+target, errors.New("injected"))

	if _, err := resizeDisksNow(f, f.sandbox.ID, DiskSizes{DockerMiB: 4096}); err == nil {
		t.Fatal("ResizeDisks succeeded")
	}
	view, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || view.Generation != f.sandbox.Generation || view.DockerMiB != f.sandbox.DockerMiB || view.Status != runtime.StatusRunning {
		t.Fatalf("view = %+v, %v", view, err)
	}
	if view.Progress == nil || view.Progress.Operation != OperationDisks || view.Progress.State != ProgressFailed || view.Progress.Step != StepCopy {
		t.Fatalf("progress = %+v", view.Progress)
	}
	if tf.hasVM(target) {
		t.Fatal("target VM remains")
	}
	if err := f.manager.ClearProgress(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
}
