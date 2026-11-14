package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
)

// transferFake adds transfer mode and tree copies to fakeSandboxRuntime. A workspace, a
// home, Docker's data and the tools are a byte string per VM name. Workspace copies record
// export:<vm> and import:<vm>, home copies export-home:<vm> and import-home:<vm>, Docker
// copies export-docker:<vm> and import-docker:<vm>, and tools export-tools:<vm> and
// import-tools:<vm>.
type transferFake struct {
	*fakeSandboxRuntime
	mu           sync.Mutex
	workspaces   map[string][]byte
	homes        map[string][]byte
	dockers      map[string][]byte
	tools        map[string][]byte
	specs        map[string]runtime.Spec
	labels       map[string]map[string]string
	booted       map[string]bool
	sockets      map[string]string
	toolsHave    map[string][]string // what ToolsHave answers, by VM
	toolsSkipped map[string][]string // what each VM's tools export was told to leave out
	guestBoots   bool                // a booted VM's agent connects and answers, see answerGuest
	busy         map[string]bool     // VMs with a command running, as the runtime allows one per VM
}

func newTransferFake(f *checkpointFixture) *transferFake {
	tf := &transferFake{fakeSandboxRuntime: f.runtime, workspaces: map[string][]byte{}, homes: map[string][]byte{}, dockers: map[string][]byte{}, tools: map[string][]byte{}, specs: map[string]runtime.Spec{},
		labels: map[string]map[string]string{}, booted: map[string]bool{}, sockets: map[string]string{}, busy: map[string]bool{},
		toolsHave: map[string][]string{}, toolsSkipped: map[string][]string{}}
	f.manager.Runtime = tf
	f.manager.Templates = perTemplateResolver{}
	return tf
}

func (f *transferFake) CreateForTransfer(_ context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	f.record("create-transfer:" + name)
	if err := f.failure("create-transfer:" + name); err != nil {
		return err
	}
	f.mu.Lock()
	f.specs[name], f.labels[name], f.sockets[name] = spec, cloneStringMap(labels), socket
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
	socket := f.sockets[name]
	f.mu.Unlock()
	if f.guestBoots {
		return answerGuest(socket)
	}
	return nil
}

func (f *transferFake) trees(tree runtime.Tree) (map[string][]byte, string) {
	switch tree {
	case runtime.TreeHome:
		return f.homes, "-home"
	case runtime.TreeDocker:
		return f.dockers, "-docker"
	case runtime.TreeTools:
		return f.tools, "-tools"
	}
	return f.workspaces, ""
}

// run takes the VM's one command slot as the runtime does, failing with ErrRunPending while
// another command runs there. It holds the slot a moment, so commands that overlap do.
func (f *transferFake) run(name string) (done func(), err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[name] {
		return nil, runtime.ErrRunPending
	}
	f.busy[name] = true
	time.Sleep(5 * time.Millisecond)
	return func() {
		f.mu.Lock()
		delete(f.busy, name)
		f.mu.Unlock()
	}, nil
}

func (f *transferFake) ExportTree(ctx context.Context, owned runtime.OwnedVM, tree runtime.Tree, opts runtime.ExportOptions, w io.Writer) error {
	trees, suffix := f.trees(tree)
	f.record("export" + suffix + ":" + owned.Name)
	if tree == runtime.TreeTools {
		f.mu.Lock()
		f.toolsSkipped[owned.Name] = opts.ToolsHave
		f.mu.Unlock()
	}
	done, err := f.run(owned.Name)
	if err != nil {
		return err
	}
	defer done()
	if err := f.running(ctx, owned.Name); err != nil {
		return err
	}
	if err := f.failure("export" + suffix + ":" + owned.Name); err != nil {
		return err
	}
	f.mu.Lock()
	data := trees[owned.Name]
	f.mu.Unlock()
	_, err = w.Write(data)
	return err
}

// ToolsHave answers what toolsHave says the VM has.
func (f *transferFake) ToolsHave(_ context.Context, owned runtime.OwnedVM) ([]string, error) {
	f.record("tools-have:" + owned.Name)
	if err := f.failure("tools-have:" + owned.Name); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.toolsHave[owned.Name], nil
}

func (f *transferFake) ImportTree(ctx context.Context, owned runtime.OwnedVM, tree runtime.Tree, r io.Reader, progress func(copied, total int64)) error {
	trees, suffix := f.trees(tree)
	f.record("import" + suffix + ":" + owned.Name)
	done, err := f.run(owned.Name)
	if err != nil {
		return err
	}
	defer done()
	if err := f.running(ctx, owned.Name); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if err := f.failure("import" + suffix + ":" + owned.Name); err != nil {
		return err
	}
	if progress != nil {
		progress(int64(len(data)), int64(len(data)))
	}
	f.mu.Lock()
	trees[owned.Name] = data
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

func (f *transferFake) home(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.homes[name])
}

func (f *transferFake) setHome(name, data string) {
	f.mu.Lock()
	f.homes[name] = []byte(data)
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

// rebaseNow rebases sandbox id and waits for the outcome, as the API's wait=true does: the
// error of a refused request or of the background rebase, else the view after it.
func rebaseNow(f *checkpointFixture, id, templateID string) (View, error) {
	return rebaseKeeping(f, id, templateID, false)
}

// rebaseKeeping is rebaseNow, keeping the tools in /opt/mise with keepTools.
func rebaseKeeping(f *checkpointFixture, id, templateID string, keepTools bool) (View, error) {
	return rebaseWith(f, id, templateID, RebaseOptions{KeepTools: keepTools})
}

// rebaseWith is rebaseNow with what keep asks to copy.
func rebaseWith(f *checkpointFixture, id, templateID string, keep RebaseOptions) (View, error) {
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, id, templateID, keep); err != nil {
		return View{}, err
	}
	if err := f.manager.Wait(f.ctx, id); err != nil {
		return View{}, err
	}
	return f.manager.Get(f.ctx, f.env.ID, id)
}

// blockingTransfer holds CreateForTransfer until release is closed, after reporting a pull.
type blockingTransfer struct {
	*transferFake
	release chan struct{}
}

func (b *blockingTransfer) CreateForTransfer(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	if spec.Progress != nil {
		spec.Progress(runtime.Progress{Phase: runtime.PhasePull, Pull: runtime.PullProgress{Layers: 2, LayersDone: 1}})
	}
	<-b.release
	return b.transferFake.CreateForTransfer(ctx, name, spec, socket, labels)
}

func TestRebaseAnswersAtOnceAndPublishesItsSteps(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	blocking := &blockingTransfer{transferFake: tf, release: make(chan struct{})}
	f.manager.Runtime = blocking

	answered, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID, RebaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := answered.Progress
	if answered.Status != StatusCreating || p == nil || p.Operation != OperationRebase || p.State != ProgressRunning ||
		!slices.Equal(p.Steps, rebaseRunningSteps) {
		t.Fatalf("answer = %+v, progress %+v", answered, p)
	}
	// While the image pulls, the view shows the pull, and the sandbox is busy. It keeps running:
	// the new VM is made before the old one stops.
	waitUntil(t, func() bool {
		v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
		return err == nil && v.Progress != nil && v.Progress.Step == StepPull && v.Progress.Pull != nil
	})
	if st, _ := tf.Status(f.ctx, source); st != runtime.StatusRunning || tf.countCall("stop:"+source) != 0 {
		t.Fatalf("during the pull the source is %s, calls %v", st, tf.calls)
	}
	if _, err := f.manager.Stop(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Stop during a rebase: err = %v, want ErrBusy", err)
	}
	if err := f.manager.ClearProgress(f.ctx, f.env.ID, f.sandbox.ID); err == nil {
		t.Fatal("a running rebase was dismissed")
	}
	close(blocking.release)
	if err := f.manager.Wait(f.ctx, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	view, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Progress != nil || view.Status != runtime.StatusRunning || view.TemplateID != template.ID {
		t.Fatalf("after the rebase: %+v, progress %+v", view, view.Progress)
	}
	// A rebase saves no progress: its rebase record recovers it.
	if saved, err := f.store.SettingsWithPrefix(f.ctx, progressKeyPrefix); err != nil || len(saved) != 0 {
		t.Fatalf("saved progress = %v, %v", saved, err)
	}
}

// A rebase while another rebase or resize copies waits its turn, queued, without touching
// its VM, and runs once the other is done.
func TestRebaseWaitsForAnotherCopy(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	f.manager.Runtime = tf

	other, err := f.manager.waitForCopy(f.ctx, "another-sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID, RebaseOptions{}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
		return err == nil && v.Progress != nil && v.Progress.Queued
	})
	time.Sleep(50 * time.Millisecond)
	v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Progress == nil || !v.Progress.Queued || v.Progress.Step != StepPull || v.Status != StatusCreating {
		t.Fatalf("queued view = %+v, progress %+v", v, v.Progress)
	}
	if st, _ := tf.Status(f.ctx, source); st != runtime.StatusRunning || tf.countCall("create-transfer:"+vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)) != 0 {
		t.Fatalf("the queued sandbox's VM is %s, calls %v", st, tf.calls)
	}

	other()
	if err := f.manager.Wait(f.ctx, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if v, err = f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID); err != nil || v.Progress != nil || v.TemplateID != template.ID {
		t.Fatalf("after the rebase: %+v, progress %+v (%v)", v, v.Progress, err)
	}
	// The slot is free again.
	release, err := f.manager.waitForCopy(f.ctx, "another-sandbox")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// reportingTransfer holds an import of the workspace after it reported its progress.
type reportingTransfer struct {
	*transferFake
	reported, release chan struct{}
}

func (r *reportingTransfer) ImportTree(ctx context.Context, owned runtime.OwnedVM, tree runtime.Tree, rd io.Reader, progress func(copied, total int64)) error {
	if tree == runtime.TreeWorkspace {
		progress(100, 300)
		close(r.reported)
		<-r.release
	}
	return r.transferFake.ImportTree(ctx, owned, tree, rd, progress)
}

func TestRebaseShowsHowFarItsCopyGot(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	reporting := &reportingTransfer{transferFake: tf, reported: make(chan struct{}), release: make(chan struct{})}
	f.manager.Runtime = reporting

	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID, RebaseOptions{}); err != nil {
		t.Fatal(err)
	}
	<-reporting.reported
	v, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := CopyProgress{Tree: "workspace", Bytes: 100, TotalBytes: 300}
	if v.Progress == nil || v.Progress.Step != StepCopy || v.Progress.Copy == nil || *v.Progress.Copy != want {
		t.Fatalf("progress = %+v, copy %+v", v.Progress, v.Progress.Copy)
	}
	close(reporting.release)
	if err := f.manager.Wait(f.ctx, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFailedRebaseNamesItsStepUntilDismissed(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	tf.fail("export:"+source, errors.New("injected"))

	if _, err := rebaseNow(f, f.sandbox.ID, template.ID); err == nil {
		t.Fatal("rebase succeeded")
	}
	view, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The sandbox is back as it was, running on its old template; the progress tells why.
	p := view.Progress
	if view.Status != runtime.StatusRunning || view.TemplateID != "" || p == nil || p.Operation != OperationRebase ||
		p.State != ProgressFailed || p.Step != StepCopy || !strings.Contains(p.Error, "injected") {
		t.Fatalf("view = %+v, progress %+v", view, p)
	}
	// It is not a failed creation: the sandbox stops and starts as usual.
	if _, err := f.manager.Stop(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.ClearProgress(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if view, _ := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID); view.Progress != nil {
		t.Fatalf("progress after dismissing = %+v", view.Progress)
	}
	// The next rebase replaces a failed one's report too.
	if _, err := rebaseNow(f, f.sandbox.ID, template.ID); err == nil {
		t.Fatal("rebase succeeded")
	}
	tf.clearFailure("export:" + source)
	if _, err := rebaseNow(f, f.sandbox.ID, template.ID); err != nil {
		t.Fatal(err)
	}
	if view, _ := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID); view.Progress != nil || view.TemplateID != template.ID {
		t.Fatalf("after a successful rebase: %+v, progress %+v", view, view.Progress)
	}
}

func TestRebaseOfStoppedSandboxHasNoBootStep(t *testing.T) {
	f := newCheckpointFixture(t)
	newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	answered, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID, RebaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if answered.Progress == nil || !slices.Equal(answered.Progress.Steps, rebaseStoppedSteps) {
		t.Fatalf("progress = %+v", answered.Progress)
	}
	if err := f.manager.Wait(f.ctx, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRebaseRunningSandboxCopiesWorkspaceAndBootsTarget(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	tf.setHome(source, "opencode sessions")
	checkpoint := f.createReadyCheckpoint(t, "before")
	var logged syncBuffer
	f.manager.Log = slog.New(slog.NewTextHandler(&logged, nil))

	view, err := rebaseNow(f, f.sandbox.ID, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Each phase says how long it took, and each tree its bytes and rate.
	for _, want := range []string{`phase="new VM"`, "phase=stop ", `phase="start for the copy"`, "tree=workspace", "tree=home", "phase=copy ", "phase=boot", "sandbox copy finished"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log lacks %s:\n%s", want, logged.String())
		}
	}
	target := VMName(view.Sandbox)
	if view.Generation != f.sandbox.Generation+1 || view.TemplateID != template.ID || view.Status != runtime.StatusRunning {
		t.Fatalf("view = %+v", view)
	}
	if view.CPUs != 2 || view.MemoryMiB != 1024 || view.MaxMemoryMiB != 2048 || view.WorkspaceMiB != 4096 || view.DockerMiB != 2048 {
		t.Fatalf("view resources = %+v", view.Sandbox)
	}
	if tf.workspace(target) != "files" || tf.home(target) != "opencode sessions" || !tf.booted[target] {
		t.Fatalf("target workspace %q, home %q, booted %v", tf.workspace(target), tf.home(target), tf.booted[target])
	}
	if tf.hasVM(source) {
		t.Fatal("source VM remains")
	}
	spec := tf.specs[target]
	if spec.CPUs != 2 || spec.WorkspaceMiB != 4096 || spec.HomeMiB != DefaultHomeMiB || spec.Image == nil || spec.Egress != (fakeEgress{}).egress(f.sandbox.ID) {
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

	view, err := rebaseNow(f, f.sandbox.ID, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := VMName(view.Sandbox)
	if view.Status != runtime.StatusStopped || tf.booted[target] || tf.workspace(target) != "files" {
		t.Fatalf("view = %+v, booted %v, workspace %q", view, tf.booted[target], tf.workspace(target))
	}
}

// Whatever state a sandbox is in, the copy starts its VM for itself. One that wasn't up stays
// down, and one that is coming up comes up on the new generation.
func TestRebaseTakesASandboxInAnyStateWithAVM(t *testing.T) {
	for status, want := range map[runtime.Status]runtime.Status{
		runtime.StatusSuspended: runtime.StatusStopped,
		runtime.StatusCrashed:   runtime.StatusStopped,
		runtime.StatusDraining:  runtime.StatusStopped,
		runtime.StatusCreated:   runtime.StatusStopped,
		runtime.StatusStarting:  runtime.StatusRunning,
	} {
		t.Run(string(status), func(t *testing.T) {
			f := newCheckpointFixture(t)
			tf := newTransferFake(f)
			template := rebaseTemplate(t, f, rebaseResources)
			source := VMName(f.sandbox)
			tf.setWorkspace(source, "files")
			tf.setStatus(source, status)
			view, err := rebaseNow(f, f.sandbox.ID, template.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Status != want || tf.workspace(VMName(view.Sandbox)) != "files" {
				t.Fatalf("view = %+v, workspace %q", view, tf.workspace(VMName(view.Sandbox)))
			}
		})
	}
}

func TestRebaseOfASandboxWithoutAVMIsRefused(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	if err := tf.Remove(f.ctx, VMName(f.sandbox)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID, RebaseOptions{}); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("err = %v", err)
	}
}

func TestRebaseFailureRemovesTargetAndRestoresSource(t *testing.T) {
	for _, running := range []bool{true, false} {
		for _, step := range []string{"create-transfer", "import", "export", "boot"} {
			if step == "boot" && !running {
				continue
			}
			t.Run(fmt.Sprintf("%s/running=%t", step, running), func(t *testing.T) {
				f := newCheckpointFixture(t)
				tf := newTransferFake(f)
				template := rebaseTemplate(t, f, rebaseResources)
				source := VMName(f.sandbox)
				target := vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)
				want, wantStarts := runtime.StatusStopped, 0
				if running {
					tf.setStatus(source, runtime.StatusRunning)
					want, wantStarts = runtime.StatusRunning, 1
				}
				tf.setWorkspace(source, "files")
				if step == "export" {
					tf.fail("export:"+source, errors.New("injected"))
				} else {
					tf.fail(step+":"+target, errors.New("injected"))
				}

				_, err := rebaseNow(f, f.sandbox.ID, template.ID)
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
				if status, _ := tf.Status(f.ctx, source); status != want {
					t.Fatalf("source is %s, want %s", status, want)
				}
				// A running source comes back through the normal start path, with services. One
				// whose new VM couldn't be made was never stopped.
				if step == "create-transfer" {
					wantStarts = 0
					if tf.countCall("stop:"+source) != 0 || tf.countCall("start-transfer:"+source) != 0 {
						t.Fatalf("the source was touched: calls = %v", tf.calls)
					}
				}
				if tf.countCall("start:"+source) != wantStarts {
					t.Fatalf("calls = %v", tf.calls)
				}
				if ops, err := f.store.Rebases(f.ctx); err != nil || len(ops) != 0 {
					t.Fatalf("rebase records = %+v, %v", ops, err)
				}
			})
		}
	}
}

func TestRebaseFailureReportsSourceRestartFailure(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	target := vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	importErr, startErr := errors.New("import failed"), errors.New("start failed")
	tf.fail("import:"+target, importErr)
	tf.fail("start:"+source, startErr)

	_, err := rebaseNow(f, f.sandbox.ID, template.ID)
	if !errors.Is(err, importErr) || !errors.Is(err, startErr) {
		t.Fatalf("err = %v", err)
	}
	if tf.hasVM(target) {
		t.Fatal("target VM remains")
	}
	if ops, err := f.store.Rebases(f.ctx); err != nil || len(ops) != 0 {
		t.Fatalf("rebase records = %+v, %v", ops, err)
	}
	tf.clearFailure("start:" + source)
	if _, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatalf("Start after failed restart: %v", err)
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
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, "missing", RebaseOptions{}); !errors.Is(err, store.ErrNotFound) {
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
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID, RebaseOptions{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Rebase err = %v, want ErrBusy", err)
	}
	if _, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy", ForkOptions{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Fork err = %v, want ErrBusy", err)
	}
}

func TestForkCopiesWorkspaceAndHomeIntoNewSandbox(t *testing.T) {
	for _, running := range []bool{true, false} {
		t.Run(fmt.Sprint("running=", running), func(t *testing.T) {
			f := newCheckpointFixture(t)
			tf := newTransferFake(f)
			tf.guestBoots = true
			source := VMName(f.sandbox)
			if running {
				tf.setStatus(source, runtime.StatusRunning)
			}
			tf.setWorkspace(source, "files")
			tf.setHome(source, "dotfiles")

			answered, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy", ForkOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.manager.Wait(f.ctx, answered.ID); err != nil {
				t.Fatal(err)
			}
			view, err := f.manager.Get(f.ctx, f.env.ID, answered.ID)
			if err != nil {
				t.Fatal(err)
			}
			fork := VMName(view.Sandbox)
			if view.ID == f.sandbox.ID || view.Name != "copy" || view.Generation != 1 || view.CPUs != f.sandbox.CPUs || view.WorkspaceMiB != f.sandbox.WorkspaceMiB {
				t.Fatalf("view = %+v", view)
			}
			if tf.workspace(fork) != "files" || tf.home(fork) != "dotfiles" || !tf.booted[fork] || view.Status != runtime.StatusRunning {
				t.Fatalf("fork workspace %q, home %q, booted %v, status %s", tf.workspace(fork), tf.home(fork), tf.booted[fork], view.Status)
			}
			if tf.specs[fork].HomeMiB != DefaultHomeMiB {
				t.Fatalf("fork spec = %+v", tf.specs[fork])
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

	answered, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy", ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, answered.ID); err == nil {
		t.Fatal("Fork succeeded")
	}
	// A failed fork keeps its record, in state failed, until it is deleted.
	failed, err := f.manager.Get(f.ctx, f.env.ID, answered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed || failed.Progress == nil || failed.Progress.Step != StepCopy {
		t.Fatalf("failed fork = %+v; want it failed while copying", failed)
	}
	all, err := f.store.AllSandboxes(f.ctx)
	if err != nil || len(all) != 2 {
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

func TestCopyTreeStopsAtLimit(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	src, dst := runtime.OwnedVM{Name: "src"}, runtime.OwnedVM{Name: "dst"}
	tf.setStatus("src", runtime.StatusRunning)
	tf.setStatus("dst", runtime.StatusRunning)
	tf.setWorkspace("src", "0123456789")
	tf.setHome("src", "0123456789")
	if err := copyTree(f.ctx, tf, src, dst, runtime.TreeWorkspace, &limitWriter{left: 9, err: ErrWorkspaceTooLarge}, nil); !errors.Is(err, ErrWorkspaceTooLarge) {
		t.Fatalf("err = %v, want ErrWorkspaceTooLarge", err)
	}
	if err := copyTree(f.ctx, tf, src, dst, runtime.TreeHome, &limitWriter{left: 9, err: ErrHomeTooLarge}, nil); !errors.Is(err, ErrHomeTooLarge) {
		t.Fatalf("err = %v, want ErrHomeTooLarge", err)
	}
	if tf.workspace("dst") != "" || tf.home("dst") != "" {
		t.Fatal("import succeeded past the limit")
	}
	if err := copyTree(f.ctx, tf, src, dst, runtime.TreeWorkspace, &limitWriter{left: 10, err: ErrWorkspaceTooLarge}, nil); err != nil || tf.workspace("dst") != "0123456789" {
		t.Fatalf("err = %v, workspace %q", err, tf.workspace("dst"))
	}
}

func TestRebaseHomeFailureRollsBack(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.fail("export-home:"+source, ErrHomeTooLarge)

	if _, err := rebaseNow(f, f.sandbox.ID, template.ID); !errors.Is(err, ErrHomeTooLarge) {
		t.Fatalf("err = %v, want ErrHomeTooLarge", err)
	}
	current, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || current.Generation != f.sandbox.Generation {
		t.Fatalf("sandbox = %+v, %v", current, err)
	}
	if tf.hasVM(vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)) {
		t.Fatal("target VM remains")
	}
	if status, _ := tf.Status(f.ctx, source); status != runtime.StatusRunning {
		t.Fatalf("source is %s, want running again", status)
	}
}

func TestRebaseOntoTheCurrentBaseKeepsResources(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	f.manager.BaseImage, f.manager.BaseVersion = "sandbox-studio-base:new", "v0.8.1"
	template := rebaseTemplate(t, f, rebaseResources)
	tf.setWorkspace(VMName(f.sandbox), "files")
	onTemplate, err := rebaseNow(f, f.sandbox.ID, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The template's base is another one than new sandboxes get.
	if onTemplate.BaseOutdated != true || onTemplate.BaseImage != template.BaseRef {
		t.Fatalf("on the template: %+v", onTemplate)
	}

	view, err := rebaseNow(f, f.sandbox.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.TemplateID != "" || view.BaseImage != "sandbox-studio-base:new" || view.BaseVersion != "v0.8.1" || view.BaseOutdated {
		t.Fatalf("view = %+v", view)
	}
	if view.CPUs != 2 || view.MemoryMiB != 1024 || view.MaxMemoryMiB != 2048 || view.WorkspaceMiB != 4096 || view.DockerMiB != 2048 {
		t.Fatalf("view resources = %+v", view.Sandbox)
	}
	target := VMName(view.Sandbox)
	if spec := tf.specs[target]; spec.Image != nil || spec.HomeMiB != DefaultHomeMiB {
		t.Fatalf("target spec = %+v", spec)
	}
	if tf.workspace(target) != "files" {
		t.Fatalf("target workspace %q", tf.workspace(target))
	}
	events, err := f.store.SandboxEvents(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || events[len(events)-1].Kind != store.EventRebase || events[len(events)-1].FromTemplateID != template.ID {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

// imageRuntime knows the image each VM was made from, as msb's catalog does.
type imageRuntime struct {
	*fakeSandboxRuntime
	images map[string]string
}

func (r imageRuntime) ImageOf(_ context.Context, name string) (string, error) {
	return r.images[name], nil
}

func TestReconcileLearnsTheBaseOfOlderSandboxes(t *testing.T) {
	f := newCheckpointFixture(t)
	f.manager.BaseImage = "ghcr.io/lukaskoebe/sandbox-studio-base@sha256:new"
	f.manager.Runtime = imageRuntime{f.runtime, map[string]string{VMName(f.sandbox): "ghcr.io/lukaskoebe/sandbox-studio-base@sha256:old"}}
	if err := f.manager.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	view, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.BaseImage != "ghcr.io/lukaskoebe/sandbox-studio-base@sha256:old" || view.BaseVersion != "" || !view.BaseOutdated {
		t.Fatalf("view = %+v", view)
	}
}

func TestNewSandboxesRecordTheirBase(t *testing.T) {
	f := newCheckpointFixture(t)
	f.manager.BaseImage, f.manager.BaseVersion = "sandbox-studio-base:dev", "v0.8.1"
	rec, err := f.manager.createOwnedRecord(f.ctx, f.env.ID, "fresh", rebaseResources, DefaultHomeMiB, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.BaseImage != "sandbox-studio-base:dev" || rec.BaseVersion != "v0.8.1" {
		t.Fatalf("record = %+v", rec)
	}
	// msb's catalog may name a Docker Hub image in full.
	if f.manager.BaseOutdated("docker.io/library/sandbox-studio-base:dev") || !f.manager.BaseOutdated("sandbox-studio-base:v0.6.1") || f.manager.BaseOutdated("") {
		t.Fatal("BaseOutdated compares the wrong references")
	}
}

// A rebase copies the tools in /opt/mise only when asked to keep them, after the workspace
// and the home; the guest merges them into the new template's (guest.ImportTools).
func TestRebaseKeepsToolsWhenAsked(t *testing.T) {
	for _, keep := range []bool{false, true} {
		f := newCheckpointFixture(t)
		tf := newTransferFake(f)
		template := rebaseTemplate(t, f, rebaseResources)
		source := VMName(f.sandbox)
		tf.setStatus(source, runtime.StatusRunning)
		tf.setWorkspace(source, "files")
		tf.mu.Lock()
		tf.tools[source] = []byte("node 20")
		// The new VM's image has node 22, which the export leaves out.
		tf.toolsHave[vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)] = []string{"installs/node/22.1.0"}
		tf.mu.Unlock()

		view, err := rebaseKeeping(f, f.sandbox.ID, template.ID, keep)
		if err != nil {
			t.Fatal(err)
		}
		target := VMName(view.Sandbox)
		tf.mu.Lock()
		got := string(tf.tools[target])
		tf.mu.Unlock()
		if keep && (got != "node 20" || tf.countCall("export-tools:"+source) != 1 || tf.countCall("import-tools:"+target) != 1) {
			t.Fatalf("kept tools %q, calls %v", got, tf.calls)
		}
		tf.mu.Lock()
		skipped := tf.toolsSkipped[source]
		tf.mu.Unlock()
		if keep && !slices.Equal(skipped, []string{"installs/node/22.1.0"}) {
			t.Fatalf("the export left out %q", skipped)
		}
		if !keep && (got != "" || tf.countCall("export-tools:"+source) != 0) {
			t.Fatalf("tools copied without asking: %q, calls %v", got, tf.calls)
		}
	}

	// A failed tools copy fails the rebase, which leaves the sandbox as it was.
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")
	tf.fail("export-tools:"+source, ErrToolsTooLarge)
	// A new VM that can't say what it has gets everything.
	tf.fail("tools-have:"+vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1), errors.New("no tools-have"))
	if _, err := rebaseKeeping(f, f.sandbox.ID, template.ID, true); !errors.Is(err, ErrToolsTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if view, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID); err != nil || view.TemplateID != "" || view.Status != runtime.StatusRunning {
		t.Fatalf("after the failed rebase: %+v %v", view, err)
	}
}

func TestRebaseKeepsDockerWhenAsked(t *testing.T) {
	for _, keep := range []bool{false, true} {
		f := newCheckpointFixture(t)
		tf := newTransferFake(f)
		template := rebaseTemplate(t, f, rebaseResources)
		source := VMName(f.sandbox)
		tf.setStatus(source, runtime.StatusRunning)
		tf.setWorkspace(source, "files")
		tf.mu.Lock()
		tf.dockers[source] = []byte("images")
		tf.mu.Unlock()

		view, err := rebaseWith(f, f.sandbox.ID, template.ID, RebaseOptions{KeepDocker: keep})
		if err != nil {
			t.Fatal(err)
		}
		tf.mu.Lock()
		got := string(tf.dockers[VMName(view.Sandbox)])
		tf.mu.Unlock()
		if keep && got != "images" || !keep && got != "" {
			t.Fatalf("keep %v: the rebased sandbox has docker %q", keep, got)
		}
	}
}

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

func TestForkCopiesDockerAndToolsWhenAsked(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	tf.guestBoots = true
	source := VMName(f.sandbox)
	tf.setWorkspace(source, "files")
	tf.mu.Lock()
	tf.dockers[source], tf.tools[source] = []byte("images"), []byte("mise")
	tf.mu.Unlock()

	fork := func(name string, opts ForkOptions) string {
		t.Helper()
		answered, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, name, opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Wait(f.ctx, answered.ID); err != nil {
			t.Fatal(err)
		}
		return VMName(answered.Sandbox)
	}
	plain := fork("plain", ForkOptions{})
	bare := fork("bare", ForkOptions{Tools: no()})
	full := fork("full", ForkOptions{Docker: yes(), Tools: yes()})

	tf.mu.Lock()
	defer tf.mu.Unlock()
	// The tools come along unless asked otherwise; Docker only when asked.
	if len(tf.dockers[plain]) != 0 || string(tf.tools[plain]) != "mise" {
		t.Fatalf("a plain fork got docker %q, tools %q", tf.dockers[plain], tf.tools[plain])
	}
	if len(tf.tools[bare]) != 0 {
		t.Fatalf("a fork without tools got %q", tf.tools[bare])
	}
	if string(tf.dockers[full]) != "images" || string(tf.tools[full]) != "mise" {
		t.Fatalf("the fork got docker %q, tools %q", tf.dockers[full], tf.tools[full])
	}
}

// Docker's daemon has to be down for its copy, and a fork can't promise to start a source it
// stopped again after a crash, so it refuses instead. Tools don't have that problem.
func TestForkWithDockerRefusesARunningSource(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	tf.guestBoots = true
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.mu.Lock()
	tf.tools[source] = []byte("mise")
	tf.mu.Unlock()

	if _, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy", ForkOptions{Docker: yes()}); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("err = %v", err)
	}
	if status, _ := tf.Status(f.ctx, source); status != runtime.StatusRunning {
		t.Fatalf("source is %s", status)
	}
	answered, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy", ForkOptions{Tools: yes()})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, answered.ID); err != nil {
		t.Fatal(err)
	}
	tf.mu.Lock()
	defer tf.mu.Unlock()
	if string(tf.tools[VMName(answered.Sandbox)]) != "mise" {
		t.Fatalf("tools = %q", tf.tools[VMName(answered.Sandbox)])
	}
}

func TestForkOfABaseTakesItsDefaultsUnlessToldOtherwise(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	tf.guestBoots = true
	source := VMName(f.sandbox)
	tf.mu.Lock()
	tf.dockers[source], tf.tools[source] = []byte("images"), []byte("mise")
	tf.mu.Unlock()
	view, err := f.manager.SetBase(f.ctx, f.env.ID, f.sandbox.ID, &store.ForkDefaults{Docker: true, Script: true})
	if err != nil {
		t.Fatal(err)
	}
	if view.ForkDefaults == nil || !view.ForkDefaults.Docker || view.ForkDefaults.Tools {
		t.Fatalf("view = %+v", view.ForkDefaults)
	}

	fork := func(name string, opts ForkOptions) View {
		t.Helper()
		answered, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, name, opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Wait(f.ctx, answered.ID); err != nil {
			t.Fatal(err)
		}
		v, err := f.manager.Get(f.ctx, f.env.ID, answered.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	byDefault := fork("by-default", ForkOptions{})
	if byDefault.ForkDefaults != nil {
		t.Fatalf("the fork is a base too: %+v", byDefault.ForkDefaults)
	}
	select {
	case req := <-guestSessions:
		if req.Name != forkScriptSession || req.Harness != "" || req.Command[0] != "bash" {
			t.Fatalf("session = %+v", req)
		}
	default:
		t.Fatal("the base's script default didn't run")
	}
	overridden := fork("overridden", ForkOptions{Docker: no(), Script: no(), Tools: yes()})
	select {
	case req := <-guestSessions:
		t.Fatalf("a fork that said no started %+v", req)
	default:
	}

	tf.mu.Lock()
	defer tf.mu.Unlock()
	if string(tf.dockers[VMName(byDefault.Sandbox)]) != "images" || len(tf.tools[VMName(byDefault.Sandbox)]) != 0 {
		t.Fatalf("by default: docker %q, tools %q", tf.dockers[VMName(byDefault.Sandbox)], tf.tools[VMName(byDefault.Sandbox)])
	}
	if len(tf.dockers[VMName(overridden.Sandbox)]) != 0 || string(tf.tools[VMName(overridden.Sandbox)]) != "mise" {
		t.Fatalf("overridden: docker %q, tools %q", tf.dockers[VMName(overridden.Sandbox)], tf.tools[VMName(overridden.Sandbox)])
	}
}

func TestSetBaseCanBeUndone(t *testing.T) {
	f := newCheckpointFixture(t)
	if _, err := f.manager.SetBase(f.ctx, f.env.ID, f.sandbox.ID, &store.ForkDefaults{Tools: true}); err != nil {
		t.Fatal(err)
	}
	view, err := f.manager.SetBase(f.ctx, f.env.ID, f.sandbox.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.ForkDefaults != nil {
		t.Fatalf("still a base: %+v", view.ForkDefaults)
	}
	if _, err := f.manager.SetBase(f.ctx, f.env.ID, "missing", &store.ForkDefaults{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// A base serves several teammates: a fork takes the one asked for, or none, and a stopped
// computer can change its owner later; a running one can't, since what it runs is the old
// owner's.
func TestForkAndOwnerChangeTakeAnotherTeammate(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	tf.guestBoots = true
	ada, err := f.store.CreateTeammate(f.ctx, store.Teammate{EnvironmentID: f.env.ID, Name: "ada", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	owner := func(id string) string {
		t.Helper()
		sb, err := f.store.Sandbox(f.ctx, f.env.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return sb.TeammateID
	}
	fork := func(name string, teammate *string) string {
		t.Helper()
		answered, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, name, ForkOptions{Teammate: teammate})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Wait(f.ctx, answered.ID); err != nil {
			t.Fatal(err)
		}
		return answered.ID
	}
	adas := fork("adas", &ada.ID)
	if got := owner(adas); got != ada.ID {
		t.Fatalf("the fork's teammate = %q", got)
	}
	none := ""
	if got := owner(fork("nobodys", &none)); got != "" {
		t.Fatalf("a fork without a teammate has %q", got)
	}
	if got := owner(fork("same", nil)); got != owner(f.sandbox.ID) {
		t.Fatalf("a plain fork's teammate = %q", got)
	}

	// The fork runs once it is made; its owner changes only once it is stopped.
	sb, _ := f.store.Sandbox(f.ctx, f.env.ID, adas)
	tf.setStatus(VMName(sb), runtime.StatusRunning)
	if _, err := f.manager.SetTeammate(f.ctx, f.env.ID, adas, ""); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("a running computer changed its owner: %v", err)
	}
	tf.setStatus(VMName(sb), runtime.StatusStopped)
	if v, err := f.manager.SetTeammate(f.ctx, f.env.ID, adas, ""); err != nil || v.TeammateID != "" || owner(adas) != "" {
		t.Fatalf("set no teammate: %+v %v", v.Sandbox, err)
	}
	if _, err := f.manager.SetTeammate(f.ctx, f.env.ID, adas, ada.ID); err != nil || owner(adas) != ada.ID {
		t.Fatalf("set the teammate back: %v", err)
	}
}

// syncBuffer is a log destination that several goroutines can write to.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
