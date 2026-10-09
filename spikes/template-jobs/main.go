//go:build linux

// template-jobs is a disposable live qualification for the durable template
// build worker. It owns a private catalog and never operates on user VMs.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	runtimego "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/ca"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatebuild"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

const (
	baseImage           = "sandbox-studio-base:dev"
	dummyPlaceholder    = "studio-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	instancePlaceholder = "studio-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	proofPath           = "home/agent/template-build-proof"
	totalTimeout        = 6 * time.Minute
	workerStopTimeout   = 25 * time.Second
	serverStopTimeout   = 5 * time.Second
	jobPollInterval     = 200 * time.Millisecond
	logFlushTimeout     = 5 * time.Second
	probePasswordPrefix = "SS_TEMPLATE_JOBS_GATEWAY_"
	maxFailureLogBytes  = 8 << 10
)

func main() {
	agent := flag.String("agent", "", "absolute path to a compiled Linux amd64 studio-agent binary")
	scratch := flag.String("scratch", os.TempDir(), "parent directory for private qualification state")
	flag.Parse()
	if *agent == "" || !filepath.IsAbs(*agent) {
		fmt.Fprintln(os.Stderr, "-agent must name an absolute path to a compiled Linux amd64 studio-agent binary")
		os.Exit(2)
	}
	if runtimego.GOARCH != "amd64" {
		fmt.Fprintln(os.Stderr, "template-jobs qualification supports Linux amd64 only")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()
	if err := qualify(ctx, *agent, *scratch); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL template-jobs qualification:", err)
		os.Exit(1)
	}
}

func qualify(ctx context.Context, agentPath, scratch string) (retErr error) {
	started := time.Now()
	scratchPath, err := filepath.Abs(scratch)
	if err != nil {
		return fmt.Errorf("resolve -scratch parent: %w", err)
	}
	info, err := os.Stat(scratchPath)
	if err != nil {
		return fmt.Errorf("inspect -scratch parent: %w", err)
	}
	if !info.IsDir() {
		return errors.New("-scratch must name an existing directory")
	}
	root, err := os.MkdirTemp(scratchPath, "ss-template-jobs-")
	if err != nil {
		return fmt.Errorf("create private qualification root: %w", err)
	}

	var st *store.Store
	var registry *templateregistry.Registry
	var registryServer *http.Server
	var registryListener net.Listener
	var registryServeErr chan error
	var workerProcess *workerRun
	workerStopped := true
	preserveRoot := false
	var privateEnv store.Environment
	var rt *runtime.Runtime
	var initialVMs map[string]runtime.Status
	var hub *agentchan.Hub
	var egress *probeEgress
	var templateInstanceCleanupPending bool

	defer func() {
		if workerProcess != nil {
			stopped, stopErr := workerProcess.stop(workerStopTimeout)
			workerStopped = stopped
			if !stopped {
				preserveRoot = true
				retErr = errors.Join(retErr, stopErr)
			} else if stopErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("stop template worker: %w", stopErr))
			}
		}

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()

		if !workerStopped {
			// The worker may still be inside an SDK/native call. Its catalog and
			// private registry stay open until process exit; the root is retained.
			preserveRoot = true
		} else {
			if templateInstanceCleanupPending {
				preserveRoot = true
				retErr = errors.Join(retErr, errors.New("private template instance cleanup is not confirmed"))
			}
			if rt != nil && rt.PendingRun() {
				preserveRoot = true
				retErr = errors.Join(retErr, errors.New("runtime command cleanup is still pending"))
			}
			if st != nil && privateEnv.ID != "" {
				pending, pendingErr := privateOwnershipPending(cleanupCtx, st, privateEnv.ID)
				if pendingErr != nil {
					preserveRoot = true
					retErr = errors.Join(retErr, fmt.Errorf("check private sandbox and template ownership: %w", pendingErr))
				} else if pending {
					preserveRoot = true
					retErr = errors.Join(retErr, errors.New("private build, prewarm, or template-instance ownership is still pending"))
				}
			}
			if egress != nil {
				if egressErr := egress.assertDetached(); egressErr != nil {
					preserveRoot = true
					retErr = errors.Join(retErr, egressErr)
				}
			}
			if rt != nil && initialVMs != nil {
				after, statusErr := rt.Statuses(cleanupCtx, sandboxes.VMPrefix)
				if statusErr != nil {
					preserveRoot = true
					retErr = errors.Join(retErr, fmt.Errorf("snapshot prefixed VMs after qualification: %w", statusErr))
				} else if lingering := newVMNames(initialVMs, after, false); len(lingering) != 0 {
					preserveRoot = true
					retErr = errors.Join(retErr, fmt.Errorf("new prefixed VMs remain after worker cleanup: %s", strings.Join(lingering, ", ")))
				} else {
					fmt.Println("PASS no new prefixed VM names remain after owned cleanup")
				}
			}
		}

		if registryServer != nil {
			serverCtx, serverCancel := context.WithTimeout(context.Background(), serverStopTimeout)
			if shutdownErr := registryServer.Shutdown(serverCtx); shutdownErr != nil {
				shutdownErr = errors.Join(shutdownErr, registryServer.Close())
				retErr = errors.Join(retErr, fmt.Errorf("stop private registry HTTP server: %w", shutdownErr))
				preserveRoot = true
			}
			serverCancel()
		} else if registryListener != nil {
			if closeErr := registryListener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				retErr = errors.Join(retErr, fmt.Errorf("close private registry listener: %w", closeErr))
				preserveRoot = true
			}
		}
		if registryServeErr != nil {
			select {
			case serveErr := <-registryServeErr:
				if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					retErr = errors.Join(retErr, fmt.Errorf("private registry HTTP server: %w", serveErr))
					preserveRoot = true
				}
			default:
			}
		}

		if workerStopped {
			if registry != nil {
				if closeErr := registry.Close(); closeErr != nil {
					retErr = errors.Join(retErr, fmt.Errorf("close private template registry: %w", closeErr))
					preserveRoot = true
				}
			}
			if st != nil {
				if closeErr := st.Close(); closeErr != nil {
					retErr = errors.Join(retErr, fmt.Errorf("close private store: %w", closeErr))
					preserveRoot = true
				}
			}
		}

		if preserveRoot {
			fmt.Fprintln(os.Stderr, "private qualification state retained for recovery:", root)
			return
		}
		if removeErr := os.RemoveAll(root); removeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove private qualification root: %w", removeErr))
			fmt.Fprintln(os.Stderr, "private qualification state retained for recovery:", root)
			return
		}
		if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
			if statErr == nil {
				retErr = errors.Join(retErr, errors.New("private qualification root still exists after cleanup"))
			}
			fmt.Fprintln(os.Stderr, "private qualification state retained for recovery:", root)
			return
		}
		if retErr == nil {
			fmt.Println("PASS private qualification state removed after owned cleanup")
		}
	}()

	privatePaths := paths.Paths{Data: filepath.Join(root, "state")}
	if err := privatePaths.Ensure(); err != nil {
		return fmt.Errorf("create private state paths: %w", err)
	}
	if err := installAgent(agentPath, filepath.Join(privatePaths.Guest(), "bin", "studio-agent")); err != nil {
		return err
	}
	sealer, err := newProbeSealer(root)
	if err != nil {
		return fmt.Errorf("create private fake sealer: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err = store.Open(ctx, privatePaths.DB())
	if err != nil {
		return fmt.Errorf("open private template catalog: %w", err)
	}
	suffix, err := randomBase32(8)
	if err != nil {
		return fmt.Errorf("generate private environment identity: %w", err)
	}
	privateEnv, err = st.CreateEnvironment(ctx, "template-jobs-probe-"+suffix)
	if err != nil {
		return fmt.Errorf("create fresh private environment: %w", err)
	}

	rt = runtime.New(runtime.Options{Image: baseImage, GuestDir: privatePaths.Guest()})
	if rt.TargetPlatform() != "linux/amd64" {
		return fmt.Errorf("runtime target platform is %q, want linux/amd64", rt.TargetPlatform())
	}
	initialVMs, err = rt.Statuses(ctx, sandboxes.VMPrefix)
	if err != nil {
		return fmt.Errorf("snapshot prefixed VMs before qualification: %w", err)
	}

	registryListener, err = net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("allocate private loopback registry listener: %w", err)
	}
	registryAddr := registryListener.Addr().String()
	registry, err = templateregistry.Open(ctx, st, filepath.Join(root, "registry"), registryAddr, sealer)
	if err != nil {
		return fmt.Errorf("open private template registry: %w", err)
	}
	registryServer = &http.Server{
		Handler:           registry.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	registryServeErr = make(chan error, 1)
	go func() { registryServeErr <- registryServer.Serve(registryListener) }()

	hub = agentchan.NewHub(logger)
	egress = &probeEgress{
		attached: make(map[string]probeCredential), seenEnvs: make(map[string]struct{}),
		seenPasswords: make(map[string]struct{}),
	}
	manager := &sandboxes.Manager{
		Store: st, Runtime: rt, Hub: hub, Egress: egress,
		CA:      &ca.Authority{Store: st, Sealer: sealer},
		Secrets: probeSecrets{env: map[string]string{"TOKEN": dummyPlaceholder}},
		Paths:   privatePaths, Log: logger,
		Templates: registry,
	}
	guests := &probeGuests{Manager: manager}
	worker, err := templatebuild.New(templatebuild.Options{
		Store: st, Runtime: rt, Guests: guests, Exporter: hub, Registry: registry,
		Bus: &events.Bus{}, Log: logger, ExportLimits: ocilayer.Limits{},
	})
	if err != nil {
		return fmt.Errorf("create production template worker: %w", err)
	}
	workerProcess = startWorker(ctx, worker)
	workerStopped = false

	setupMarker := "TEMPLATE_BUILD_SETUP_OK_" + suffix
	setup := setupScript(setupMarker)
	first, created, err := worker.Submit(ctx, privateEnv.ID, buildSource(setup, false))
	if err != nil {
		return fmt.Errorf("submit initial build: %w", err)
	}
	if !created {
		return errors.New("initial build unexpectedly deduplicated")
	}
	first, err = waitJob(ctx, st, privateEnv.ID, first.ID, store.BuildReady, workerProcess)
	if err != nil {
		return fmt.Errorf("wait for initial build: %w", err)
	}
	if err := waitJobLogMarker(ctx, st, privateEnv.ID, first.ID, store.BuildReady, setupMarker, workerProcess); err != nil {
		return fmt.Errorf("wait for initial setup log entry: %w", err)
	}
	firstLog, _, err := st.BuildLog(ctx, privateEnv.ID, first.ID)
	if err != nil {
		return fmt.Errorf("read initial build log: %w", err)
	}
	if first.TemplateID == "" || first.SandboxID != "" || first.CleanupPending || !strings.Contains(firstLog, setupMarker) {
		return errors.New("initial build did not publish, log its unique setup marker, and release ownership")
	}
	if strings.Contains(firstLog, dummyPlaceholder) {
		return errors.New("dummy placeholder appeared in the build log")
	}
	if err := verifyPublishedLayer(ctx, st, registry, privateEnv.ID, first.TemplateID, setupMarker); err != nil {
		return fmt.Errorf("inspect published template layer: %w", err)
	}
	if err := assertNoOwnedRecords(ctx, st, privateEnv.ID); err != nil {
		return fmt.Errorf("initial build retained owner records: %w", err)
	}
	if err := egress.assertDetached(); err != nil {
		return err
	}
	fmt.Println("PASS fresh worker build: setup environment, dummy placeholder, persistent proof, login profile and excluded runtime state verified")

	if err := checkRegistryServer(registryServeErr); err != nil {
		return err
	}
	if err := checkPrivateRegistryHTTP(ctx, registryAddr, registry, privateEnv.ID, first.TemplateID); err != nil {
		return fmt.Errorf("check private registry HTTP handler: %w", err)
	}
	fmt.Println("PASS private loopback registry HTTP handler authenticated the private environment")

	cacheBefore, err := rt.Statuses(ctx, sandboxes.VMPrefix)
	if err != nil {
		return fmt.Errorf("snapshot prefixed VMs before cache hit: %w", err)
	}
	cacheAttachCount := egress.attachCountValue()
	second, created, err := worker.Submit(ctx, privateEnv.ID, buildSource(setup, true))
	if err != nil {
		return fmt.Errorf("submit canonical-equivalent build: %w", err)
	}
	if !created || second.ID == first.ID || second.Spec != first.Spec || second.Source == first.Source {
		return errors.New("canonical-equivalent YAML did not create a new job with the same canonical spec")
	}
	second, err = waitJob(ctx, st, privateEnv.ID, second.ID, store.BuildReady, workerProcess)
	if err != nil {
		return fmt.Errorf("wait for cache-hit build: %w", err)
	}
	if err := waitJobLogMarker(ctx, st, privateEnv.ID, second.ID, store.BuildReady, "Using the cached template", workerProcess); err != nil {
		return fmt.Errorf("wait for cached-template log entry: %w", err)
	}
	secondLog, _, err := st.BuildLog(ctx, privateEnv.ID, second.ID)
	if err != nil {
		return fmt.Errorf("read cache-hit build log: %w", err)
	}
	if second.TemplateID != first.TemplateID || second.SandboxID != "" || second.CleanupPending ||
		!strings.Contains(secondLog, "Using the cached template") ||
		strings.Contains(secondLog, "Booting the temporary build sandbox") ||
		strings.Contains(secondLog, "Running apt") || strings.Contains(secondLog, "Running mise") || strings.Contains(secondLog, "Running setup") {
		return errors.New("canonical-equivalent build did not reuse the ready template without booting a build sandbox")
	}
	cacheAfter, err := rt.Statuses(ctx, sandboxes.VMPrefix)
	if err != nil {
		return fmt.Errorf("snapshot prefixed VMs after cache hit: %w", err)
	}
	if newVMs := newVMNames(cacheBefore, cacheAfter, true); len(newVMs) != 0 {
		return fmt.Errorf("cache hit left new non-prewarm VMs: %s", strings.Join(newVMs, ", "))
	}
	if got := egress.attachCountValue(); got != cacheAttachCount {
		return fmt.Errorf("cache hit attached %d build sandboxes; wanted no new build-sandbox attach", got-cacheAttachCount)
	}
	if err := assertNoOwnedRecords(ctx, st, privateEnv.ID); err != nil {
		return fmt.Errorf("cache-hit build retained owner records: %w", err)
	}
	fmt.Println("PASS canonical-equivalent request created a new job, reused the same TemplateID, and made no build-sandbox attach")

	if err := runFreshTemplateInstanceProbe(ctx, manager, st, rt, registry, egress, privateEnv.ID, first, setupMarker, suffix, &templateInstanceCleanupPending); err != nil {
		return fmt.Errorf("fresh-template instance probe: %w", err)
	}
	if err := assertNoOwnedRecords(ctx, st, privateEnv.ID); err != nil {
		return fmt.Errorf("template instance probe retained private sandbox ownership: %w", err)
	}
	if err := egress.assertDetached(); err != nil {
		return err
	}
	fmt.Println("PASS fresh-template instances: template lineage, resources, configured guest state, fresh data markers, and owned cleanup verified")

	cancelMarker := "CANCEL_STARTED_" + suffix
	cancelJob, created, err := worker.Submit(ctx, privateEnv.ID, buildSource(cancelSetup(cancelMarker), false))
	if err != nil {
		return fmt.Errorf("submit cancellation probe: %w", err)
	}
	if !created {
		return errors.New("cancellation probe unexpectedly deduplicated")
	}
	if err := waitLogMarker(ctx, st, privateEnv.ID, cancelJob.ID, cancelMarker, workerProcess); err != nil {
		return fmt.Errorf("wait for cancellation setup marker: %w", err)
	}
	cancelled, err := worker.Cancel(ctx, privateEnv.ID, cancelJob.ID)
	if err != nil {
		return fmt.Errorf("cancel owned build through worker: %w", err)
	}
	if cancelled.Status != store.BuildCancelled {
		return fmt.Errorf("worker cancellation returned status %q, want %q", cancelled.Status, store.BuildCancelled)
	}
	cancelled, err = waitJob(ctx, st, privateEnv.ID, cancelJob.ID, store.BuildCancelled, workerProcess)
	if err != nil {
		return fmt.Errorf("wait for cancelled build cleanup: %w", err)
	}
	cancelLog, _, err := st.BuildLog(ctx, privateEnv.ID, cancelJob.ID)
	if err != nil {
		return fmt.Errorf("read cancellation probe log: %w", err)
	}
	if cancelled.CleanupPending || cancelled.SandboxID != "" || !strings.Contains(cancelLog, cancelMarker) {
		return errors.New("cancellation did not preserve its marker and finish owned cleanup")
	}
	if err := assertNoOwnedRecords(ctx, st, privateEnv.ID); err != nil {
		return fmt.Errorf("cancelled build retained owner records: %w", err)
	}
	if err := egress.assertDetached(); err != nil {
		return err
	}
	fmt.Println("PASS worker cancellation: marker observed before cancel and owned sandbox cleanup completed")

	if time.Since(started) < 5*time.Minute && ctx.Err() == nil {
		failure, created, err := worker.Submit(ctx, privateEnv.ID, buildSource("set -eu\nexit 7\n", false))
		if err != nil {
			return fmt.Errorf("submit optional failure probe: %w", err)
		}
		if !created {
			return errors.New("failure probe unexpectedly deduplicated")
		}
		failure, err = waitJob(ctx, st, privateEnv.ID, failure.ID, store.BuildFailed, workerProcess)
		if err != nil {
			return fmt.Errorf("wait for failed build cleanup: %w", err)
		}
		if err := waitJobLogMarker(ctx, st, privateEnv.ID, failure.ID, store.BuildFailed, "setup exited with status 7", workerProcess); err != nil {
			return fmt.Errorf("wait for exit-7 log entry: %w", err)
		}
		failureLog, _, err := st.BuildLog(ctx, privateEnv.ID, failure.ID)
		if err != nil {
			return fmt.Errorf("read failure probe log: %w", err)
		}
		if failure.CleanupPending || failure.SandboxID != "" || !strings.Contains(failureLog, "setup exited with status 7") {
			return errors.New("exit-7 probe did not fail with status 7 and finish owned cleanup")
		}
		if err := assertNoOwnedRecords(ctx, st, privateEnv.ID); err != nil {
			return fmt.Errorf("failed build retained owner records: %w", err)
		}
		if err := egress.assertDetached(); err != nil {
			return err
		}
		fmt.Println("PASS exit-7 build: failed status and owned sandbox cleanup verified")
	} else {
		fmt.Println("SKIP exit-7 probe: the five-minute start window elapsed")
	}

	if err := checkRegistryServer(registryServeErr); err != nil {
		return err
	}
	stopped, stopErr := workerProcess.stop(workerStopTimeout)
	workerStopped = stopped
	if !stopped {
		return stopErr
	}
	if stopErr != nil {
		return fmt.Errorf("template worker stopped with an error: %w", stopErr)
	}
	if rt.PendingRun() {
		return errors.New("runtime command remains pending after worker shutdown")
	}
	if err := assertNoOwnedRecords(ctx, st, privateEnv.ID); err != nil {
		return fmt.Errorf("private build or template-instance ownership remains after worker shutdown: %w", err)
	}
	if err := egress.assertDetached(); err != nil {
		return err
	}
	fmt.Println("PASS production worker stopped with no runtime command or owner cleanup pending")
	return nil
}

func runFreshTemplateInstanceProbe(
	ctx context.Context,
	manager *sandboxes.Manager,
	st *store.Store,
	rt *runtime.Runtime,
	registry *templateregistry.Registry,
	egress *probeEgress,
	envID string,
	build store.BuildJob,
	buildMarker, suffix string,
	cleanupPending *bool,
) (retErr error) {
	if build.TemplateID == "" {
		return errors.New("ready build has no template ID")
	}
	template, err := st.Template(ctx, envID, build.TemplateID)
	if err != nil {
		return fmt.Errorf("load private ready template: %w", err)
	}
	if template.State != store.TemplateStateReady || template.EnvironmentID != envID {
		return errors.New("private instance source is not a ready same-environment template")
	}
	if template.Spec != build.Spec {
		return errors.New("published template spec differs from the successful build spec")
	}
	spec, err := templatespec.ParseCanonicalJSON([]byte(template.Spec))
	if err != nil {
		return fmt.Errorf("parse private template resources: %w", err)
	}
	wantResources := resources.Resources{CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 1024, WorkspaceMiB: 1024, DockerMiB: 1024}
	if spec.Resources != wantResources {
		return fmt.Errorf("published template resources are %+v, want %+v", spec.Resources, wantResources)
	}
	if template.Platform != rt.TargetPlatform() {
		return fmt.Errorf("published template platform is %q, want %q", template.Platform, rt.TargetPlatform())
	}
	caHash, err := currentCAHash(ctx, manager, envID)
	if err != nil {
		return fmt.Errorf("read private environment CA for guest assertion: %w", err)
	}

	originalSecrets := manager.Secrets
	manager.Secrets = probeSecrets{env: map[string]string{"TOKEN": instancePlaceholder}}
	defer func() { manager.Secrets = originalSecrets }()

	firstName := "template-instance-one-" + suffix
	secondName := "template-instance-two-" + suffix
	firstOwner := &privateTemplateInstanceOwner{envID: envID, name: firstName, templateID: template.ID, manager: manager, store: st, runtime: rt}
	secondOwner := &privateTemplateInstanceOwner{envID: envID, name: secondName, templateID: template.ID, manager: manager, store: st, runtime: rt}
	owners := []*privateTemplateInstanceOwner{firstOwner, secondOwner}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for i := len(owners) - 1; i >= 0; i-- {
			owner := owners[i]
			if !owner.armed {
				continue
			}
			if cleanupErr := owner.cleanup(cleanupCtx); cleanupErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("deferred cleanup of %s could not be confirmed: %w", owner.name, cleanupErr))
				continue
			}
			owner.armed = false
		}
		*cleanupPending = firstOwner.armed || secondOwner.armed
	}()

	firstOwner.armed = true
	*cleanupPending = true
	firstAttachCount := egress.attachCountValue()
	first, err := createPrivateTemplateInstance(ctx, manager, envID, template.ID, firstName)
	firstOwner.createReturned = true
	firstOwner.createSucceeded = err == nil
	if err != nil {
		return errors.New("create first private template instance failed; provider details suppressed")
	}
	if err := assertTemplateInstanceView(first, envID, firstName, template.ID, spec.Resources); err != nil {
		return fmt.Errorf("first instance catalog fields: %w", err)
	}
	if egress.attachCountValue() != firstAttachCount+1 {
		return errors.New("first template instance did not receive exactly one new egress attachment")
	}
	firstCredential, err := egress.currentCredential(first.ID)
	if err != nil {
		return fmt.Errorf("first template instance egress credential: %w", err)
	}
	if err := assertInstanceStoreRow(ctx, st, firstOwner, first); err != nil {
		return fmt.Errorf("first instance private catalog ownership: %w", err)
	}
	if err := waitAndConfigureTemplateInstance(ctx, manager, envID, first.ID, "first"); err != nil {
		return err
	}
	if err := assertTemplatePinBlocksDelete(ctx, registry, envID, template.ID); err != nil {
		return err
	}
	if err := runTemplateInstanceAssertions(ctx, rt, first, buildMarker, "TEMPLATE_INSTANCE_ONE_"+suffix, caHash); err != nil {
		return fmt.Errorf("first instance guest assertions: %w", err)
	}
	if err := firstOwner.cleanup(ctx); err != nil {
		return fmt.Errorf("delete first template instance before second creation: %w", err)
	}
	firstOwner.armed = false
	*cleanupPending = false
	if err := egress.assertDetached(); err != nil {
		return fmt.Errorf("first template instance egress cleanup: %w", err)
	}
	if err := assertNoOwnedRecords(ctx, st, envID); err != nil {
		return fmt.Errorf("first template instance row remains before second creation: %w", err)
	}

	secondOwner.armed = true
	*cleanupPending = true
	secondAttachCount := egress.attachCountValue()
	second, err := createPrivateTemplateInstance(ctx, manager, envID, template.ID, secondName)
	secondOwner.createReturned = true
	secondOwner.createSucceeded = err == nil
	if err != nil {
		return errors.New("create second private template instance failed; provider details suppressed")
	}
	if err := assertTemplateInstanceView(second, envID, secondName, template.ID, spec.Resources); err != nil {
		return fmt.Errorf("second instance catalog fields: %w", err)
	}
	if second.ID == first.ID {
		return errors.New("sequential template instances reused the same sandbox ID")
	}
	if egress.attachCountValue() != secondAttachCount+1 {
		return errors.New("second template instance did not receive exactly one new egress attachment")
	}
	secondCredential, err := egress.currentCredential(second.ID)
	if err != nil {
		return fmt.Errorf("second template instance egress credential: %w", err)
	}
	if secondCredential.env == firstCredential.env || secondCredential.password == firstCredential.password {
		return errors.New("sequential template instances reused an egress credential")
	}
	if err := assertInstanceStoreRow(ctx, st, secondOwner, second); err != nil {
		return fmt.Errorf("second instance private catalog ownership: %w", err)
	}
	if err := waitAndConfigureTemplateInstance(ctx, manager, envID, second.ID, "second"); err != nil {
		return err
	}
	if err := runTemplateInstanceAssertions(ctx, rt, second, buildMarker, "TEMPLATE_INSTANCE_TWO_"+suffix, caHash); err != nil {
		return fmt.Errorf("second instance guest assertions: %w", err)
	}
	if err := secondOwner.cleanup(ctx); err != nil {
		return fmt.Errorf("delete second template instance: %w", err)
	}
	secondOwner.armed = false
	*cleanupPending = false
	if err := egress.assertDetached(); err != nil {
		return fmt.Errorf("second template instance egress cleanup: %w", err)
	}
	return nil
}

func createPrivateTemplateInstance(ctx context.Context, manager *sandboxes.Manager, envID, templateID, name string) (sandboxes.View, error) {
	createCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return manager.CreateFromTemplate(createCtx, envID, templateID, name)
}

func assertTemplateInstanceView(view sandboxes.View, envID, name, templateID string, want resources.Resources) error {
	if view.ID == "" || view.EnvironmentID != envID || view.Name != name || view.TemplateID != templateID || view.BuildJobID != "" {
		return errors.New("returned view does not match the private environment, name, and template")
	}
	if view.Generation != 1 {
		return fmt.Errorf("new template instance generation is %d, want 1", view.Generation)
	}
	got := resources.Resources{
		CPUs: int64(view.CPUs), MemoryMiB: int64(view.MemoryMiB), MaxMemoryMiB: int64(view.MaxMemoryMiB),
		WorkspaceMiB: int64(view.WorkspaceMiB), DockerMiB: int64(view.DockerMiB),
	}
	if got != want {
		return fmt.Errorf("instance resources are %+v, want template resources %+v", got, want)
	}
	return nil
}

func waitAndConfigureTemplateInstance(ctx context.Context, manager *sandboxes.Manager, envID, sandboxID, label string) error {
	if err := manager.WaitReady(ctx, sandboxID, 45*time.Second); err != nil {
		return fmt.Errorf("wait for %s template instance readiness: %w", label, err)
	}
	if err := manager.ConfigureGuest(ctx, envID, sandboxID); err != nil {
		return fmt.Errorf("explicitly configure %s template instance guest: %w", label, err)
	}
	return nil
}

func currentCAHash(ctx context.Context, manager *sandboxes.Manager, envID string) (string, error) {
	caPEM, err := manager.CA.CertPEM(ctx, envID)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(caPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("private environment CA is not a PEM certificate")
	}
	hash := sha256.Sum256(pem.EncodeToMemory(block))
	return hex.EncodeToString(hash[:]), nil
}

func assertTemplatePinBlocksDelete(ctx context.Context, registry *templateregistry.Registry, envID, templateID string) error {
	if err := registry.Delete(ctx, envID, templateID); !errors.Is(err, store.ErrConflict) {
		if err == nil {
			return errors.New("registry deleted a template while a private instance pinned it")
		}
		return errors.New("registry did not reject template deletion with store.ErrConflict")
	}
	reference, err := registry.Resolve(ctx, envID, templateID)
	if err != nil {
		return errors.New("template pin did not preserve resolvable registry artifacts")
	}
	if reference.Image == "" || reference.Username != envID || reference.Password == "" {
		return errors.New("resolved private template reference is incomplete or outside its environment")
	}
	return nil
}

func runTemplateInstanceAssertions(ctx context.Context, rt *runtime.Runtime, view sandboxes.View, originalProof, marker, caHash string) error {
	command := runtime.RunCommand{
		Path: "/bin/bash",
		Args: []string{
			"--noprofile", "--norc", "-e", "-u", "-o", "pipefail", "-c",
			templateInstanceAssertionScript, "studio-template-instance-probe", marker, originalProof, caHash,
		},
		User: "root", Cwd: "/", Env: map[string]string{"HOME": "/root", "PATH": "/usr/sbin:/usr/bin:/sbin:/bin"},
		Timeout: 20 * time.Second,
	}
	result, runErr := rt.Run(ctx, runtime.OwnedVM{
		Name:   sandboxes.VMName(view.Sandbox),
		Labels: templateInstanceLabels(view.Sandbox, view.EnvironmentID, view.TemplateID),
	}, command, nil)
	pending := rt.PendingRun()
	if runErr != nil {
		return fmt.Errorf("root assertion command returned an SDK error (PendingRun=%t, ExitCodeKnown=%t; SDK details suppressed)", pending, result.ExitCodeKnown)
	}
	if result.CleanupPending || pending {
		return fmt.Errorf("root assertion command cleanup is pending (CleanupPending=%t, PendingRun=%t)", result.CleanupPending, pending)
	}
	if !result.ExitCodeKnown {
		return errors.New("root assertion command returned without a known SDK exit code")
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("root assertion command exited with known SDK status %d", result.ExitCode)
	}
	if result.OutputDropped {
		return errors.New("root assertion command output was dropped by the SDK")
	}
	return nil
}

const templateInstanceAssertionScript = `set -euo pipefail
marker="$1"
original_proof="$2"
expected_ca_hash="$3"
test -n "$marker"
test "$(cat /home/agent/template-build-proof)" = "$original_proof"
test ! -e /workspace/template-instance-only
test ! -e /var/lib/docker/template-instance-only
test "$(cat /etc/sandbox-studio/env)" = 'TOKEN=studio-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
test -r /etc/sandbox-studio/ca.crt
test -r /usr/local/share/ca-certificates/sandbox-studio.crt
test -s /etc/ssl/certs/ca-certificates.crt
file_hash() { sha256sum "$1" | cut -d ' ' -f 1; }
test "$(file_hash /etc/sandbox-studio/ca.crt)" = "$expected_ca_hash"
test "$(file_hash /usr/local/share/ca-certificates/sandbox-studio.crt)" = "$expected_ca_hash"
ca_pem="$(cat /etc/sandbox-studio/ca.crt)"
trusted_bundle="$(cat /etc/ssl/certs/ca-certificates.crt)"
case "$trusted_bundle" in *"$ca_pem"*) ;; *) exit 41 ;; esac
test -r /etc/profile.d/sandbox-studio-env.sh
grep -Fq 'Written by the Sandbox Studio guest agent' /etc/profile.d/sandbox-studio-env.sh
. /etc/profile.d/sandbox-studio-env.sh
test "${TOKEN-}" = 'studio-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
profile_found=
for profile in /home/agent/.profile /home/agent/.bash_profile /home/agent/.bash_login; do
    if [ -r "$profile" ] && grep -Fq '# sandbox-studio managed mise login profile' "$profile" &&
       grep -Fq 'mise activate bash' "$profile" && grep -Fq ". '/etc/profile.d/sandbox-studio-env.sh'" "$profile"; then
        profile_found=1
    fi
done
test "$profile_found" = 1
printf '%s\n' "$marker" > /home/agent/template-build-proof
mkdir -p /workspace /var/lib/docker
printf '%s\n' "$marker" > /workspace/template-instance-only
printf '%s\n' "$marker" > /var/lib/docker/template-instance-only
`

type privateTemplateInstanceOwner struct {
	envID, name, templateID string
	manager                 *sandboxes.Manager
	store                   *store.Store
	runtime                 *runtime.Runtime
	record                  *store.Sandbox
	createReturned          bool
	createSucceeded         bool
	armed                   bool
}

func (owner *privateTemplateInstanceOwner) cleanup(ctx context.Context) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	rows, err := owner.store.Sandboxes(cleanupCtx, owner.envID)
	if err != nil {
		return fmt.Errorf("read private sandbox rows: %w", err)
	}
	record, err := findPrivateTemplateInstance(rows, owner.envID, owner.name, owner.templateID)
	rowExists := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if rowExists {
		if owner.record != nil && owner.record.ID != record.ID {
			return errors.New("private cleanup identity now resolves to a different sandbox row")
		}
		copy := record
		owner.record = &copy
	} else if owner.record != nil {
		record = *owner.record
	} else if owner.createReturned && !owner.createSucceeded {
		// CreateFromTemplate retains a catalog row whenever exact VM cleanup is
		// uncertain. A failed call with no row therefore means it either failed
		// before allocation or already verified its own partial-boot cleanup.
		return nil
	} else {
		return errors.New("no matching private catalog row exists, so the VM owner cannot be confirmed")
	}
	if record.EnvironmentID != owner.envID || record.Name != owner.name || record.TemplateID != owner.templateID || record.BuildJobID != "" {
		return errors.New("private cleanup row does not match the registered environment, name, and template")
	}
	if err := owner.runtime.RemoveOwned(cleanupCtx, runtime.OwnedVM{
		Name: sandboxes.VMName(record), Labels: templateInstanceLabels(record, owner.envID, owner.templateID),
	}); err != nil {
		return errors.New("runtime could not confirm removal of the exact private template VM")
	}
	status, err := owner.runtime.Status(cleanupCtx, sandboxes.VMName(record))
	if err != nil {
		return errors.New("runtime status could not confirm private VM absence")
	}
	if status != runtime.StatusAbsent {
		return fmt.Errorf("private VM remains in runtime status %q", status)
	}
	if rowExists {
		if err := owner.manager.Delete(cleanupCtx, owner.envID, record.ID); err != nil {
			return errors.New("manager.Delete could not remove the verified private template instance")
		}
	}
	if _, err := owner.store.Sandbox(cleanupCtx, owner.envID, record.ID); !errors.Is(err, store.ErrNotFound) {
		if err != nil {
			return fmt.Errorf("verify private sandbox row deletion: %w", err)
		}
		return errors.New("private sandbox row remains after manager.Delete")
	}
	return nil
}

func findPrivateTemplateInstance(rows []store.Sandbox, envID, name, templateID string) (store.Sandbox, error) {
	var match store.Sandbox
	found := false
	for _, row := range rows {
		if row.EnvironmentID != envID || row.Name != name {
			continue
		}
		if row.TemplateID != templateID || row.BuildJobID != "" {
			return store.Sandbox{}, errors.New("private instance name resolves to a row with different template ownership")
		}
		if found {
			return store.Sandbox{}, errors.New("multiple private sandbox rows match the registered instance identity")
		}
		match, found = row, true
	}
	if !found {
		return store.Sandbox{}, store.ErrNotFound
	}
	return match, nil
}

func assertInstanceStoreRow(ctx context.Context, st *store.Store, owner *privateTemplateInstanceOwner, view sandboxes.View) error {
	rows, err := st.Sandboxes(ctx, owner.envID)
	if err != nil {
		return err
	}
	record, err := findPrivateTemplateInstance(rows, owner.envID, owner.name, owner.templateID)
	if err != nil {
		return err
	}
	if record.ID != view.ID || record.Generation != view.Generation {
		return errors.New("returned view does not match the unique private catalog row")
	}
	copy := record
	owner.record = &copy
	return nil
}

func templateInstanceLabels(sb store.Sandbox, envID, templateID string) map[string]string {
	return map[string]string{
		"studio.sandbox-id":     sb.ID,
		"studio.environment-id": envID,
		"studio.sandbox-name":   sb.Name,
		"studio.template-id":    templateID,
	}
}

func installAgent(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect -agent binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("-agent must be an executable regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create private guest binary directory: %w", err)
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open -agent binary: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return fmt.Errorf("create private guest agent copy: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("copy -agent binary into private guest directory: %w", err)
	}
	return nil
}

func setupScript(marker string) string {
	return fmt.Sprintf("set -eu\ntest \"$HOME\" = /home/agent\ntest \"$PWD\" = /home/agent\ntest \"$USER\" = agent\ntest \"$SSL_CERT_FILE\" = /etc/ssl/certs/ca-certificates.crt\ntest -r \"$SSL_CERT_FILE\"\ntest \"$CURL_CA_BUNDLE\" = \"$SSL_CERT_FILE\"\ntest -r /etc/sandbox-studio/ca.crt\ntest \"$TOKEN\" = %q\numask 077\nprintf '%%s\\n' %q > /home/agent/template-build-proof\necho %q\n", dummyPlaceholder, marker, marker)
}

func cancelSetup(marker string) string {
	return fmt.Sprintf("set -eu\necho %q\nsleep 60\n", marker)
}

func buildSource(setup string, reordered bool) string {
	var out strings.Builder
	writeSetup := func() {
		out.WriteString("setup: |\n")
		for _, line := range strings.Split(strings.TrimSuffix(setup, "\n"), "\n") {
			out.WriteString("  ")
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	writeResourcesBlock := func() {
		out.WriteString("resources:\n  cpus: 1\n  memory: 512MiB\n  max_memory: 1024MiB\n  workspace: 1024MiB\n  docker: 1024MiB\n")
	}
	if reordered {
		writeSetup()
		out.WriteString("tools: {}\n")
		out.WriteString("resources: {cpus: 1, memory: 512MiB, max_memory: 1024MiB, workspace: 1024MiB, docker: 1024MiB}\n")
		out.WriteString("apt: []\n")
	} else {
		writeResourcesBlock()
		out.WriteString("apt: []\ntools: {}\n")
		writeSetup()
	}
	return out.String()
}

func waitJob(ctx context.Context, st *store.Store, envID, jobID, want string, worker *workerRun) (store.BuildJob, error) {
	ticker := time.NewTicker(jobPollInterval)
	defer ticker.Stop()
	for {
		if err := worker.alive(); err != nil {
			return store.BuildJob{}, err
		}
		job, err := st.BuildJob(ctx, envID, jobID)
		if err != nil {
			return store.BuildJob{}, err
		}
		if isTerminal(job.Status) && !job.CleanupPending {
			if job.Status != want {
				logText, _, logErr := st.BuildLog(ctx, envID, jobID)
				if logErr != nil {
					return job, fmt.Errorf("job finished with status %q, want %q: %s (read private job log: %w)", job.Status, want, job.Error, logErr)
				}
				logTail := tailBytes(logText, maxFailureLogBytes)
				return job, fmt.Errorf("job finished with status %q, want %q: %s\nlast %d bytes of private job log:\n%s", job.Status, want, job.Error, len(logTail), logTail)
			}
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitLogMarker(ctx context.Context, st *store.Store, envID, jobID, marker string, worker *workerRun) error {
	ticker := time.NewTicker(jobPollInterval)
	defer ticker.Stop()
	for {
		if err := worker.alive(); err != nil {
			return err
		}
		logText, _, err := st.BuildLog(ctx, envID, jobID)
		if err != nil {
			return err
		}
		if strings.Contains(logText, marker) {
			return nil
		}
		job, err := st.BuildJob(ctx, envID, jobID)
		if err != nil {
			return err
		}
		if isTerminal(job.Status) {
			return fmt.Errorf("job reached status %q before logging its setup marker", job.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitJobLogMarker(ctx context.Context, st *store.Store, envID, jobID, want, marker string, worker *workerRun) error {
	logCtx, cancel := context.WithTimeout(ctx, logFlushTimeout)
	defer cancel()
	ticker := time.NewTicker(jobPollInterval)
	defer ticker.Stop()
	for {
		if err := worker.alive(); err != nil {
			return err
		}
		job, err := st.BuildJob(logCtx, envID, jobID)
		if err != nil {
			return err
		}
		if isTerminal(job.Status) && job.Status != want {
			return fmt.Errorf("job reached status %q before logging %q", job.Status, marker)
		}
		logText, _, err := st.BuildLog(logCtx, envID, jobID)
		if err != nil {
			return err
		}
		if job.Status == want && strings.Contains(logText, marker) {
			return nil
		}
		select {
		case <-logCtx.Done():
			return logCtx.Err()
		case <-ticker.C:
		}
	}
}

func isTerminal(status string) bool {
	return status == store.BuildReady || status == store.BuildFailed || status == store.BuildCancelled
}

func tailBytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	return value[len(value)-maxBytes:]
}

func assertNoOwnedRecords(ctx context.Context, st *store.Store, envID string) error {
	jobs, err := st.BuildJobs(ctx, envID, 500)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.CleanupPending || job.SandboxID != "" || job.PrewarmName != "" || job.PrewarmToken != "" {
			return fmt.Errorf("job %s retains sandbox or prewarm ownership", job.ID)
		}
	}
	sandboxes, err := st.Sandboxes(ctx, envID)
	if err != nil {
		return err
	}
	for _, sandbox := range sandboxes {
		if sandbox.BuildJobID != "" {
			return fmt.Errorf("sandbox %s retains build-job ownership", sandbox.ID)
		}
		if sandbox.TemplateID != "" {
			return fmt.Errorf("sandbox %s retains a public template instance pin", sandbox.ID)
		}
	}
	return nil
}

func privateOwnershipPending(ctx context.Context, st *store.Store, envID string) (bool, error) {
	jobs, err := st.BuildJobs(ctx, envID, 500)
	if err != nil {
		return true, err
	}
	for _, job := range jobs {
		if job.CleanupPending || job.SandboxID != "" || job.PrewarmName != "" || job.PrewarmToken != "" {
			return true, nil
		}
	}
	sandboxes, err := st.Sandboxes(ctx, envID)
	if err != nil {
		return true, err
	}
	for _, sandbox := range sandboxes {
		if sandbox.BuildJobID != "" || sandbox.TemplateID != "" {
			return true, nil
		}
	}
	return false, nil
}

func verifyPublishedLayer(ctx context.Context, st *store.Store, registry *templateregistry.Registry, envID, templateID, marker string) error {
	template, err := st.Template(ctx, envID, templateID)
	if err != nil {
		return err
	}
	if template.State != store.TemplateStateReady {
		return fmt.Errorf("template state is %q, want ready", template.State)
	}
	var layerDigest string
	for _, artifact := range template.Artifacts {
		if artifact.Role == "layer" {
			layerDigest = artifact.Digest
			break
		}
	}
	if layerDigest == "" {
		return errors.New("ready template has no layer artifact")
	}
	file, descriptor, err := registry.OpenArtifact(ctx, envID, templateID, "blobs", layerDigest)
	if err != nil {
		return err
	}
	if descriptor.Digest != layerDigest {
		_ = file.Close()
		return errors.New("registry layer descriptor changed while opening artifact")
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("open compressed layer: %w", err)
	}
	reader := tar.NewReader(gz)
	proofFound := false
	profileFound := false
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = gz.Close()
			_ = file.Close()
			return fmt.Errorf("read published layer: %w", nextErr)
		}
		cleanName := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if strings.HasPrefix(cleanName, "/") {
			return closeLayerReaders(gz, file, errors.New("published layer contains an absolute path"))
		}
		for _, excluded := range []string{"workspace", "tmp", "var/tmp", "run", "opt/studio", "etc/sandbox-studio", "etc/ssl/certs", "var/cache/apt/archives", "var/lib/apt/lists"} {
			if cleanName == excluded || strings.HasPrefix(cleanName, excluded+"/") {
				return closeLayerReaders(gz, file, fmt.Errorf("published layer includes excluded runtime path %q", cleanName))
			}
		}
		if cleanName == proofPath {
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				return closeLayerReaders(gz, file, errors.New("published proof path is not a regular file"))
			}
			contents, readErr := io.ReadAll(io.LimitReader(reader, 1024))
			if readErr != nil {
				return closeLayerReaders(gz, file, fmt.Errorf("read published proof: %w", readErr))
			}
			if string(contents) != marker+"\n" {
				return closeLayerReaders(gz, file, errors.New("published proof contents do not match the unique setup marker"))
			}
			proofFound = true
		}
		if cleanName == "home/agent/.profile" || cleanName == "home/agent/.bash_profile" || cleanName == "home/agent/.bash_login" {
			contents, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
			if readErr != nil {
				return closeLayerReaders(gz, file, fmt.Errorf("read managed login profile: %w", readErr))
			}
			if strings.Contains(string(contents), "# sandbox-studio managed mise login profile") && strings.Contains(string(contents), "mise activate bash") {
				profileFound = true
			}
		}
	}
	if closeErr := errors.Join(gz.Close(), file.Close()); closeErr != nil {
		return fmt.Errorf("close published layer readers: %w", closeErr)
	}
	if !proofFound {
		return errors.New("published layer does not contain /home/agent/template-build-proof")
	}
	if !profileFound {
		return errors.New("published layer does not contain the managed mise login profile")
	}
	return nil
}

func closeLayerReaders(gz *gzip.Reader, file *os.File, cause error) error {
	return errors.Join(cause, gz.Close(), file.Close())
}

func checkPrivateRegistryHTTP(ctx context.Context, addr string, registry *templateregistry.Registry, envID, templateID string) error {
	reference, err := registry.Resolve(ctx, envID, templateID)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v2/", nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(reference.Username, reference.Password)
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return fmt.Errorf("read registry response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("registry /v2/ returned HTTP %d, want %d", response.StatusCode, http.StatusOK)
	}
	return nil
}

func checkRegistryServer(serveErr <-chan error) error {
	select {
	case err := <-serveErr:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return errors.New("private registry HTTP server stopped before qualification finished")
		}
		return fmt.Errorf("private registry HTTP server stopped: %w", err)
	default:
		return nil
	}
}

func newVMNames(before, after map[string]runtime.Status, allowPrewarm bool) []string {
	var names []string
	for name := range after {
		if _, existed := before[name]; existed {
			continue
		}
		if allowPrewarm && strings.HasPrefix(name, "ss-prewarm-") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type workerRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func startWorker(parent context.Context, worker *templatebuild.Worker) *workerRun {
	ctx, cancel := context.WithCancel(parent)
	run := &workerRun{cancel: cancel, done: make(chan struct{})}
	go func() {
		run.err = worker.Run(ctx)
		close(run.done)
	}()
	return run
}

func (r *workerRun) alive() error {
	select {
	case <-r.done:
		if r.err != nil {
			return fmt.Errorf("template worker stopped unexpectedly: %w", r.err)
		}
		return errors.New("template worker stopped before the qualification ended")
	default:
		return nil
	}
}

func (r *workerRun) stop(timeout time.Duration) (bool, error) {
	r.cancel()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-r.done:
		return true, normalizeRequestedStopError(r.err)
	case <-timer.C:
		return false, fmt.Errorf("template worker did not stop within %s", timeout)
	}
}

func normalizeRequestedStopError(err error) error {
	if !errors.Is(err, context.Canceled) || !onlyCancellation(err) {
		return err
	}
	return nil
}

func onlyCancellation(err error) bool {
	if err == context.Canceled {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		causes := wrapped.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !onlyCancellation(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return onlyCancellation(wrapped.Unwrap())
	default:
		return false
	}
}

type probeCredential struct {
	env      string
	password string
}

// probeGuests keeps production manager behavior intact while exposing the
// underlying boot error for this private, dummy-credential qualification.
type probeGuests struct {
	*sandboxes.Manager
}

func (g *probeGuests) BootBuildSandbox(ctx context.Context, envID, jobID, sandboxID string, source runtime.ImageSource) error {
	err := g.Manager.BootBuildSandbox(ctx, envID, jobID, sandboxID, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "PRIVATE TEMPLATE PROBE BootBuildSandbox returned: %v\n", err)
	}
	return err
}

type probeEgress struct {
	mu            sync.Mutex
	attached      map[string]probeCredential
	seenEnvs      map[string]struct{}
	seenPasswords map[string]struct{}
	lastErr       error
	attachCount   int
}

func (e *probeEgress) Attach(sb store.Sandbox) (runtime.Egress, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.attached[sb.ID]; exists {
		return runtime.Egress{}, errors.New("probe egress already has this sandbox attached")
	}
	suffix, err := randomBase32(10)
	if err != nil {
		return runtime.Egress{}, fmt.Errorf("generate per-job probe credential: %w", err)
	}
	envName := probePasswordPrefix + strings.ToUpper(suffix)
	if _, exists := os.LookupEnv(envName); exists {
		return runtime.Egress{}, errors.New("per-job probe credential environment name already exists")
	}
	password, err := randomBase32(20)
	if err != nil {
		return runtime.Egress{}, fmt.Errorf("generate per-job probe password: %w", err)
	}
	if _, exists := e.seenEnvs[envName]; exists {
		return runtime.Egress{}, errors.New("probe egress generated a repeated credential environment name")
	}
	if _, exists := e.seenPasswords[password]; exists {
		return runtime.Egress{}, errors.New("probe egress generated a repeated password")
	}
	if err := os.Setenv(envName, password); err != nil {
		return runtime.Egress{}, fmt.Errorf("set host-only per-job probe password: %w", err)
	}
	e.attached[sb.ID] = probeCredential{env: envName, password: password}
	e.seenEnvs[envName] = struct{}{}
	e.seenPasswords[password] = struct{}{}
	e.attachCount++
	return runtime.Egress{
		Nameserver:  "127.0.0.1:9",
		Proxy:       "127.0.0.1:9",
		User:        "template-jobs-probe",
		PasswordEnv: envName,
	}, nil
}

func (e *probeEgress) attachCountValue() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attachCount
}

func (e *probeEgress) currentCredential(sandboxID string) (probeCredential, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	credential, exists := e.attached[sandboxID]
	if !exists || credential.env == "" || credential.password == "" {
		return probeCredential{}, errors.New("no active private credential is recorded for the sandbox")
	}
	current, exists := os.LookupEnv(credential.env)
	if !exists || current != credential.password {
		return probeCredential{}, errors.New("active private credential does not match its recorded sandbox")
	}
	return credential, nil
}

func (e *probeEgress) Detach(sandboxID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	credential, exists := e.attached[sandboxID]
	if !exists {
		return
	}
	current, exists := os.LookupEnv(credential.env)
	if !exists || current != credential.password {
		e.lastErr = errors.Join(e.lastErr, errors.New("per-job probe password changed before owned cleanup"))
		return
	}
	if err := os.Unsetenv(credential.env); err != nil {
		e.lastErr = errors.Join(e.lastErr, fmt.Errorf("unset per-job probe password after owned cleanup: %w", err))
		return
	}
	delete(e.attached, sandboxID)
}

func (e *probeEgress) assertDetached() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastErr != nil {
		return e.lastErr
	}
	if len(e.attached) != 0 {
		return errors.New("per-job probe password remains until its recorded owner is cleaned up")
	}
	return nil
}

type probeSecrets struct{ env map[string]string }

func (s probeSecrets) Env(context.Context, string) (map[string]string, error) {
	copy := make(map[string]string, len(s.env))
	for name, value := range s.env {
		copy[name] = value
	}
	return copy, nil
}

type probeSealer struct{ aead cipher.AEAD }

func newProbeSealer(root string) (*probeSealer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(root, "probe-seal.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &probeSealer{aead: aead}, nil
}

func (s *probeSealer) Seal(plaintext, aad []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil
	}
	sealed := append([]byte(nil), nonce...)
	return s.aead.Seal(sealed, nonce, plaintext, aad)
}

func (s *probeSealer) Unseal(sealed, aad []byte) ([]byte, error) {
	nonceSize := s.aead.NonceSize()
	if len(sealed) < nonceSize+s.aead.Overhead() {
		return nil, errors.New("private fake-sealed value is too short")
	}
	nonce := sealed[:nonceSize]
	return s.aead.Open(nil, nonce, sealed[nonceSize:], aad)
}

func randomBase32(n int) (string, error) {
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)), nil
}
