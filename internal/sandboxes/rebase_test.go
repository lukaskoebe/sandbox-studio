package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"io"
	goruntime "runtime"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
)

// transferFake adds transfer mode and workspace copies to fakeSandboxRuntime. A workspace
// is a byte string per VM name.
type transferFake struct {
	*fakeSandboxRuntime
	mu         sync.Mutex
	workspaces map[string][]byte
	specs      map[string]runtime.Spec
	labels     map[string]map[string]string
	booted     map[string]bool
}

func newTransferFake(f *checkpointFixture) *transferFake {
	tf := &transferFake{fakeSandboxRuntime: f.runtime, workspaces: map[string][]byte{}, specs: map[string]runtime.Spec{},
		labels: map[string]map[string]string{}, booted: map[string]bool{}}
	f.manager.Runtime = tf
	f.manager.Templates = perTemplateResolver{}
	return tf
}

func (f *transferFake) CreateForTransfer(_ context.Context, name string, spec runtime.Spec, _ string, labels map[string]string) error {
	f.record("create-transfer:" + name)
	if err := f.failure("create-transfer:" + name); err != nil {
		return err
	}
	f.mu.Lock()
	f.specs[name], f.labels[name] = spec, cloneStringMap(labels)
	f.mu.Unlock()
	f.setStatus(name, runtime.StatusRunning)
	return nil
}

func (f *transferFake) StartForTransfer(_ context.Context, name string) error {
	f.record("start-transfer:" + name)
	if err := f.failure("start-transfer:" + name); err != nil {
		return err
	}
	f.setStatus(name, runtime.StatusRunning)
	return nil
}

func (f *transferFake) Boot(_ context.Context, name string) error {
	f.record("boot:" + name)
	if err := f.failure("boot:" + name); err != nil {
		return err
	}
	f.mu.Lock()
	f.booted[name] = true
	f.mu.Unlock()
	return nil
}

func (f *transferFake) ExportWorkspace(ctx context.Context, owned runtime.OwnedVM, w io.Writer) error {
	f.record("export:" + owned.Name)
	if err := f.running(ctx, owned.Name); err != nil {
		return err
	}
	if err := f.failure("export:" + owned.Name); err != nil {
		return err
	}
	f.mu.Lock()
	data := f.workspaces[owned.Name]
	f.mu.Unlock()
	_, err := w.Write(data)
	return err
}

func (f *transferFake) ImportWorkspace(ctx context.Context, owned runtime.OwnedVM, r io.Reader) error {
	f.record("import:" + owned.Name)
	if err := f.running(ctx, owned.Name); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if err := f.failure("import:" + owned.Name); err != nil {
		return err
	}
	f.mu.Lock()
	f.workspaces[owned.Name] = data
	f.mu.Unlock()
	return nil
}

func (f *transferFake) running(ctx context.Context, name string) error {
	status, err := f.Status(ctx, name)
	if err != nil {
		return err
	}
	if status != runtime.StatusRunning {
		return fmt.Errorf("%s is %s", name, status)
	}
	return nil
}

func (f *transferFake) workspace(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.workspaces[name])
}

func (f *transferFake) setWorkspace(name, data string) {
	f.mu.Lock()
	f.workspaces[name] = []byte(data)
	f.mu.Unlock()
}

type perTemplateResolver struct{}

func (perTemplateResolver) Resolve(_ context.Context, envID, templateID string) (templateregistry.Reference, error) {
	return templateReference(envID, templateID), nil
}

func rebaseTemplate(t *testing.T, f *checkpointFixture, res resources.Resources) store.Template {
	t.Helper()
	return readyTemplateForManager(t, f, canonicalTemplateSpec(t, res), "linux/"+goruntime.GOARCH, templateimage.ExporterVersion)
}

var rebaseResources = resources.Resources{CPUs: 2, MemoryMiB: 1024, MaxMemoryMiB: 2048, WorkspaceMiB: 4096, DockerMiB: 2048}

func TestRebaseRunningSandboxCopiesWorkspaceAndBootsTarget(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	checkpoint := f.createReadyCheckpoint(t, "before")

	view, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := VMName(view.Sandbox)
	if view.Generation != f.sandbox.Generation+1 || view.TemplateID != template.ID || view.Status != runtime.StatusRunning {
		t.Fatalf("view = %+v", view)
	}
	if view.CPUs != 2 || view.MemoryMiB != 1024 || view.MaxMemoryMiB != 2048 || view.WorkspaceMiB != 4096 || view.DockerMiB != 2048 {
		t.Fatalf("view resources = %+v", view.Sandbox)
	}
	if tf.workspace(target) != "files" || !tf.booted[target] {
		t.Fatalf("target workspace %q, booted %v", tf.workspace(target), tf.booted[target])
	}
	if tf.hasVM(source) {
		t.Fatal("source VM remains")
	}
	spec := tf.specs[target]
	if spec.CPUs != 2 || spec.WorkspaceMiB != 4096 || spec.Image == nil || spec.Egress != (fakeEgress{}).egress(f.sandbox.ID) {
		t.Fatalf("target spec = %+v", spec)
	}
	if tf.labels[target]["studio.template-id"] != template.ID || tf.labels[target]["studio.sandbox-id"] != f.sandbox.ID {
		t.Fatalf("target labels = %+v", tf.labels[target])
	}
	// The source stopped gracefully before it was started for the copy.
	if tf.countCall("stop:"+source) < 2 || tf.countCall("start-transfer:"+source) != 1 {
		t.Fatalf("calls = %v", tf.calls)
	}
	if ops, err := f.store.Rebases(f.ctx); err != nil || len(ops) != 0 {
		t.Fatalf("rebase records = %+v, %v", ops, err)
	}
	cps, err := f.store.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(cps) != 1 || cps[0].ID != checkpoint.ID || cps[0].Generation != f.sandbox.Generation {
		t.Fatalf("checkpoints = %+v, %v", cps, err)
	}
}

func TestRebaseStoppedSandboxStaysStopped(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	tf.setWorkspace(VMName(f.sandbox), "files")

	view, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := VMName(view.Sandbox)
	if view.Status != runtime.StatusStopped || tf.booted[target] || tf.workspace(target) != "files" {
		t.Fatalf("view = %+v, booted %v, workspace %q", view, tf.booted[target], tf.workspace(target))
	}
}

func TestRebaseFailureRemovesTargetAndKeepsSourceStopped(t *testing.T) {
	for _, step := range []string{"create-transfer", "import", "export", "boot"} {
		t.Run(step, func(t *testing.T) {
			f := newCheckpointFixture(t)
			tf := newTransferFake(f)
			template := rebaseTemplate(t, f, rebaseResources)
			source := VMName(f.sandbox)
			target := vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)
			tf.setStatus(source, runtime.StatusRunning)
			tf.setWorkspace(source, "files")
			if step == "export" {
				tf.fail("export:"+source, errors.New("injected"))
			} else {
				tf.fail(step+":"+target, errors.New("injected"))
			}

			_, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID)
			if err == nil {
				t.Fatal("Rebase succeeded")
			}
			current, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
			if err != nil || current.Generation != f.sandbox.Generation || current.TemplateID != "" || current.CPUs != f.sandbox.CPUs {
				t.Fatalf("sandbox = %+v, %v", current, err)
			}
			if tf.hasVM(target) {
				t.Fatal("target VM remains")
			}
			if status, _ := tf.Status(f.ctx, source); status != runtime.StatusStopped {
				t.Fatalf("source is %s", status)
			}
			if ops, err := f.store.Rebases(f.ctx); err != nil || len(ops) != 0 {
				t.Fatalf("rebase records = %+v, %v", ops, err)
			}
			if _, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
				t.Fatalf("Start after failed rebase: %v", err)
			}
		})
	}
}

func TestRebaseRecoveryRollsBackOrFinishes(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint("committed=", committed), func(t *testing.T) {
			f := newCheckpointFixture(t)
			tf := newTransferFake(f)
			template := rebaseTemplate(t, f, rebaseResources)
			source := VMName(f.sandbox)
			target := vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)
			op, err := f.store.BeginRebase(f.ctx, store.RebaseOperation{SandboxID: f.sandbox.ID, EnvironmentID: f.env.ID, TemplateID: template.ID,
				CPUs: 2, MemoryMiB: 1024, MaxMemoryMiB: 2048, WorkspaceMiB: 4096, DockerMiB: 2048})
			if err != nil {
				t.Fatal(err)
			}
			tf.setStatus(source, runtime.StatusRunning) // started for the copy
			tf.setStatus(target, runtime.StatusRunning)
			if committed {
				if err := f.store.CommitRebase(f.ctx, op); err != nil {
					t.Fatal(err)
				}
			}

			if err := f.manager.Reconcile(f.ctx); err != nil {
				t.Fatal(err)
			}
			if ops, err := f.store.Rebases(f.ctx); err != nil || len(ops) != 0 {
				t.Fatalf("rebase records = %+v, %v", ops, err)
			}
			if committed {
				if tf.hasVM(source) || !tf.hasVM(target) {
					t.Fatalf("source present %v, target present %v", tf.hasVM(source), tf.hasVM(target))
				}
				return
			}
			if tf.hasVM(target) {
				t.Fatal("target VM remains")
			}
			if status, _ := tf.Status(f.ctx, source); status != runtime.StatusStopped {
				t.Fatalf("source is %s", status)
			}
		})
	}
}

func TestRebaseRequiresTemplateInSameEnvironment(t *testing.T) {
	f := newCheckpointFixture(t)
	newTransferFake(f)
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing template: err = %v", err)
	}
	if ops, err := f.store.Rebases(f.ctx); err != nil || len(ops) != 0 {
		t.Fatalf("rebase records = %+v, %v", ops, err)
	}
}

func TestRebaseAndForkAreSerialized(t *testing.T) {
	f := newCheckpointFixture(t)
	newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	unlock, err := f.manager.tryMutation(f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Rebase err = %v, want ErrBusy", err)
	}
	if _, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Fork err = %v, want ErrBusy", err)
	}
}

func TestForkCopiesWorkspaceIntoNewSandbox(t *testing.T) {
	for _, running := range []bool{true, false} {
		t.Run(fmt.Sprint("running=", running), func(t *testing.T) {
			f := newCheckpointFixture(t)
			tf := newTransferFake(f)
			source := VMName(f.sandbox)
			if running {
				tf.setStatus(source, runtime.StatusRunning)
			}
			tf.setWorkspace(source, "files")

			view, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy")
			if err != nil {
				t.Fatal(err)
			}
			fork := VMName(view.Sandbox)
			if view.ID == f.sandbox.ID || view.Name != "copy" || view.Generation != 1 || view.CPUs != f.sandbox.CPUs || view.WorkspaceMiB != f.sandbox.WorkspaceMiB {
				t.Fatalf("view = %+v", view)
			}
			if tf.workspace(fork) != "files" || !tf.booted[fork] || view.Status != runtime.StatusRunning {
				t.Fatalf("fork workspace %q, booted %v, status %s", tf.workspace(fork), tf.booted[fork], view.Status)
			}
			if tf.specs[fork].Egress != (fakeEgress{}).egress(view.ID) {
				t.Fatalf("fork egress = %+v", tf.specs[fork].Egress)
			}
			want := runtime.StatusStopped
			if running {
				want = runtime.StatusRunning
			}
			if status, _ := tf.Status(f.ctx, source); status != want {
				t.Fatalf("source is %s, want %s", status, want)
			}
			if running && tf.countCall("stop:"+source) != 0 {
				t.Fatal("a running source was stopped")
			}
		})
	}
}

func TestForkFailureRemovesFork(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	source := VMName(f.sandbox)
	tf.setWorkspace(source, "files")
	tf.fail("export:"+source, errors.New("injected"))

	if _, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy"); err == nil {
		t.Fatal("Fork succeeded")
	}
	all, err := f.store.AllSandboxes(f.ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("sandboxes = %+v, %v", all, err)
	}
	if status, _ := tf.Status(f.ctx, source); status != runtime.StatusStopped {
		t.Fatalf("source is %s", status)
	}
	for name := range tf.specs {
		if tf.hasVM(name) {
			t.Fatalf("fork VM %s remains", name)
		}
	}
}

func TestCopyWorkspaceStopsAtLimit(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	src, dst := runtime.OwnedVM{Name: "src"}, runtime.OwnedVM{Name: "dst"}
	tf.setStatus("src", runtime.StatusRunning)
	tf.setStatus("dst", runtime.StatusRunning)
	tf.setWorkspace("src", "0123456789")
	if err := copyWorkspace(f.ctx, tf, src, dst, 9); !errors.Is(err, ErrWorkspaceTooLarge) {
		t.Fatalf("err = %v, want ErrWorkspaceTooLarge", err)
	}
	if tf.workspace("dst") != "" {
		t.Fatal("import succeeded past the limit")
	}
	if err := copyWorkspace(f.ctx, tf, src, dst, 10); err != nil || tf.workspace("dst") != "0123456789" {
		t.Fatalf("err = %v, workspace %q", err, tf.workspace("dst"))
	}
}
