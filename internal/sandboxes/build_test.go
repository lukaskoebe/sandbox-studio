package sandboxes

import (
	"context"
	"errors"
	"maps"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func createOwnedBuildSandbox(t *testing.T, f *checkpointFixture) (store.BuildJob, store.Sandbox) {
	t.Helper()
	job, _, err := f.store.CreateBuildJob(f.ctx, store.BuildJob{
		EnvironmentID:   f.env.ID,
		RequestKey:      "request-" + store.NewID(),
		Source:          "name: private-build\n",
		Spec:            `{}`,
		BaseRef:         "ubuntu:latest",
		TargetPlatform:  "linux/amd64",
		ExporterVersion: "test-exporter",
	})
	if err != nil {
		t.Fatalf("CreateBuildJob: %v", err)
	}
	job, err = f.store.ClaimBuildJob(f.ctx)
	if err != nil {
		t.Fatalf("ClaimBuildJob: %v", err)
	}
	job, err = f.store.ResolveBuildJob(f.ctx, f.env.ID, job.ID, "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("a", 64), "linux/amd64")
	if err != nil {
		t.Fatalf("ResolveBuildJob: %v", err)
	}
	sb, err := f.store.ReserveBuildSandbox(f.ctx, f.env.ID, job.ID, resources.Resources{
		CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024,
	})
	if err != nil {
		t.Fatalf("ReserveBuildSandbox: %v", err)
	}
	job, err = f.store.BuildJob(f.ctx, f.env.ID, job.ID)
	if err != nil {
		t.Fatalf("read reserved BuildJob: %v", err)
	}
	return job, sb
}

func privateBuildImageSourceForTest(envID string) runtime.ImageSource {
	return runtime.ImageSource{
		Reference: "127.0.0.1:7880/studio/" + envID + "/base@sha256:" + strings.Repeat("a", 64),
		Username:  envID,
		Password:  "registry-secret-for-tests",
	}
}

func TestPublicSandboxGuardsBuildOwnedSandbox(t *testing.T) {
	f := newCheckpointFixture(t)
	job, sb := createOwnedBuildSandbox(t, f)
	egress := &buildRecordingEgress{}
	f.manager.Egress = egress
	if sb.BuildJobID != job.ID || job.SandboxID != sb.ID {
		t.Fatalf("reservation did not bind job and sandbox: job=%+v sandbox=%+v", job, sb)
	}

	if _, err := f.manager.PublicSandbox(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("PublicSandbox error = %v; want not found", err)
	}
	unlock, err := f.manager.tryMutation(sb.ID)
	if err != nil {
		t.Fatalf("claim builder lifecycle gate: %v", err)
	}
	if _, err := f.manager.Start(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		unlock()
		t.Fatalf("Start while a worker holds the gate = %v; want not found", err)
	}
	unlock()
	views, err := f.manager.List(f.ctx, f.env.ID)
	if err != nil || len(views) != 1 || views[0].ID != f.sandbox.ID {
		t.Fatalf("List = %+v, %v; want only the public sandbox", views, err)
	}

	if _, err := f.manager.Get(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get error = %v; want not found", err)
	}
	if _, err := f.manager.Start(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Start error = %v; want not found", err)
	}
	if _, err := f.manager.Stop(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Stop error = %v; want not found", err)
	}
	if err := f.manager.Delete(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete error = %v; want not found", err)
	}
	if _, err := f.manager.Terminals(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Terminals error = %v; want not found", err)
	}
	if _, err := f.manager.OpenTerminal(f.ctx, f.env.ID, sb.ID, "main", 80, 24); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("OpenTerminal error = %v; want not found", err)
	}
	if err := f.manager.CloseTerminal(f.ctx, f.env.ID, sb.ID, "main"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CloseTerminal error = %v; want not found", err)
	}
	if _, err := f.manager.Ports(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Ports error = %v; want not found", err)
	}
	if _, err := f.manager.Checkpoints(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Checkpoints error = %v; want not found", err)
	}
	if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, sb.ID, "private"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CreateCheckpoint error = %v; want not found", err)
	}
	if err := f.manager.DeleteCheckpoint(f.ctx, f.env.ID, sb.ID, "private"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DeleteCheckpoint error = %v; want not found", err)
	}
	if _, err := f.manager.RestoreCheckpoint(f.ctx, f.env.ID, sb.ID, "private"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RestoreCheckpoint error = %v; want not found", err)
	}
	if _, err := f.manager.DialPreviewTCP(f.ctx, sb.ID, 3000); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DialPreviewTCP error = %v; want not found", err)
	}
	if _, err := f.manager.DialPreviewTCP(f.ctx, f.sandbox.ID, 3000); !errors.Is(err, agentchan.ErrNotConnected) {
		t.Errorf("DialPreviewTCP for a public sandbox = %v; want guest-agent not connected", err)
	}

	if f.runtime.countCall("start:"+VMName(sb)) != 0 || f.runtime.countCall("stop:"+VMName(sb)) != 0 ||
		f.runtime.countCall("remove:"+VMName(sb)) != 0 || f.runtime.countCall("status:"+VMName(sb)) != 0 {
		t.Fatal("a public manager path reached the builder runtime")
	}
	if egress.attachCalls != 0 || egress.detachCalls != 0 {
		t.Fatalf("a public manager path touched builder egress: attach=%d detach=%d", egress.attachCalls, egress.detachCalls)
	}
	if _, err := os.Stat(f.paths.AgentSocket(sb.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a public manager path created the builder listener: stat error = %v", err)
	}
	if _, err := f.store.Sandbox(f.ctx, f.env.ID, sb.ID); err != nil {
		t.Fatalf("public delete removed the worker-owned row: %v", err)
	}
}

func TestReconcileSkipsBuildOwnedSandbox(t *testing.T) {
	f := newCheckpointFixture(t)
	_, sb := createOwnedBuildSandbox(t, f)
	egress := &buildRecordingEgress{}
	f.manager.Egress = egress

	if err := f.manager.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if egress.attachCalls != 1 || egress.detachCalls != 0 {
		t.Fatalf("Reconcile egress calls = attach %d, detach %d; want one public attach", egress.attachCalls, egress.detachCalls)
	}
	if _, err := os.Stat(f.paths.AgentSocket(sb.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Reconcile created builder listener socket: stat error = %v", err)
	}
}

type buildRecordingEgress struct {
	attachCalls int
	detachCalls int
	attachErr   error
}

func (e *buildRecordingEgress) Attach(sb store.Sandbox) (runtime.Egress, error) {
	e.attachCalls++
	if e.attachErr != nil {
		return runtime.Egress{}, e.attachErr
	}
	return fakeEgress{}.Attach(sb)
}

func (e *buildRecordingEgress) Detach(string) { e.detachCalls++ }

type buildRecordingRuntime struct {
	SandboxRuntime
	createName   string
	createSpec   runtime.Spec
	createSocket string
	createLabels map[string]string
	removeCalls  int
	removed      runtime.OwnedVM
	removeErr    error
}

func (r *buildRecordingRuntime) Create(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	r.createName, r.createSpec, r.createSocket = name, spec, socket
	r.createLabels = maps.Clone(labels)
	return r.SandboxRuntime.Create(ctx, name, spec, socket, labels)
}

func (r *buildRecordingRuntime) RemoveOwned(ctx context.Context, owned runtime.OwnedVM) error {
	r.removeCalls++
	r.removed = runtime.OwnedVM{Name: owned.Name, Labels: maps.Clone(owned.Labels)}
	if r.removeErr != nil {
		return r.removeErr
	}
	return r.SandboxRuntime.Remove(ctx, owned.Name)
}

func TestBootAndCleanupBuildSandboxRetainStoreOwnership(t *testing.T) {
	f := newCheckpointFixture(t)
	job, sb := createOwnedBuildSandbox(t, f)
	source := privateBuildImageSourceForTest(f.env.ID)
	recorder := &buildRecordingRuntime{SandboxRuntime: f.runtime}
	f.manager.Runtime = recorder
	egress := &buildRecordingEgress{}
	f.manager.Egress = egress

	if err := f.manager.BootBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID, source); err != nil {
		t.Fatalf("BootBuildSandbox: %v", err)
	}
	wantLabels := map[string]string{
		"studio.sandbox-id":     sb.ID,
		"studio.environment-id": f.env.ID,
		"studio.sandbox-name":   sb.Name,
		"studio.build-job":      job.ID,
	}
	if recorder.createName != VMName(sb) || recorder.createSocket != f.paths.AgentSocket(sb.ID) ||
		!maps.Equal(recorder.createLabels, wantLabels) {
		t.Fatalf("build VM create: name=%q socket=%q labels=%v", recorder.createName, recorder.createSocket, recorder.createLabels)
	}
	if recorder.createSpec.Image == nil || *recorder.createSpec.Image != source || recorder.createSpec.CPUs != 1 || recorder.createSpec.MemoryMiB != 512 || recorder.createSpec.MaxMemoryMiB != 512 ||
		recorder.createSpec.WorkspaceMiB != 1024 || recorder.createSpec.DockerMiB != 1024 || egress.attachCalls != 1 {
		t.Fatalf("build VM resource spec or image source was not forwarded; attaches=%d", egress.attachCalls)
	}

	if err := f.manager.CleanupBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID); err != nil {
		t.Fatalf("CleanupBuildSandbox: %v", err)
	}
	if recorder.removeCalls != 1 || recorder.removed.Name != VMName(sb) || !maps.Equal(recorder.removed.Labels, wantLabels) {
		t.Fatalf("RemoveOwned call = %+v, count=%d", recorder.removed, recorder.removeCalls)
	}
	if egress.detachCalls != 1 {
		t.Fatalf("cleanup detached egress %d times; want one", egress.detachCalls)
	}
	stored, err := f.store.Sandbox(f.ctx, f.env.ID, sb.ID)
	if err != nil || stored.BuildJobID != job.ID {
		t.Fatalf("manager cleanup changed owned sandbox row: %+v, %v", stored, err)
	}
	currentJob, err := f.store.BuildJob(f.ctx, f.env.ID, job.ID)
	if err != nil || currentJob.SandboxID != sb.ID || currentJob.BaseDigest != job.BaseDigest || currentJob.BaseDigest == "" {
		t.Fatalf("manager cleanup changed job ownership: %+v, %v", currentJob, err)
	}
}

func TestBootBuildSandboxVerifiesJobScopeAndRetainsFailedCreate(t *testing.T) {
	f := newCheckpointFixture(t)
	job, sb := createOwnedBuildSandbox(t, f)
	recorder := &buildRecordingRuntime{SandboxRuntime: f.runtime}
	f.manager.Runtime = recorder
	egress := &buildRecordingEgress{}
	f.manager.Egress = egress
	otherEnv, err := f.store.CreateEnvironment(f.ctx, "other-build-env")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		envID string
		jobID string
	}{
		{envID: otherEnv.ID, jobID: job.ID},
		{envID: f.env.ID, jobID: "another-job"},
	} {
		if err := f.manager.BootBuildSandbox(f.ctx, tc.envID, tc.jobID, sb.ID, privateBuildImageSourceForTest(f.env.ID)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("BootBuildSandbox(%q, %q) = %v; want not found", tc.envID, tc.jobID, err)
		}
		if err := f.manager.CleanupBuildSandbox(f.ctx, tc.envID, tc.jobID, sb.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CleanupBuildSandbox(%q, %q) = %v; want not found", tc.envID, tc.jobID, err)
		}
	}
	if recorder.createName != "" || recorder.removeCalls != 0 || egress.attachCalls != 0 {
		t.Fatal("wrong build ownership reached egress or runtime")
	}
	wrongSource := privateBuildImageSourceForTest(otherEnv.ID)
	if err := f.manager.BootBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID, wrongSource); err == nil {
		t.Fatal("image source scoped to another environment was accepted")
	} else if strings.Contains(err.Error(), wrongSource.Password) {
		t.Fatal("environment-scope error leaked registry credentials")
	}
	if recorder.createName != "" || egress.attachCalls != 0 {
		t.Fatal("mismatched image source reached egress or runtime")
	}
	if _, err := os.Stat(f.paths.AgentSocket(sb.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched image source created an agent socket: stat error = %v", err)
	}

	wantErr := errors.New("injected VM create failure")
	f.runtime.fail("create:"+VMName(sb), wantErr)
	if err := f.manager.BootBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID, privateBuildImageSourceForTest(f.env.ID)); !errors.Is(err, wantErr) {
		t.Fatalf("BootBuildSandbox error = %v; want injected create error", err)
	}
	stored, err := f.store.Sandbox(f.ctx, f.env.ID, sb.ID)
	if err != nil || stored.BuildJobID != job.ID {
		t.Fatalf("failed boot deleted or changed its owned row: %+v, %v", stored, err)
	}
	currentJob, err := f.store.BuildJob(f.ctx, f.env.ID, job.ID)
	if err != nil || currentJob.SandboxID != sb.ID {
		t.Fatalf("failed boot changed job ownership: %+v, %v", currentJob, err)
	}
	if recorder.removeCalls != 0 || f.runtime.countCall("remove:"+VMName(sb)) != 0 {
		t.Fatal("failed boot automatically removed the possibly partial VM")
	}
	if egress.detachCalls != 0 {
		t.Fatal("failed boot automatically detached egress before worker cleanup")
	}
}

type blockingBuildRuntime struct {
	*buildRecordingRuntime
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingBuildRuntime) Create(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return r.buildRecordingRuntime.Create(ctx, name, spec, socket, labels)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestBuildBootAndCleanupShareLifecycleGate(t *testing.T) {
	f := newCheckpointFixture(t)
	job, sb := createOwnedBuildSandbox(t, f)
	recorder := &buildRecordingRuntime{SandboxRuntime: f.runtime}
	blocking := &blockingBuildRuntime{buildRecordingRuntime: recorder, entered: make(chan struct{}), release: make(chan struct{})}
	f.manager.Runtime = blocking

	booted := make(chan error, 1)
	go func() {
		booted <- f.manager.BootBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID, privateBuildImageSourceForTest(f.env.ID))
	}()
	<-blocking.entered
	if err := f.manager.CleanupBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("cleanup during boot = %v; want ErrBusy", err)
	}
	close(blocking.release)
	if err := <-booted; err != nil {
		t.Fatalf("BootBuildSandbox: %v", err)
	}
}

type blockingRemoveBuildRuntime struct {
	*buildRecordingRuntime
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingRemoveBuildRuntime) RemoveOwned(ctx context.Context, owned runtime.OwnedVM) error {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return r.buildRecordingRuntime.RemoveOwned(ctx, owned)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestBuildBootWaitsWhileCleanupHoldsLifecycleGate(t *testing.T) {
	f := newCheckpointFixture(t)
	job, sb := createOwnedBuildSandbox(t, f)
	recorder := &buildRecordingRuntime{SandboxRuntime: f.runtime}
	blocking := &blockingRemoveBuildRuntime{buildRecordingRuntime: recorder, entered: make(chan struct{}), release: make(chan struct{})}
	f.manager.Runtime = blocking

	cleaned := make(chan error, 1)
	go func() { cleaned <- f.manager.CleanupBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID) }()
	<-blocking.entered
	if err := f.manager.BootBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID, privateBuildImageSourceForTest(f.env.ID)); !errors.Is(err, ErrBusy) {
		t.Fatalf("boot during cleanup = %v; want ErrBusy", err)
	}
	close(blocking.release)
	if err := <-cleaned; err != nil {
		t.Fatalf("CleanupBuildSandbox: %v", err)
	}
	if recorder.createName != "" {
		t.Fatal("boot reached runtime while cleanup held the lifecycle gate")
	}
}

func TestCleanupBuildSandboxRequiresOwnedRemovalSupport(t *testing.T) {
	f := newCheckpointFixture(t)
	job, sb := createOwnedBuildSandbox(t, f)
	if err := f.manager.CleanupBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID); err == nil {
		t.Fatal("CleanupBuildSandbox succeeded without RemoveOwned support")
	}
	if _, err := f.store.Sandbox(f.ctx, f.env.ID, sb.ID); err != nil {
		t.Fatalf("unsupported cleanup removed worker-owned row: %v", err)
	}
}

func TestCleanupBuildSandboxRetainsOwnershipWhenOwnedRemovalFails(t *testing.T) {
	f := newCheckpointFixture(t)
	job, sb := createOwnedBuildSandbox(t, f)
	removeErr := errors.New("owned VM removal failed")
	recorder := &buildRecordingRuntime{SandboxRuntime: f.runtime, removeErr: removeErr}
	egress := &buildRecordingEgress{}
	f.manager.Runtime, f.manager.Egress = recorder, egress

	if err := f.manager.CleanupBuildSandbox(f.ctx, f.env.ID, job.ID, sb.ID); !errors.Is(err, removeErr) {
		t.Fatalf("CleanupBuildSandbox error = %v; want removal failure", err)
	}
	stored, err := f.store.Sandbox(f.ctx, f.env.ID, sb.ID)
	if err != nil || stored.BuildJobID != job.ID {
		t.Fatalf("failed cleanup changed owned sandbox row: %+v, %v", stored, err)
	}
	currentJob, err := f.store.BuildJob(f.ctx, f.env.ID, job.ID)
	if err != nil || currentJob.SandboxID != sb.ID || !currentJob.CleanupPending {
		t.Fatalf("failed cleanup changed worker ownership: %+v, %v", currentJob, err)
	}
	if egress.detachCalls != 0 {
		t.Fatalf("failed runtime cleanup detached egress %d times; want none", egress.detachCalls)
	}
}
