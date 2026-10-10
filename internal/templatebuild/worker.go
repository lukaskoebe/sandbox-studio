// Package templatebuild runs durable, serialized template builds. Jobs own their
// temporary VMs until absence is verified, including after failure or cancellation.
package templatebuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

const (
	cleanupTimeout = 20 * time.Second
	buildTimeout   = 2 * time.Hour
	logLimit       = 1 << 20
)

type Runner interface {
	BaseReference() string
	TargetPlatform() string
	PrepareTemplateBaseOwned(context.Context, runtime.BaseOwner) (templateimage.Base, error)
	RemovePrewarm(context.Context, string, string) error
	Run(context.Context, runtime.OwnedVM, runtime.RunCommand, chan<- runtime.RunOutput) (runtime.RunResult, error)
	PendingRun() bool
}

type Guests interface {
	BootBuildSandbox(context.Context, string, string, string, runtime.ImageSource) error
	CleanupBuildSandbox(context.Context, string, string, string) error
	WaitReady(context.Context, string, time.Duration) error
	ConfigureGuest(context.Context, string, string) error
}

type Registry interface {
	StagingDir(context.Context, string) (string, error)
	Resolve(context.Context, string, string) (templateregistry.Reference, error)
	PrepareBase(context.Context, string, templateimage.Base) (templateregistry.Reference, func() error, error)
	Publish(context.Context, store.Template, templateimage.Image, templateexport.Layer) (store.Template, error)
}

type Options struct {
	Store        *store.Store
	Runtime      Runner
	Guests       Guests
	Registry     Registry
	Bus          *events.Bus
	Log          *slog.Logger
	ExportLimits ocilayer.Limits
}

type Worker struct {
	Options
	wake         chan struct{}
	running      atomic.Bool
	mu           sync.Mutex
	activeID     string
	activeCancel context.CancelFunc
	poll         time.Duration
}

func New(opts Options) (*Worker, error) {
	if opts.Store == nil || opts.Runtime == nil || opts.Guests == nil || opts.Registry == nil {
		return nil, errors.New("template builder requires store, runtime, guests and registry")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Worker{Options: opts, wake: make(chan struct{}, 1), poll: time.Second}, nil
}

// Submit only validates and persists. No VM, registry pull, or setup happens on
// the request goroutine. The request key is preliminary; actual image identity
// is bound after SDK inspection by the worker.
func (w *Worker) Submit(ctx context.Context, envID, source string) (store.BuildJob, bool, error) {
	spec, err := templatespec.ParseYAML([]byte(source))
	if err != nil {
		return store.BuildJob{}, false, err
	}
	canonical, err := templatespec.CanonicalJSON(spec)
	if err != nil {
		return store.BuildJob{}, false, err
	}
	base, platform := w.Runtime.BaseReference(), w.Runtime.TargetPlatform()
	job, created, err := w.Store.CreateBuildJob(ctx, store.BuildJob{
		EnvironmentID: envID, RequestKey: requestKey(canonical, base, platform),
		Source: source, Spec: string(canonical), BaseRef: base, TargetPlatform: platform,
		ExporterVersion: templateimage.ExporterVersion,
	})
	if err == nil {
		w.changed(job)
		w.signal()
	}
	return job, created, err
}

func requestKey(spec []byte, base, platform string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("sandbox-studio-template-build-request-v1\x00"))
	for _, part := range [][]byte{spec, []byte(base), []byte(platform), []byte(templateimage.ExporterVersion)} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(part)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Job returns one build job of an environment.
func (w *Worker) Job(ctx context.Context, envID, id string) (store.BuildJob, error) {
	return w.Store.BuildJob(ctx, envID, id)
}

// Cancel commits cancellation before signaling the running operation. A late
// publication may leave a reusable ready cache entry, but cannot resurrect a job.
func (w *Worker) Cancel(ctx context.Context, envID, id string) (store.BuildJob, error) {
	job, changed, err := w.Store.CancelBuildJob(ctx, envID, id)
	if err != nil {
		return job, err
	}
	if changed {
		w.mu.Lock()
		if w.activeID == id && w.activeCancel != nil {
			w.activeCancel()
		}
		w.mu.Unlock()
		w.changed(job)
	}
	w.signal()
	return job, nil
}

func (w *Worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *Worker) changed(job store.BuildJob) {
	if w.Bus != nil {
		w.Bus.Publish(events.Event{Topic: events.TopicBuilds, EnvironmentID: job.EnvironmentID, ID: job.ID})
	}
}

// Run must start after registry reconciliation and before accepting build work.
// Only queued work resumes. An interrupted setup is never replayed. Run returns
// after its current operation's bounded cleanup attempt; unresolved ownership is
// retained for recovery and blocks future VM allocation.
func (w *Worker) Run(ctx context.Context) error {
	if !w.running.CompareAndSwap(false, true) {
		return errors.New("template build worker already running")
	}
	defer w.running.Store(false)
	release, err := w.Store.AcquireBuildWorker(ctx)
	if err != nil {
		return err
	}
	defer w.releaseWhenIdle(release)
	if err := w.recover(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for ctx.Err() == nil {
		blocked, err := w.cleanupOutstanding(ctx)
		if err != nil {
			return err
		}
		if !blocked && !w.Runtime.PendingRun() {
			job, err := w.Store.ClaimBuildJob(ctx)
			if err == nil {
				if err := w.process(ctx, job); err != nil {
					return err
				}
				continue
			}
			if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrConflict) {
				return err
			}
		}
		select {
		case <-ctx.Done():
		case <-w.wake:
		case <-ticker.C:
		}
	}
	return nil
}

// Startup recovery must never mistake another live worker for a crashed one.
// Keep the catalog's process lock while a native command is quarantined, even
// after Run returns on shutdown. A permanently lost SDK handle requires process
// restart; the OS releases the lock then, without any stale-age heuristic.
func (w *Worker) releaseWhenIdle(release func() error) {
	finish := func() {
		if err := release(); err != nil {
			w.Log.Error("releasing template build worker lock", "err", err)
		}
	}
	if !w.Runtime.PendingRun() {
		finish()
		return
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !w.Runtime.PendingRun() {
				finish()
				return
			}
		}
	}()
}

func (w *Worker) recover(ctx context.Context) error {
	jobs, err := w.Store.RecoverableBuildJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Status == store.BuildQueued || terminal(job.Status) {
			continue
		}
		repaired := false
		if job.Status == store.BuildExporting && job.CacheKey != "" {
			if t, err := w.Store.TemplateByKey(ctx, job.EnvironmentID, job.CacheKey); err == nil && t.State == store.TemplateStateReady {
				if _, err := w.Registry.Resolve(ctx, job.EnvironmentID, t.ID); err == nil {
					err = w.Store.CompleteBuildJob(ctx, job.EnvironmentID, job.ID, store.BuildExporting, t.ID)
					if err != nil && !errors.Is(err, store.ErrConflict) {
						return err
					}
					repaired = err == nil
				}
			}
		}
		if !repaired {
			if err := w.Store.FailBuildJob(ctx, job.EnvironmentID, job.ID, "Studio restarted during this build; setup was not replayed"); err != nil && !errors.Is(err, store.ErrConflict) {
				return err
			}
		}
		w.changed(job)
	}
	return nil
}

func terminal(status string) bool {
	return status == store.BuildReady || status == store.BuildFailed || status == store.BuildCancelled
}

// cleanupOutstanding does not retry active setup. It only retires the resources
// of terminal jobs. An unfinished native exec keeps its slot even after the
// user's cancellation request has returned.
func (w *Worker) cleanupOutstanding(ctx context.Context) (bool, error) {
	jobs, err := w.Store.RecoverableBuildJobs(ctx)
	if err != nil {
		return true, err
	}
	blocked := false
	for _, job := range jobs {
		if !job.CleanupPending {
			continue
		}
		if !terminal(job.Status) || w.Runtime.PendingRun() {
			blocked = true
			continue
		}
		if err := w.cleanup(ctx, job); err != nil {
			blocked = true
		}
	}
	return blocked, nil
}

func (w *Worker) cleanup(ctx context.Context, job store.BuildJob) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	var err error
	switch {
	case w.Runtime.PendingRun():
		err = errors.New("waiting for the runtime command to finish cleanup")
	case job.PrewarmName != "":
		err = w.Runtime.RemovePrewarm(cleanupCtx, job.PrewarmName, job.PrewarmToken)
		if err == nil {
			err = w.Store.FinishBuildPrewarm(cleanupCtx, job.EnvironmentID, job.ID, job.PrewarmName, job.PrewarmToken)
		}
	case job.SandboxID != "":
		err = w.Guests.CleanupBuildSandbox(cleanupCtx, job.EnvironmentID, job.ID, job.SandboxID)
		if err == nil {
			err = w.Store.FinishBuildSandbox(cleanupCtx, job.EnvironmentID, job.ID, job.SandboxID)
		}
	case job.CleanupPending:
		err = errors.New("build cleanup has no recorded VM owner")
	}
	if err != nil {
		// This message is deliberately generic: runtime errors may contain command
		// output or host paths. Detailed state stays with the owned runtime handle.
		_ = w.Store.SetBuildCleanupError(cleanupCtx, job.EnvironmentID, job.ID, "Temporary build VM cleanup is pending; Studio will retry before starting another build")
		w.Log.Warn("template build cleanup pending", "job", job.ID)
	}
	w.changed(job)
	return err
}

func (w *Worker) process(ctx context.Context, job store.BuildJob) error {
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	w.mu.Lock()
	w.activeID, w.activeCancel = job.ID, cancel
	w.mu.Unlock()
	defer func() { cancel(); w.mu.Lock(); w.activeID, w.activeCancel = "", nil; w.mu.Unlock() }()
	w.changed(job)
	logger := w.collectLog(job)
	err := w.build(buildCtx, job, logger)
	buildContextErr := buildCtx.Err()
	cancel()
	logger.close()
	finishCtx, finishCancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer finishCancel()
	if err != nil {
		message := buildFailureMessage(ctx.Err(), buildContextErr)
		if failErr := w.Store.FailBuildJob(finishCtx, job.EnvironmentID, job.ID, message); failErr != nil && !errors.Is(failErr, store.ErrConflict) {
			return fmt.Errorf("recording build %s failure: %w", job.ID, failErr)
		}
	}
	latest, lookupErr := w.Store.BuildJob(finishCtx, job.EnvironmentID, job.ID)
	if lookupErr != nil {
		return fmt.Errorf("reading build %s cleanup ownership: %w", job.ID, lookupErr)
	}
	if latest.CleanupPending {
		_ = w.cleanup(finishCtx, latest)
	}
	w.changed(latest)
	return nil
}

func buildFailureMessage(parentErr, buildContextErr error) string {
	switch {
	case parentErr != nil:
		return "Studio stopped during this build; setup was not replayed"
	case errors.Is(buildContextErr, context.DeadlineExceeded):
		return "Template build exceeded its time limit"
	default:
		return "Template build failed; see the build log"
	}
}

func (w *Worker) build(ctx context.Context, job store.BuildJob, log *buildLog) error {
	// Recheck after registering cancellation so a cancel between claim and
	// process cannot result in an untracked VM or command.
	current, err := w.Store.BuildJob(ctx, job.EnvironmentID, job.ID)
	if err != nil {
		return err
	}
	if current.Status != store.BuildPreparing {
		return store.ErrConflict
	}
	if job.BaseRef != w.Runtime.BaseReference() || job.TargetPlatform != w.Runtime.TargetPlatform() || job.ExporterVersion != templateimage.ExporterVersion {
		log.message("Build configuration changed since submission; submit the spec again\n")
		return store.ErrConflict
	}
	spec, err := templatespec.ParseYAML([]byte(job.Source))
	if err != nil {
		return err
	}
	canonical, err := templatespec.CanonicalJSON(spec)
	if err != nil || !bytes.Equal(canonical, []byte(job.Spec)) {
		return errors.New("stored build spec does not match its canonical form")
	}
	stages, err := Commands(spec)
	if err != nil {
		return err
	}
	log.message("Preparing the Studio base image\n")
	base, err := w.Runtime.PrepareTemplateBaseOwned(ctx, &baseOwner{worker: w, job: job})
	if err != nil {
		log.message("Base preparation failed\n")
		return err
	}
	platform := base.OS + "/" + base.Architecture
	if base.Variant != "" {
		platform += "/" + base.Variant
	}
	if base.Reference != job.BaseRef || base.OS+"/"+base.Architecture != job.TargetPlatform {
		return errors.New("prepared base identity does not match this build")
	}
	key, err := templateimage.CacheKey(canonical, base.Digest, platform, job.ExporterVersion)
	if err != nil {
		return err
	}
	job, err = w.Store.ResolveBuildJob(ctx, job.EnvironmentID, job.ID, key, base.Digest, platform)
	if err != nil {
		return err
	}
	if cached, err := w.Store.TemplateByKey(ctx, job.EnvironmentID, key); err == nil {
		if _, err := w.Registry.Resolve(ctx, job.EnvironmentID, cached.ID); err != nil {
			log.message("Cached template artifacts are unavailable\n")
			return err
		}
		log.message("Using the cached template\n")
		return w.Store.CompleteBuildJob(ctx, job.EnvironmentID, job.ID, store.BuildPreparing, cached.ID)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sb, err := w.Store.ReserveBuildSandbox(ctx, job.EnvironmentID, job.ID, spec.Resources)
	if err != nil {
		return err
	}
	w.changed(job)
	baseReference, releaseBase, err := w.Registry.PrepareBase(ctx, job.EnvironmentID, base)
	if releaseBase != nil {
		defer func() {
			if err := releaseBase(); err != nil {
				log.message("Prepared base registry reference release failed\n")
			}
		}()
	}
	if err != nil {
		log.message("Prepared base registry reference failed\n")
		return err
	}
	if releaseBase == nil {
		return errors.New("registry returned no prepared base release function")
	}
	imageSource := runtime.ImageSource{
		Reference: baseReference.Image,
		Username:  baseReference.Username,
		Password:  baseReference.Password,
	}
	log.message("Booting the temporary build sandbox\n")
	if err := w.Guests.BootBuildSandbox(ctx, job.EnvironmentID, job.ID, sb.ID, imageSource); err != nil {
		log.message("Build sandbox boot failed\n")
		return err
	}
	if err := w.Guests.WaitReady(ctx, sb.ID, 60*time.Second); err != nil {
		log.message("Build sandbox agent did not become ready\n")
		return err
	}
	if err := w.Guests.ConfigureGuest(ctx, job.EnvironmentID, sb.ID); err != nil {
		log.message("Guest did not acknowledge its CA and placeholder configuration\n")
		return err
	}
	owned := runtime.OwnedVM{Name: sandboxes.VMName(sb), Labels: map[string]string{
		"studio.sandbox-id": sb.ID, "studio.environment-id": sb.EnvironmentID,
		"studio.sandbox-name": sb.Name, "studio.build-job": job.ID,
	}}
	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.message("Running " + stage.Name + "\n")
		result, err := w.Runtime.Run(ctx, owned, stage.Command, log.output)
		if result.OutputDropped {
			log.truncated.Store(true)
		}
		if err != nil || result.CleanupPending {
			log.message(stage.Name + " was interrupted or failed\n")
			return errors.Join(err, errors.New("command did not complete cleanly"))
		}
		if !result.ExitCodeKnown || result.ExitCode != 0 {
			log.message(fmt.Sprintf("%s exited with status %d (known: %t)\n", stage.Name, result.ExitCode, result.ExitCodeKnown))
			return errors.New("setup command failed")
		}
	}
	if err := w.Store.AdvanceBuildJob(ctx, job.EnvironmentID, job.ID, store.BuildSettingUp, store.BuildExporting); err != nil {
		return err
	}
	w.changed(job)
	log.message("Exporting the template root layer\n")
	dir, err := w.Registry.StagingDir(ctx, job.EnvironmentID)
	if err != nil {
		return err
	}
	layer, err := w.export(ctx, owned, dir, log)
	if err != nil {
		log.message("Template export failed\n")
		return err
	}
	// Only an artifact returned by the trusted receiver is ours to remove.
	defer os.Remove(layer.Path)
	image, err := templateimage.Compose(base, layer)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	t, err := w.Registry.Publish(ctx, store.Template{
		EnvironmentID: job.EnvironmentID, CacheKey: key, Spec: job.Spec, BaseRef: job.BaseRef,
		BaseDigest: base.Digest, Platform: platform, ExporterVersion: job.ExporterVersion,
	}, image, layer)
	if err != nil {
		log.message("Template publication failed\n")
		return err
	}
	if err := w.Store.CompleteBuildJob(ctx, job.EnvironmentID, job.ID, store.BuildExporting, t.ID); err != nil {
		return err
	}
	log.message("Template is ready\n")
	return nil
}

type baseOwner struct {
	worker *Worker
	job    store.BuildJob
}

func (o *baseOwner) Begin(ctx context.Context, name, token string) error {
	return o.worker.Store.BeginBuildPrewarm(ctx, o.job.EnvironmentID, o.job.ID, name, token)
}
func (o *baseOwner) Finish(ctx context.Context, name, token string) error {
	return o.worker.Store.FinishBuildPrewarm(ctx, o.job.EnvironmentID, o.job.ID, name, token)
}

// buildLog drains independently from the native exec receiver. Its channel is
// never closed: a quarantined native call may deliver late output, which is
// dropped nonblockingly after the collector has stopped.
type buildLog struct {
	output    chan runtime.RunOutput
	stop      chan struct{}
	done      chan struct{}
	truncated atomic.Bool
}

func (l *buildLog) message(s string) {
	select {
	case l.output <- runtime.RunOutput{Data: []byte(s)}:
	default:
		l.truncated.Store(true)
	}
}
func (l *buildLog) close() { close(l.stop); <-l.done }

func (w *Worker) collectLog(job store.BuildJob) *buildLog {
	l := &buildLog{output: make(chan runtime.RunOutput, 64), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(l.done)
		var buf bytes.Buffer
		dirty := false
		appendOutput := func(out runtime.RunOutput) {
			n := min(len(out.Data), logLimit-buf.Len())
			buf.Write(out.Data[:n])
			dirty = true
			if n < len(out.Data) {
				l.truncated.Store(true)
			}
		}
		flush := func() {
			if !dirty && !l.truncated.Load() {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := w.Store.PutBuildLog(ctx, job.EnvironmentID, job.ID, buf.String(), l.truncated.Load()); err == nil {
				dirty = false
				w.changed(job)
			}
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case out := <-l.output:
				appendOutput(out)
			case <-ticker.C:
				flush()
			case <-l.stop:
				// Bounded even when a quarantined producer continues sending.
				for range cap(l.output) {
					select {
					case out := <-l.output:
						appendOutput(out)
					default:
					}
				}
				flush()
				return
			}
		}
	}()
	return l
}
