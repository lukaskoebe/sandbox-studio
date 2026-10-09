package sandboxes

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestCheckpointCreateAndRestoreAdoptsBeforeRemovingSource(t *testing.T) {
	f := newCheckpointFixture(t)
	sourceName := VMName(f.sandbox)
	f.runtime.setStatus(sourceName, runtime.StatusStopped)

	checkpoint, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "  stable state  ")
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Name != "stable state" || checkpoint.State != store.CheckpointStateReady || checkpoint.Generation != f.sandbox.Generation {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}

	generationAtSourceRemoval := 0
	f.runtime.onRemove = func(name string) {
		if name != sourceName {
			return
		}
		current, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
		if err != nil {
			t.Errorf("read generation at source removal: %v", err)
			return
		}
		generationAtSourceRemoval = current.Generation
	}

	view, err := f.manager.RestoreCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Generation != f.sandbox.Generation+1 || view.Status != runtime.StatusStopped {
		t.Fatalf("restored view = %+v", view)
	}
	if generationAtSourceRemoval != view.Generation {
		t.Fatalf("source removed at generation %d; want adopted generation %d", generationAtSourceRemoval, view.Generation)
	}
	if f.runtime.hasVM(sourceName) {
		t.Fatal("source VM remains after successful restore cleanup")
	}
	if !f.runtime.hasVM(VMName(view.Sandbox)) {
		t.Fatal("adopted candidate VM is missing")
	}
	remaining, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(remaining) != 1 || remaining[0].ID != checkpoint.ID || remaining[0].State != store.CheckpointStateReady {
		t.Fatalf("checkpoints after restore = %+v, %v", remaining, err)
	}
}

func TestRestoreFailurePreservesSourceAndPendingCleanupBlocksMutations(t *testing.T) {
	f := newCheckpointFixture(t)
	sourceName := VMName(f.sandbox)
	f.runtime.setStatus(sourceName, runtime.StatusStopped)
	checkpoint := f.createReadyCheckpoint(t, "restore-failure")
	candidateName := vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)
	f.runtime.fail("restore-checkpoint", errors.New("restore failed after candidate creation"))
	f.runtime.fail("remove:"+candidateName, errors.New("candidate removal failed"))

	if _, err := f.manager.RestoreCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID); err == nil {
		t.Fatal("RestoreCheckpoint succeeded despite injected failures")
	}
	current, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.sandbox.Generation || !f.runtime.hasVM(sourceName) {
		t.Fatalf("failed restore changed source: generation=%d sourcePresent=%v", current.Generation, f.runtime.hasVM(sourceName))
	}
	if pending, err := f.store.Restores(f.ctx); err != nil || len(pending) != 1 {
		t.Fatalf("pending restores = %+v, %v; want one retained operation", pending, err)
	}
	if err := f.manager.DeleteCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete checkpoint in active restore = %v; want store.ErrConflict", err)
	}
	if _, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start with uncertain candidate cleanup = %v; want ErrBusy", err)
	}

	f.runtime.clearFailure("remove:" + candidateName)
	if _, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatalf("Start after recovery succeeds: %v", err)
	}
	if pending, err := f.store.Restores(f.ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending restores after retry = %+v, %v", pending, err)
	}
	if f.runtime.hasVM(candidateName) {
		t.Fatal("orphan candidate remains after recovery")
	}
}

func TestRestoreUnavailablePreservesSourceAndDoesNotBeginOperation(t *testing.T) {
	f := newCheckpointFixture(t)
	sourceName := VMName(f.sandbox)
	f.runtime.setStatus(sourceName, runtime.StatusStopped)
	checkpoint := f.createReadyCheckpoint(t, "restore-disabled")
	f.runtime.setCheckpointRestoreSupported(false)
	candidateName := vmNameAtGeneration(f.sandbox, f.sandbox.Generation+1)

	view, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.CheckpointRestoreSupported {
		t.Fatal("view reports checkpoint restore as supported")
	}
	if _, err := f.manager.RestoreCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID); !errors.Is(err, runtime.ErrCheckpointRestoreUnavailable) {
		t.Fatalf("RestoreCheckpoint = %v; want runtime.ErrCheckpointRestoreUnavailable", err)
	}
	current, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.sandbox.Generation || !f.runtime.hasVM(sourceName) {
		t.Fatalf("unsupported restore changed source: generation=%d sourcePresent=%v", current.Generation, f.runtime.hasVM(sourceName))
	}
	if pending, err := f.store.Restores(f.ctx); err != nil || len(pending) != 0 {
		t.Fatalf("restore operations = %+v, %v; want none", pending, err)
	}
	if calls := f.runtime.countCall("restore-checkpoint:" + candidateName); calls != 0 {
		t.Fatalf("runtime restore called %d times while unsupported", calls)
	}
}

func TestRestoreCheckpointKeepsTheSandboxEgress(t *testing.T) {
	f := newCheckpointFixture(t)
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusStopped)
	checkpoint := f.createReadyCheckpoint(t, "egress")
	if _, err := f.manager.RestoreCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID); err != nil {
		t.Fatal(err)
	}
	if want := (fakeEgress{}).egress(f.sandbox.ID); f.runtime.restoredEgress != want {
		t.Fatalf("restored with egress %+v; want the sandbox's %+v", f.runtime.restoredEgress, want)
	}
}

func TestCheckpointCreationRequiresRunningOrStoppedGuest(t *testing.T) {
	f := newCheckpointFixture(t)
	vmName := VMName(f.sandbox)
	f.runtime.setStatus(vmName, runtime.StatusStopped)
	if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "  \t\n "); !errors.Is(err, ErrCheckpointName) {
		t.Fatalf("control-only checkpoint name = %v; want ErrCheckpointName", err)
	}
	for _, tc := range []struct {
		status runtime.Status
		name   string
	}{
		{status: runtime.StatusPaused, name: "paused"},
		{status: runtime.StatusStarting, name: "starting"},
	} {
		f.runtime.setStatus(vmName, tc.status)
		if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, tc.name); !errors.Is(err, ErrCheckpointState) {
			t.Errorf("capture while %s = %v; want ErrCheckpointState", tc.name, err)
		}
	}
	for _, status := range []runtime.Status{runtime.StatusStopped, runtime.StatusRunning} {
		f.runtime.setStatus(vmName, status)
		if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, string(status)); err != nil {
			t.Fatalf("capture while %s: %v", status, err)
		}
	}
	f.runtime.setStatus(vmName, runtime.StatusDraining)
	if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "draining"); !errors.Is(err, ErrCheckpointState) {
		t.Fatalf("capture while draining = %v; want ErrCheckpointState", err)
	}
}

func TestCheckpointPublicationRetriesAfterCaptureCancellation(t *testing.T) {
	f := newCheckpointFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.runtime.onCreateCheckpoint = cancel

	checkpoint, err := f.manager.CreateCheckpoint(ctx, f.env.ID, f.sandbox.ID, "cancelled-after-capture")
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.State != store.CheckpointStateReady {
		t.Fatalf("checkpoint state after publication retry = %q; want ready", checkpoint.State)
	}
	if calls := f.runtime.countCall("remove-checkpoint:" + checkpoint.ID); calls != 0 {
		t.Fatalf("completed artifact was removed %d times after publication cancellation", calls)
	}
	rows, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(rows) != 1 || rows[0].State != store.CheckpointStateReady {
		t.Fatalf("checkpoint row after publication retry = %+v, %v", rows, err)
	}
}

func TestReconcileRecoversBothSidesOfRestoreCommit(t *testing.T) {
	t.Run("catalog still source removes candidate", func(t *testing.T) {
		f := newCheckpointFixture(t)
		sourceName := VMName(f.sandbox)
		f.runtime.setStatus(sourceName, runtime.StatusStopped)
		checkpoint := f.createReadyCheckpoint(t, "rollback")
		op, err := f.store.BeginRestore(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID)
		if err != nil {
			t.Fatal(err)
		}
		candidateName := vmNameAtGeneration(f.sandbox, op.ToGeneration)
		f.runtime.setStatus(candidateName, runtime.StatusStopped)

		if err := f.manager.Reconcile(f.ctx); err != nil {
			t.Fatal(err)
		}
		if !f.runtime.hasVM(sourceName) || f.runtime.hasVM(candidateName) {
			t.Fatalf("rollback recovery source=%v candidate=%v", f.runtime.hasVM(sourceName), f.runtime.hasVM(candidateName))
		}
		if pending, err := f.store.Restores(f.ctx); err != nil || len(pending) != 0 {
			t.Fatalf("pending restores = %+v, %v", pending, err)
		}
	})

	t.Run("catalog adopted candidate removes source", func(t *testing.T) {
		f := newCheckpointFixture(t)
		sourceName := VMName(f.sandbox)
		f.runtime.setStatus(sourceName, runtime.StatusStopped)
		checkpoint := f.createReadyCheckpoint(t, "adopted")
		op, err := f.store.BeginRestore(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.CommitRestore(f.ctx, op); err != nil {
			t.Fatal(err)
		}
		candidateName := vmNameAtGeneration(f.sandbox, op.ToGeneration)
		f.runtime.setStatus(candidateName, runtime.StatusStopped)

		if err := f.manager.Reconcile(f.ctx); err != nil {
			t.Fatal(err)
		}
		if f.runtime.hasVM(sourceName) || !f.runtime.hasVM(candidateName) {
			t.Fatalf("adopted recovery source=%v candidate=%v", f.runtime.hasVM(sourceName), f.runtime.hasVM(candidateName))
		}
		current, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
		if err != nil || current.Generation != op.ToGeneration {
			t.Fatalf("catalog after recovery = %+v, %v", current, err)
		}
		if pending, err := f.store.Restores(f.ctx); err != nil || len(pending) != 0 {
			t.Fatalf("pending restores = %+v, %v", pending, err)
		}
	})
}

func TestCheckpointEnvironmentIsolationAndInterruptedDelete(t *testing.T) {
	f := newCheckpointFixture(t)
	checkpoint := f.createReadyCheckpoint(t, "scoped")
	otherEnv, err := f.store.CreateEnvironment(f.ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Checkpoints(f.ctx, otherEnv.ID, f.sandbox.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-environment list = %v; want not found", err)
	}
	if err := f.manager.DeleteCheckpoint(f.ctx, otherEnv.ID, f.sandbox.ID, checkpoint.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-environment delete = %v; want not found", err)
	}

	f.runtime.fail("remove-checkpoint:"+checkpoint.ID, errors.New("snapshot removal failed"))
	if err := f.manager.DeleteCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID); err == nil {
		t.Fatal("DeleteCheckpoint succeeded despite injected failure")
	}
	rows, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(rows) != 1 || rows[0].State != store.CheckpointStateDeleting {
		t.Fatalf("interrupted checkpoint delete = %+v, %v", rows, err)
	}
	f.runtime.clearFailure("remove-checkpoint:" + checkpoint.ID)
	if err := f.manager.DeleteCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, checkpoint.ID); err != nil {
		t.Fatalf("retry DeleteCheckpoint: %v", err)
	}
	if rows, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID); err != nil || len(rows) != 0 {
		t.Fatalf("checkpoints after delete retry = %+v, %v", rows, err)
	}
}

func TestDeleteCheckpointRecoversInterruptedCreate(t *testing.T) {
	f := newCheckpointFixture(t)
	cp, err := f.store.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "interrupted-create")
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.setCheckpoint(f.sandbox.ID, cp.ID)
	rows, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(rows) != 1 || rows[0].State != store.CheckpointStateCreating {
		t.Fatalf("interrupted checkpoint create = %+v, %v", rows, err)
	}
	if err := f.manager.DeleteCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, cp.ID); err != nil {
		t.Fatalf("delete interrupted create: %v", err)
	}
	if f.runtime.hasCheckpoint(cp.ID) {
		t.Fatal("interrupted create artifact remains after delete")
	}
}

func TestDeleteSandboxRemovesCheckpointArtifactsBeforeCatalog(t *testing.T) {
	f := newCheckpointFixture(t)
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusStopped)
	checkpoint := f.createReadyCheckpoint(t, "sandbox-delete")
	var checkpointExistedAtSandboxRemoval bool
	f.runtime.onRemove = func(name string) {
		if name != VMName(f.sandbox) {
			return
		}
		checkpointExistedAtSandboxRemoval = f.runtime.hasCheckpoint(checkpoint.ID)
	}

	if err := f.manager.Delete(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if checkpointExistedAtSandboxRemoval {
		t.Fatal("sandbox VM was removed before its checkpoint artifact")
	}
	if _, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sandbox remains in catalog: %v", err)
	}
}

func TestDeleteSandboxRetriesParentAfterRemovingIndexedChild(t *testing.T) {
	f := newCheckpointFixture(t)
	sourceName := VMName(f.sandbox)
	f.runtime.setStatus(sourceName, runtime.StatusStopped)
	if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "second"); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(checkpoints) != 2 {
		t.Fatalf("checkpoints before delete = %+v, %v", checkpoints, err)
	}
	// The first row in store order is made the parent, so the first deletion pass must
	// encounter it before its indexed child regardless of same-second random ID ordering.
	parentID, childID := checkpoints[0].ID, checkpoints[1].ID
	f.runtime.setCheckpointChild(parentID, childID)
	var checkpointsAtSourceRemoval int
	f.runtime.onRemove = func(name string) {
		if name == sourceName {
			checkpointsAtSourceRemoval = f.runtime.checkpointCount()
		}
	}

	if err := f.manager.Delete(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if calls := f.runtime.countCall("remove-checkpoint:" + parentID); calls != 2 {
		t.Fatalf("parent removal attempts = %d; want first failure and retry", calls)
	}
	if calls := f.runtime.countCall("remove-checkpoint:" + childID); calls != 1 {
		t.Fatalf("child removal attempts = %d; want one", calls)
	}
	if checkpointsAtSourceRemoval != 0 {
		t.Fatalf("sandbox source removed with %d checkpoint artifacts remaining", checkpointsAtSourceRemoval)
	}
	if f.runtime.hasVM(sourceName) {
		t.Fatal("sandbox source remains after successful delete")
	}
}

func TestDeleteCheckpointWithChildLeavesItReady(t *testing.T) {
	f := newCheckpointFixture(t)
	parent := f.createReadyCheckpoint(t, "first")
	child := f.createReadyCheckpoint(t, "second")
	f.runtime.setCheckpointChild(parent.ID, child.ID)

	if err := f.manager.DeleteCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, parent.ID); !errors.Is(err, runtime.ErrCheckpointInUse) {
		t.Fatalf("DeleteCheckpoint(parent) = %v; want ErrCheckpointInUse", err)
	}
	got, err := f.store.Checkpoint(f.ctx, f.env.ID, f.sandbox.ID, parent.ID)
	if err != nil || got.State != store.CheckpointStateReady {
		t.Fatalf("parent after refused delete = %+v, %v; want ready", got, err)
	}
}

func TestDeleteSandboxPreservesSourceWhenCheckpointCleanupStalls(t *testing.T) {
	f := newCheckpointFixture(t)
	sourceName := VMName(f.sandbox)
	f.runtime.setStatus(sourceName, runtime.StatusStopped)
	checkpoint := f.createReadyCheckpoint(t, "cleanup-stalls")
	f.runtime.fail("remove-checkpoint:"+checkpoint.ID, errors.New("snapshot is still in use"))

	if err := f.manager.Delete(f.ctx, f.env.ID, f.sandbox.ID); err == nil {
		t.Fatal("Delete succeeded despite checkpoint cleanup failure")
	}
	if _, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatalf("sandbox catalog row was removed before checkpoint cleanup: %v", err)
	}
	if !f.runtime.hasVM(sourceName) {
		t.Fatal("sandbox source was removed before checkpoint cleanup")
	}
	if calls := f.runtime.countCall("remove:" + sourceName); calls != 0 {
		t.Fatalf("source removal calls = %d; want none", calls)
	}
}

func TestSandboxMutationsUseIndependentTryLocks(t *testing.T) {
	f := newCheckpointFixture(t)
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusStopped)
	entered := make(chan struct{})
	release := make(chan struct{})
	f.runtime.onCreateCheckpoint = func() {
		close(entered)
		<-release
	}
	result := make(chan error, 1)
	go func() {
		_, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "locked")
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("checkpoint creation did not reach the runtime")
	}
	rows, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || len(rows) != 1 || rows[0].State != store.CheckpointStateCreating {
		t.Fatalf("list while checkpoint is creating = %+v, %v", rows, err)
	}
	if _, err := f.manager.Stop(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("same-sandbox Stop = %v; want ErrBusy", err)
	}

	other, err := f.store.CreateSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, Name: "other", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.setStatus(VMName(other), runtime.StatusStopped)
	if _, err := f.manager.Stop(f.ctx, f.env.ID, other.ID); err != nil {
		t.Fatalf("other-sandbox Stop while first is locked: %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}
}

func TestConfigurationPushDoesNotBlockLifecycle(t *testing.T) {
	f := newCheckpointFixture(t)
	// Configure holds this lock while it waits on the guest agent, for up to configureTimeout.
	gate := f.manager.configurationGate(f.sandbox.ID)
	gate <- struct{}{}
	defer func() { <-gate }()
	if _, err := f.manager.Stop(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatalf("Stop during a configuration push: %v", err)
	}
}

type checkpointFixture struct {
	ctx     context.Context
	store   *store.Store
	env     store.Environment
	sandbox store.Sandbox
	manager *Manager
	runtime *fakeSandboxRuntime
	paths   paths.Paths
}

func newCheckpointFixture(t *testing.T) *checkpointFixture {
	t.Helper()
	ctx := context.Background()
	dataDir, err := os.MkdirTemp("", "ss-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dataDir) })
	p := paths.Paths{Data: dataDir}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := agentchan.NewHub(log)
	env, err := st.CreateEnvironment(ctx, "checkpoints")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "checkpoint-box", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	rt := newFakeSandboxRuntime()
	rt.setStatus(VMName(sb), runtime.StatusStopped)
	m := &Manager{Store: st, Runtime: rt, Hub: hub, Egress: fakeEgress{}, Paths: p, Log: log}
	t.Cleanup(func() {
		all, _ := st.AllSandboxes(context.Background())
		for _, rec := range all {
			hub.Close(rec.ID)
		}
		st.Close()
	})
	return &checkpointFixture{ctx: ctx, store: st, env: env, sandbox: sb, manager: m, runtime: rt, paths: p}
}

func (f *checkpointFixture) createReadyCheckpoint(t *testing.T, name string) store.Checkpoint {
	t.Helper()
	cp, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	return cp
}

type fakeEgress struct{}

func (e fakeEgress) Attach(sb store.Sandbox) (runtime.Egress, error) { return e.egress(sb.ID), nil }
func (fakeEgress) Detach(string)                                     {}

func (fakeEgress) egress(id string) runtime.Egress {
	return runtime.Egress{Nameserver: "127.0.0.1:53000", Proxy: "127.0.0.1:7879", User: id, PasswordEnv: "STUDIO_GW_" + id}
}

type fakeSandboxRuntime struct {
	mu                 sync.Mutex
	statuses           map[string]runtime.Status
	checkpoints        map[string]bool
	checkpointChildren map[string]string
	failures           map[string]error
	calls              []string
	restoreDisabled    bool
	restoredEgress     runtime.Egress
	onCreateCheckpoint func()
	onRemove           func(string)
}

func newFakeSandboxRuntime() *fakeSandboxRuntime {
	return &fakeSandboxRuntime{
		statuses:           make(map[string]runtime.Status),
		checkpoints:        make(map[string]bool),
		checkpointChildren: make(map[string]string),
		failures:           make(map[string]error),
	}
}

func (f *fakeSandboxRuntime) Create(_ context.Context, name string, _ runtime.Spec, _ string, _ map[string]string) error {
	f.record("create:" + name)
	f.setStatus(name, runtime.StatusRunning)
	return f.failure("create:" + name)
}

func (f *fakeSandboxRuntime) Start(_ context.Context, name string) error {
	f.record("start:" + name)
	if err := f.failure("start:" + name); err != nil {
		return err
	}
	f.setStatus(name, runtime.StatusRunning)
	return nil
}

func (f *fakeSandboxRuntime) Stop(_ context.Context, name string) error {
	f.record("stop:" + name)
	if err := f.failure("stop:" + name); err != nil {
		return err
	}
	f.setStatus(name, runtime.StatusStopped)
	return nil
}

func (f *fakeSandboxRuntime) Remove(_ context.Context, name string) error {
	f.record("remove:" + name)
	if err := f.failure("remove:" + name); err != nil {
		return err
	}
	f.mu.Lock()
	delete(f.statuses, name)
	hook := f.onRemove
	f.mu.Unlock()
	if hook != nil {
		hook(name)
	}
	return nil
}

func (f *fakeSandboxRuntime) Status(_ context.Context, name string) (runtime.Status, error) {
	f.record("status:" + name)
	if err := f.failure("status:" + name); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if status, ok := f.statuses[name]; ok {
		return status, nil
	}
	return runtime.StatusAbsent, nil
}

func (f *fakeSandboxRuntime) Statuses(_ context.Context, _ string) (map[string]runtime.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]runtime.Status, len(f.statuses))
	for name, status := range f.statuses {
		out[name] = status
	}
	return out, nil
}

func (f *fakeSandboxRuntime) CheckpointRestoreSupported() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.restoreDisabled
}

func (f *fakeSandboxRuntime) CreateCheckpoint(_ context.Context, vmName, sandboxID, checkpointID string) error {
	key := sandboxID + "/" + checkpointID
	f.record("create-checkpoint:" + key)
	f.mu.Lock()
	f.checkpoints[key] = true // Model a partial SDK create even when it returns an error.
	hook := f.onCreateCheckpoint
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err := f.failure("create-checkpoint:" + key); err != nil {
		return err
	}
	return f.failure("create-checkpoint")
}

func (f *fakeSandboxRuntime) RemoveCheckpoint(_ context.Context, sandboxID, checkpointID string) error {
	key := sandboxID + "/" + checkpointID
	f.record("remove-checkpoint:" + checkpointID)
	if err := f.failure("remove-checkpoint:" + checkpointID); err != nil {
		return err
	}
	f.mu.Lock()
	if childID, ok := f.checkpointChildren[checkpointID]; ok && f.checkpoints[sandboxID+"/"+childID] {
		f.mu.Unlock()
		return runtime.ErrCheckpointInUse // As the real runtime reports an indexed child.
	}
	delete(f.checkpoints, key)
	f.mu.Unlock()
	return nil
}

func (f *fakeSandboxRuntime) RestoreCheckpoint(_ context.Context, sandboxID, checkpointID, newVMName string, egress runtime.Egress) error {
	f.record("restore-checkpoint:" + newVMName)
	f.mu.Lock()
	f.restoredEgress = egress
	if f.checkpoints[sandboxID+"/"+checkpointID] {
		f.statuses[newVMName] = runtime.StatusStopped
	}
	f.mu.Unlock()
	if err := f.failure("restore-checkpoint"); err != nil {
		return err
	}
	return nil
}

func (f *fakeSandboxRuntime) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeSandboxRuntime) setStatus(name string, status runtime.Status) {
	f.mu.Lock()
	f.statuses[name] = status
	f.mu.Unlock()
}

func (f *fakeSandboxRuntime) fail(step string, err error) {
	f.mu.Lock()
	f.failures[step] = err
	f.mu.Unlock()
}

func (f *fakeSandboxRuntime) clearFailure(step string) {
	f.mu.Lock()
	delete(f.failures, step)
	f.mu.Unlock()
}

func (f *fakeSandboxRuntime) failure(step string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failures[step]
}

func (f *fakeSandboxRuntime) hasVM(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.statuses[name]
	return ok
}

func (f *fakeSandboxRuntime) hasCheckpoint(checkpointID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key := range f.checkpoints {
		if strings.HasSuffix(key, "/"+checkpointID) {
			return true
		}
	}
	return false
}

func (f *fakeSandboxRuntime) setCheckpoint(sandboxID, checkpointID string) {
	f.mu.Lock()
	f.checkpoints[sandboxID+"/"+checkpointID] = true
	f.mu.Unlock()
}

func (f *fakeSandboxRuntime) setCheckpointChild(parentID, childID string) {
	f.mu.Lock()
	f.checkpointChildren[parentID] = childID
	f.mu.Unlock()
}

func (f *fakeSandboxRuntime) setCheckpointRestoreSupported(supported bool) {
	f.mu.Lock()
	f.restoreDisabled = !supported
	f.mu.Unlock()
}

func (f *fakeSandboxRuntime) checkpointCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checkpoints)
}

func (f *fakeSandboxRuntime) countCall(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, got := range f.calls {
		if got == call {
			n++
		}
	}
	return n
}
