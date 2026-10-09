package templatebuild

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
)

const testSource = "resources: {cpus: 1, memory: 512MiB, workspace: 1024MiB, docker: 1024MiB}\nsetup: echo build-output\n"

type buildHarness struct {
	w              *Worker
	st             *store.Store
	env            string
	dir            string
	mu             sync.Mutex
	sequence       []string
	runs           int
	prepares       int
	basePrepares   int
	baseReleases   int
	baseEnv        string
	baseReleaseErr error
	bootErr        error
	bootSource     runtime.ImageSource
	configureErr   error
	cleanupErr     error
	resolveErr     error
	exportErr      error
	exportCtxLive  bool
	runHook        func(context.Context, chan<- runtime.RunOutput) (runtime.RunResult, error)
	beforePublish  func()
	pending        atomic.Bool
}

func newBuildHarness(t *testing.T) *buildHarness {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	h := &buildHarness{st: st, env: env.ID, dir: t.TempDir()}
	h.w, err = New(Options{Store: st, Runtime: h, Guests: h, Registry: h, Exporter: h, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	h.w.poll = time.Millisecond
	return h
}

func (h *buildHarness) record(s string) {
	h.mu.Lock()
	h.sequence = append(h.sequence, s)
	h.mu.Unlock()
}
func (h *buildHarness) BaseReference() string  { return "sandbox-studio-base:dev" }
func (h *buildHarness) TargetPlatform() string { return "linux/" + goruntime.GOARCH }
func (h *buildHarness) PendingRun() bool       { return h.pending.Load() }
func (h *buildHarness) PrepareTemplateBaseOwned(ctx context.Context, owner runtime.BaseOwner) (templateimage.Base, error) {
	h.prepares++
	h.record("prepare")
	name, token := "ss-prewarm-"+strings.Repeat("a", 24), strings.Repeat("b", 32)
	if err := owner.Begin(ctx, name, token); err != nil {
		return templateimage.Base{}, err
	}
	jobs, err := h.st.RecoverableBuildJobs(ctx)
	if err != nil || len(jobs) == 0 {
		return templateimage.Base{}, errors.New("prewarm ownership was not recorded")
	}
	if err := owner.Finish(context.Background(), name, token); err != nil {
		return templateimage.Base{}, err
	}
	return fakeBase(h.BaseReference()), nil
}
func fakeBase(reference string) templateimage.Base {
	return templateimage.Base{Reference: reference, OS: "linux", Architecture: strings.TrimPrefix("linux/"+goruntime.GOARCH, "linux/"), Digest: templateimage.Digest([]byte("base")), Layers: []templateimage.BaseLayer{{Descriptor: templateimage.Descriptor{MediaType: templateimage.MediaLayer, Digest: templateimage.Digest([]byte("base-layer")), Size: 20}, DiffID: templateimage.Digest([]byte("base-diff"))}}}
}
func (h *buildHarness) RemovePrewarm(ctx context.Context, name, token string) error {
	h.record("remove-prewarm")
	return h.cleanupErr
}
func (h *buildHarness) Run(ctx context.Context, owner runtime.OwnedVM, cmd runtime.RunCommand, output chan<- runtime.RunOutput) (runtime.RunResult, error) {
	h.runs++
	h.record("command")
	if owner.Labels["studio.build-job"] == "" || cmd.Path == "" {
		return runtime.RunResult{}, errors.New("command missing ownership or path")
	}
	if h.runHook != nil {
		return h.runHook(ctx, output)
	}
	select {
	case output <- runtime.RunOutput{Data: []byte("build-output\n")}:
	default:
	}
	return runtime.RunResult{ExitCodeKnown: true}, nil
}
func (h *buildHarness) BootBuildSandbox(ctx context.Context, env, job, sandbox string, source runtime.ImageSource) error {
	h.record("boot")
	h.bootSource = source
	if h.bootErr != nil {
		return h.bootErr
	}
	sb, err := h.st.Sandbox(ctx, env, sandbox)
	if err != nil {
		return err
	}
	if sb.BuildJobID != job {
		return errors.New("VM created before persistent ownership")
	}
	return nil
}
func (h *buildHarness) WaitReady(context.Context, string, time.Duration) error {
	h.record("ready")
	return nil
}
func (h *buildHarness) ConfigureGuest(context.Context, string, string) error {
	h.record("configure")
	return h.configureErr
}
func (h *buildHarness) CleanupBuildSandbox(context.Context, string, string, string) error {
	h.record("cleanup")
	return h.cleanupErr
}
func (h *buildHarness) StagingDir(context.Context, string) (string, error) { return h.dir, nil }
func (h *buildHarness) Resolve(context.Context, string, string) (templateregistry.Reference, error) {
	return templateregistry.Reference{}, h.resolveErr
}
func (h *buildHarness) PrepareBase(ctx context.Context, env string, base templateimage.Base) (templateregistry.Reference, func() error, error) {
	h.basePrepares++
	h.baseEnv = env
	h.record("prepare-base")
	if err := ctx.Err(); err != nil {
		return templateregistry.Reference{}, nil, err
	}
	if env != h.env || base.Reference != h.BaseReference() {
		return templateregistry.Reference{}, nil, errors.New("prepared base scope does not match the build")
	}
	ref := fakeBaseReference(env)
	release := func() error {
		h.baseReleases++
		h.record("release-base")
		return h.baseReleaseErr
	}
	return ref, release, nil
}
func (h *buildHarness) Export(ctx context.Context, id, dir string, limits ocilayer.Limits) (templateexport.Layer, error) {
	h.record("export")
	h.exportCtxLive = ctx.Err() == nil
	if h.exportErr != nil {
		return templateexport.Layer{}, h.exportErr
	}
	path := filepath.Join(dir, ".oci-layer-test")
	if err := os.WriteFile(path, []byte("layer"), 0600); err != nil {
		return templateexport.Layer{}, err
	}
	return templateexport.Layer{Path: path, Size: 5, UncompressedSize: 20, Digest: templateimage.Digest([]byte("layer")), DiffID: templateimage.Digest([]byte("diff"))}, nil
}

func fakeBaseReference(env string) templateregistry.Reference {
	return templateregistry.Reference{
		Image:    "127.0.0.1:5001/studio/" + env + "/base@" + templateimage.Digest([]byte("base-manifest")),
		Username: env,
		Password: "dummy-base-auth",
	}
}
func (h *buildHarness) Publish(ctx context.Context, in store.Template, image templateimage.Image, layer templateexport.Layer) (store.Template, error) {
	h.record("publish")
	if h.beforePublish != nil {
		h.beforePublish()
	}
	// A cache publication can commit just as cancellation wins. Model that
	// independent transaction; the worker must still CAS the job separately.
	return h.publishRecord(context.Background(), in)
}
func (h *buildHarness) publishRecord(ctx context.Context, in store.Template) (store.Template, error) {
	t, err := h.st.CreateTemplate(ctx, in)
	if err != nil {
		return t, err
	}
	var artifacts []store.TemplateArtifact
	for _, role := range []string{"manifest", "config", "layer"} {
		artifacts = append(artifacts, store.TemplateArtifact{Role: role, Digest: templateimage.Digest([]byte(role)), MediaType: "application/octet-stream", Size: 5})
	}
	if err := h.st.ReadyTemplate(ctx, t.EnvironmentID, t.ID, artifacts); err != nil {
		return t, err
	}
	return h.st.Template(ctx, t.EnvironmentID, t.ID)
}

func (h *buildHarness) submit(t *testing.T, source string) store.BuildJob {
	t.Helper()
	job, _, err := h.w.Submit(context.Background(), h.env, source)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func (h *buildHarness) claim(t *testing.T) store.BuildJob {
	t.Helper()
	job, err := h.st.ClaimBuildJob(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func (h *buildHarness) job(t *testing.T, id string) store.BuildJob {
	t.Helper()
	job, err := h.st.BuildJob(context.Background(), h.env, id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func (h *buildHarness) stageOwned(t *testing.T, source string) (store.BuildJob, store.Sandbox) {
	t.Helper()
	h.submit(t, source)
	job := h.claim(t)
	base := fakeBase(h.BaseReference())
	key, err := templateimage.CacheKey([]byte(job.Spec), base.Digest, "linux/"+goruntime.GOARCH, templateimage.ExporterVersion)
	if err != nil {
		t.Fatal(err)
	}
	job, err = h.st.ResolveBuildJob(context.Background(), h.env, job.ID, key, base.Digest, "linux/"+goruntime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := h.st.ReserveBuildSandbox(context.Background(), h.env, job.ID, resources.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	return h.job(t, job.ID), sb
}

func TestSubmitCanonicalDedupAndIsolation(t *testing.T) {
	h := newBuildHarness(t)
	first := h.submit(t, testSource)
	second, created, err := h.w.Submit(context.Background(), h.env, "# same decoded spec\n"+testSource)
	if err != nil || created || first.ID != second.ID {
		t.Fatalf("dedup: %+v %t %v", second, created, err)
	}
	other, _ := h.st.CreateEnvironment(context.Background(), "other")
	third, created, err := h.w.Submit(context.Background(), other.ID, testSource)
	if err != nil || !created || third.ID == first.ID {
		t.Fatalf("environment isolation: %+v %t %v", third, created, err)
	}
	if _, _, err := h.w.Submit(context.Background(), h.env, "unknown: true"); err == nil {
		t.Fatal("invalid spec accepted")
	}
	if h.prepares != 0 || h.runs != 0 {
		t.Fatal("submit performed runtime work")
	}
}

func TestBuildOrdersConfigurationBeforeSetupAndPublishes(t *testing.T) {
	h := newBuildHarness(t)
	job := h.submit(t, testSource)
	h.w.process(context.Background(), h.claim(t))
	job = h.job(t, job.ID)
	if job.Status != store.BuildReady || job.CleanupPending || job.SandboxID != "" || job.TemplateID == "" {
		t.Fatalf("job %+v", job)
	}
	got := strings.Join(h.sequence, ",")
	if !strings.HasPrefix(got, "prepare,prepare-base,boot,ready,configure,command") || !strings.HasSuffix(got, "export,publish,release-base,cleanup") {
		t.Fatalf("order: %s", got)
	}
	wantSource := runtime.ImageSource{Reference: fakeBaseReference(h.env).Image, Username: h.env, Password: "dummy-base-auth"}
	if h.basePrepares != 1 || h.baseReleases != 1 || h.baseEnv != h.env || h.bootSource != wantSource {
		t.Fatalf("prepared base bridge: prepares=%d releases=%d env=%q source=%+v", h.basePrepares, h.baseReleases, h.baseEnv, h.bootSource)
	}
	text, truncated, err := h.st.BuildLog(context.Background(), h.env, job.ID)
	if err != nil || truncated || !strings.Contains(text, "build-output") || !strings.Contains(text, "Template is ready") {
		t.Fatalf("log: %q %t %v", text, truncated, err)
	}
	if all, err := h.st.AllSandboxes(context.Background()); err != nil || len(all) != 0 {
		t.Fatalf("owned sandbox leaked: %v %v", all, err)
	}
	// A second request is a new job, but uses the immutable cache after base
	// inspection, without allocating another build sandbox or running commands.
	oldRuns := h.runs
	second := h.submit(t, testSource)
	h.w.process(context.Background(), h.claim(t))
	second = h.job(t, second.ID)
	if second.ID == job.ID || second.TemplateID != job.TemplateID || second.Status != store.BuildReady || h.runs != oldRuns || h.basePrepares != 1 || h.baseReleases != 1 {
		t.Fatalf("cache hit: %+v runs %d", second, h.runs)
	}
}

func TestExporterDeadlineWhileBuildContextIsLiveIsNotTotalBuildTimeout(t *testing.T) {
	h := newBuildHarness(t)
	h.exportErr = errors.Join(context.DeadlineExceeded, errors.New("open /tmp/private-export-layer"))
	job := h.submit(t, testSource)
	h.w.process(context.Background(), h.claim(t))

	job = h.job(t, job.ID)
	if !h.exportCtxLive {
		t.Fatal("fake exporter context was already done")
	}
	if job.Status != store.BuildFailed || job.Error != "Template build failed; see the build log" {
		t.Fatalf("nested exporter timeout classification: status=%q error=%q", job.Status, job.Error)
	}
	if strings.Contains(job.Error, "time limit") || strings.Contains(job.Error, "/tmp/private-export-layer") {
		t.Fatalf("failure message exposed a total timeout or runtime detail: %q", job.Error)
	}
	logText, _, err := h.st.BuildLog(context.Background(), h.env, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logText, "Template export failed") || strings.Contains(logText, "/tmp/private-export-layer") {
		t.Fatalf("export failure log was not safely generic: %q", logText)
	}
}

func TestBuildFailureMessageDeadlineAndShutdownPrecedence(t *testing.T) {
	tests := []struct {
		name            string
		parentErr       error
		buildContextErr error
		want            string
	}{
		{
			name:            "total build deadline",
			buildContextErr: context.DeadlineExceeded,
			want:            "Template build exceeded its time limit",
		},
		{
			name:            "studio shutdown takes precedence",
			parentErr:       context.Canceled,
			buildContextErr: context.DeadlineExceeded,
			want:            "Studio stopped during this build; setup was not replayed",
		},
		{
			name: "live build context uses generic failure",
			want: "Template build failed; see the build log",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildFailureMessage(tt.parentErr, tt.buildContextErr); got != tt.want {
				t.Fatalf("buildFailureMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPreparedBaseReleasedWhenBuildSandboxBootFails(t *testing.T) {
	h := newBuildHarness(t)
	h.bootErr = errors.New("native image source rejected")
	h.baseReleaseErr = errors.New("private release detail")
	job := h.submit(t, testSource)
	h.w.process(context.Background(), h.claim(t))
	job = h.job(t, job.ID)
	if job.Status != store.BuildFailed || job.CleanupPending || job.SandboxID != "" || h.basePrepares != 1 || h.baseReleases != 1 {
		t.Fatalf("boot failure ownership/release: job=%+v prepares=%d releases=%d", job, h.basePrepares, h.baseReleases)
	}
	got := strings.Join(h.sequence, ",")
	if !strings.HasSuffix(got, "prepare-base,boot,release-base,cleanup") {
		t.Fatalf("boot failure order: %s", got)
	}
	logText, _, err := h.st.BuildLog(context.Background(), h.env, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logText, "Prepared base registry reference release failed") || strings.Contains(logText, "private release detail") || strings.Contains(logText, "dummy-base-auth") {
		t.Fatalf("release diagnostic exposed details or omitted generic failure: %q", logText)
	}
}

func TestConfigurationFailureDoesNotRunSetupAndRetainsFailedCleanup(t *testing.T) {
	h := newBuildHarness(t)
	h.configureErr, h.cleanupErr = errors.New("guest rejected config"), errors.New("VM still running")
	job := h.submit(t, testSource)
	h.w.process(context.Background(), h.claim(t))
	job = h.job(t, job.ID)
	if h.runs != 0 || job.Status != store.BuildFailed || !job.CleanupPending || job.SandboxID == "" || job.CleanupError == "" {
		t.Fatalf("unsafe failure: %+v runs %d", job, h.runs)
	}
	if _, err := h.st.ClaimBuildJob(context.Background()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("claim bypassed unresolved cleanup: %v", err)
	}
	h.cleanupErr = nil
	blocked, err := h.w.cleanupOutstanding(context.Background())
	if err != nil || blocked || h.job(t, job.ID).CleanupPending {
		t.Fatalf("cleanup retry: %t %v", blocked, err)
	}
}

func TestCancelQuarantinesUntilNativeRunSettles(t *testing.T) {
	h := newBuildHarness(t)
	started := make(chan struct{})
	h.runHook = func(ctx context.Context, output chan<- runtime.RunOutput) (runtime.RunResult, error) {
		close(started)
		<-ctx.Done()
		h.pending.Store(true)
		return runtime.RunResult{CleanupPending: true}, ctx.Err()
	}
	job := h.submit(t, testSource)
	claimed := h.claim(t)
	done := make(chan struct{})
	go func() { h.w.process(context.Background(), claimed); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("setup not started")
	}
	if _, err := h.w.Cancel(context.Background(), h.env, job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not return")
	}
	job = h.job(t, job.ID)
	if job.Status != store.BuildCancelled || !job.CleanupPending {
		t.Fatalf("cancellation lost ownership: %+v", job)
	}
	if h.basePrepares != 1 || h.baseReleases != 1 || h.baseEnv != h.env || h.bootSource.Username != h.env || h.bootSource.Password != "dummy-base-auth" {
		t.Fatalf("cancelled build did not release its prepared base source: prepares=%d releases=%d env=%q source=%+v", h.basePrepares, h.baseReleases, h.baseEnv, h.bootSource)
	}
	if strings.Contains(strings.Join(h.sequence, ","), "cleanup") {
		t.Fatal("removed VM while native receiver was still active")
	}
	h.pending.Store(false)
	if blocked, err := h.w.cleanupOutstanding(context.Background()); err != nil || blocked {
		t.Fatalf("cleanup after native exit: %t %v", blocked, err)
	}
	if got := h.job(t, job.ID); got.Status != store.BuildCancelled || got.CleanupPending {
		t.Fatalf("cancel resurrected: %+v", got)
	}
}

func TestCancellationWinsLateCachePublication(t *testing.T) {
	h := newBuildHarness(t)
	job := h.submit(t, testSource)
	h.beforePublish = func() {
		if _, err := h.w.Cancel(context.Background(), h.env, job.ID); err != nil {
			t.Error(err)
		}
	}
	h.w.process(context.Background(), h.claim(t))
	job = h.job(t, job.ID)
	if job.Status != store.BuildCancelled || job.TemplateID != "" || job.CleanupPending {
		t.Fatalf("late completion resurrected: %+v", job)
	}
	if cached, err := h.st.TemplateByKey(context.Background(), h.env, job.CacheKey); err != nil || cached.State != store.TemplateStateReady {
		t.Fatalf("independent published cache: %+v %v", cached, err)
	}
}

func TestRestartNeverReplaysSetupAndCanRepairCompletedExport(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(fmtBool(repair), func(t *testing.T) {
			h := newBuildHarness(t)
			job, _ := h.stageOwned(t, testSource)
			if repair {
				if err := h.st.AdvanceBuildJob(context.Background(), h.env, job.ID, store.BuildSettingUp, store.BuildExporting); err != nil {
					t.Fatal(err)
				}
				_, err := h.publishRecord(context.Background(), store.Template{EnvironmentID: h.env, CacheKey: job.CacheKey, Spec: job.Spec, BaseRef: job.BaseRef, BaseDigest: job.BaseDigest, Platform: job.Platform, ExporterVersion: job.ExporterVersion})
				if err != nil {
					t.Fatal(err)
				}
			}
			queued := h.submit(t, testSource+"# queued\n")
			// Same source coalesces while active; use a distinct actual setup.
			if queued.ID == job.ID {
				queued = h.submit(t, strings.Replace(testSource, "build-output", "second-output", 1))
			}
			if err := h.w.recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := h.w.cleanupOutstanding(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := h.job(t, job.ID)
			want := store.BuildFailed
			if repair {
				want = store.BuildReady
			}
			if got.Status != want || got.CleanupPending || h.runs != 0 || h.prepares != 0 {
				t.Fatalf("restart: %+v runs %d", got, h.runs)
			}
			if h.job(t, queued.ID).Status != store.BuildQueued {
				t.Fatal("recovery changed queued work")
			}
		})
	}
}
func fmtBool(b bool) string {
	if b {
		return "repair-published-export"
	}
	return "fail-interrupted-setup"
}

func TestBuildLogIsBoundedAndKeepsDraining(t *testing.T) {
	h := newBuildHarness(t)
	job := h.submit(t, testSource)
	l := h.w.collectLog(job)
	for range 256 {
		l.message(strings.Repeat("x", 32<<10))
	}
	l.close()
	text, truncated, err := h.st.BuildLog(context.Background(), h.env, job.ID)
	if err != nil || !truncated || len(text) > logLimit {
		t.Fatalf("bounded log: len=%d truncated=%t err=%v", len(text), truncated, err)
	}
	// The output channel stays open for a quarantined late producer.
	select {
	case l.output <- runtime.RunOutput{Data: []byte("late")}:
	default:
	}
}

func TestWorkerLoopKeepsQueuedWorkBehindPendingCleanup(t *testing.T) {
	h := newBuildHarness(t)
	first := h.submit(t, testSource)
	second := h.submit(t, strings.Replace(testSource, "build-output", "second-output", 1))
	started := make(chan struct{})
	var calls atomic.Int32
	h.runHook = func(ctx context.Context, out chan<- runtime.RunOutput) (runtime.RunResult, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			h.pending.Store(true)
			return runtime.RunResult{CleanupPending: true}, ctx.Err()
		}
		return runtime.RunResult{ExitCodeKnown: true}, nil
	}
	// IDs break timestamp ties; do not assume the first submit wins the claim.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop")
		}
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no job started")
	}
	active, queued := h.job(t, first.ID), h.job(t, second.ID)
	if active.Status == store.BuildQueued {
		active, queued = queued, active
	}
	if _, err := h.w.Cancel(context.Background(), h.env, active.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !h.pending.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !h.pending.Load() {
		t.Fatal("runtime never entered pending cleanup")
	}
	// Give the worker several queue polls while the native slot stays held.
	time.Sleep(20 * time.Millisecond)
	if got := h.job(t, queued.ID); got.Status != store.BuildQueued {
		t.Fatalf("next job escaped quarantine: %+v", got)
	}
	h.pending.Store(false)
	h.w.signal()
	for time.Now().Before(deadline) {
		got := h.job(t, queued.ID)
		if got.Status == store.BuildReady && !got.CleanupPending {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("next job did not finish after cleanup: %+v", h.job(t, queued.ID))
}

func TestSecondWorkerCannotRecoverLiveBuild(t *testing.T) {
	h := newBuildHarness(t)
	started := make(chan struct{})
	h.runHook = func(ctx context.Context, output chan<- runtime.RunOutput) (runtime.RunResult, error) {
		close(started)
		<-ctx.Done()
		return runtime.RunResult{}, ctx.Err()
	}
	job := h.submit(t, testSource)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop")
		}
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first worker did not start")
	}
	second, err := New(h.w.Options)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Run(context.Background()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("overlapping worker: %v", err)
	}
	if got := h.job(t, job.ID); got.Status != store.BuildSettingUp || !got.CleanupPending {
		t.Fatalf("live build was changed by recovery: %+v", got)
	}
}
