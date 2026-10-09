// Command roundtrip qualifies guest layer export against the installed Studio dev base.
// It creates and removes its own two deny-all VMs; it never attaches to a user's VM.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
)

func main() {
	agent := flag.String("agent", "", "new Linux amd64 studio-agent binary to qualify")
	flag.Parse()
	if *agent == "" {
		fmt.Fprintln(os.Stderr, "-agent is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if err := run(ctx, *agent); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, agent string) (retErr error) {
	dir, err := os.MkdirTemp("", "studio-export-roundtrip-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove roundtrip temporary root: %w", err))
		}
	}()
	guestDir := filepath.Join(dir, "guest")
	if err := os.Mkdir(guestDir, 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(agent)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(guestDir, "studio-agent"), data, 0o755); err != nil {
		return err
	}
	id := "studio-export-" + strings.ToLower(rand.Text()[:8])
	hub := agentchan.NewHub(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	socket := filepath.Join(dir, "agent.sock")
	if err := hub.Listen(id, socket); err != nil {
		return err
	}
	defer hub.Close(id)
	source, err := msb.CreateSandbox(ctx, id+"-source",
		msb.WithImage("sandbox-studio-base:dev"), msb.WithPullPolicy(msb.PullPolicyNever),
		msb.WithCPUs(1), msb.WithMemory(512), msb.WithDetached(),
		msb.WithNetwork(denyAll()),
		msb.WithMounts(map[string]msb.MountConfig{"/opt/studio": msb.Mount.Bind(guestDir, msb.MountOptions{Readonly: true})}),
		msb.WithVsock(msb.VsockRoute{HostSocket: socket, Port: agentproto.VsockPort}))
	if err != nil {
		return fmt.Errorf("create export source: %w", err)
	}
	sourceCleanupDone := false
	cleanupSource := func() error {
		if sourceCleanupDone {
			return nil
		}
		sourceCleanupDone = true
		return removeVM(source, id+"-source")
	}
	defer func() { retErr = errors.Join(retErr, cleanupSource()) }()
	if _, err := python(ctx, source, fixtures); err != nil {
		return err
	}
	baseRoots, err := python(ctx, source, rootDigest)
	if err != nil {
		return err
	}
	if _, err := python(ctx, source, privateFixtures); err != nil {
		return err
	}
	if _, err := python(ctx, source, largeExportFixture); err != nil {
		return fmt.Errorf("create owner-death export fixture: %w", err)
	}
	if err := qualifyOwnerDeath(ctx, source, dir); err != nil {
		return fmt.Errorf("owner-death capture qualification: %w", err)
	}
	if _, err := python(ctx, source, ownerDeathResumption); err != nil {
		return fmt.Errorf("source did not resume after owner death: %w", err)
	}
	fmt.Println("PASS: source resumed writes after owner death")
	if _, err := python(ctx, source, `import subprocess
subprocess.Popen(['/opt/studio/studio-agent','connect'],stdin=subprocess.DEVNULL,
                 stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True)
`); err != nil {
		return err
	}
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = hub.WaitConnected(ready, id)
	cancel()
	if err != nil {
		return fmt.Errorf("guest agent did not connect: %w", err)
	}
	if err := qualifyWatchdogDeath(ctx, source, hub, id, dir); err != nil {
		return fmt.Errorf("watchdog-death capture qualification: %w", err)
	}
	if _, err := python(ctx, source, watchdogDeathResumptionAndCleanup); err != nil {
		return fmt.Errorf("source did not resume after watchdog death or fixture cleanup failed: %w", err)
	}
	fmt.Println("PASS: source resumed writes after watchdog death; large fixture removed")
	base, err := inspectRegistryBase(ctx)
	if err != nil {
		return fmt.Errorf("inspect warmed sandbox-studio base: %w", err)
	}
	fmt.Println("NOTE: sandbox-studio-base:dev is intentionally warmed by the source VM; this probe does not qualify a cold base-image pull")
	registry, err := openRegistryProbe(ctx, dir)
	if err != nil {
		return fmt.Errorf("open private template registry: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, registry.Close()) }()
	staging, err := registry.StagingDir(ctx)
	if err != nil {
		return fmt.Errorf("create registry receive directory: %w", err)
	}
	layer, err := exportOverExec(ctx, source, staging)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	fmt.Printf("exported %d entries, %d tar bytes, %d gzip bytes; %s\n", layer.Entries, layer.UncompressedSize, layer.Size, layer.Digest)
	if _, err := python(ctx, source, "open('/tmp/after-export','w').write('thawed')"); err != nil {
		return fmt.Errorf("source did not resume writes: %w", err)
	}
	if err := cleanupSource(); err != nil {
		return fmt.Errorf("remove export source before destination startup: %w", err)
	}
	tmpl, image, ref, err := registry.Publish(ctx, base, layer, roundtripCanonicalSpec)
	if err != nil {
		return err
	}
	otherEnv, err := registry.CreateEnvironment(ctx, "roundtrip-cross-environment")
	if err != nil {
		return err
	}
	if err := checkRegistryHTTP(ctx, registry.addr, tmpl, image, ref, otherEnv.ID); err != nil {
		return fmt.Errorf("registry HTTP qualification: %w", err)
	}
	fmt.Println("PASS: unauthenticated manifest request returned 401")
	fmt.Println("PASS: authenticated cross-environment manifest request returned 404")
	fmt.Println("PASS: authenticated manifest GET returned the published bytes and OCI headers")
	if err := registry.Restart(ctx); err != nil {
		return fmt.Errorf("restart private template registry on its original address: %w", err)
	}
	refAfterRestart, err := registry.registry.Resolve(ctx, registry.env.ID, tmpl.ID)
	if err != nil {
		return fmt.Errorf("resolve template after registry restart: %w", err)
	}
	if refAfterRestart.Image != ref.Image || refAfterRestart.Username != ref.Username || refAfterRestart.Password != ref.Password {
		return errors.New("template image reference or credentials changed after registry restart")
	}
	if err := checkAuthenticatedManifest(ctx, registry.addr, tmpl, image, refAfterRestart); err != nil {
		return fmt.Errorf("authenticated manifest request after registry restart: %w", err)
	}
	fmt.Println("PASS: registry restart preserved image reference and authentication")
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cachedImage, err := msb.Image.Get(cleanup, ref.Image)
		if msb.IsKind(err, msb.ErrImageNotFound) {
			return
		}
		if err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("find unique roundtrip image for cleanup: %w", err))
			return
		}
		retErr = errors.Join(retErr, cachedImage.Remove(cleanup, false))
	}()
	manifestRequestsBeforeSDK := registry.authenticatedManifestRequests.Load()
	destination, err := msb.CreateSandbox(ctx, id+"-import", msb.WithImage(ref.Image), msb.WithRegistryInsecure(),
		msb.WithRegistryAuth(msb.RegistryAuth{Username: ref.Username, Password: ref.Password}),
		msb.WithCPUs(1), msb.WithMemory(512), msb.WithDetached(), msb.WithNetwork(denyAll()))
	if err != nil {
		return fmt.Errorf("import exported layer: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, removeVM(destination, id+"-import")) }()
	if count := registry.authenticatedManifestRequests.Load() - manifestRequestsBeforeSDK; count == 0 {
		return errors.New("SDK destination creation made no authenticated manifest request to the private registry")
	}
	fmt.Println("PASS: SDK authenticated to the private registry for the manifest pull")
	if _, err := python(ctx, destination, verify); err != nil {
		return err
	}
	gotRoots, err := python(ctx, destination, rootDigest)
	if err != nil {
		return err
	}
	if gotRoots != baseRoots {
		return errors.New("environment CA state leaked into imported image")
	}
	fmt.Println("PASS: file contents, modes, symlink, hardlink, whiteout, opaque directory, untouched base, exclusions, source thaw")
	return nil
}

const (
	ownerDeathPrefixLimit = 64 << 10
	ownerDeathExecTimeout = 150 * time.Second
)

type failedCaptureResult struct {
	layer templateexport.Layer
	err   error
}

type faultProbeReport struct {
	pid              uint32
	firstStdout      bool
	triggerConfirmed bool
	triggerErr       error
	sawExited        bool
	exitCode         int
	sawDone          bool
	stdoutPrefix     []byte
	stderrPrefix     []byte
	recvErr          error
	receiverErr      error
	sawCompletion    bool
}

func qualifyOwnerDeath(ctx context.Context, source *msb.Sandbox, dir string) error {
	report, err := runCaptureFault(ctx, source, dir, true, func(handle *msb.ExecHandle, pid uint32) error {
		if pid <= 1 {
			return fmt.Errorf("refusing to signal invalid export-layer PID %d", pid)
		}
		return handle.Signal(ctx, int(syscall.SIGKILL))
	})
	if err != nil {
		return err
	}
	fmt.Printf("PASS: owner-death capture failed after SIGKILL of PID %d at first stdout data frame; prefix %d bytes, exit %d, no completion\n",
		report.pid, len(report.stdoutPrefix), report.exitCode)
	return nil
}

// exportOverExec streams export-layer stdout into templateexport.Receive, as
// the template builder does.
func exportOverExec(ctx context.Context, source *msb.Sandbox, dir string) (templateexport.Layer, error) {
	handle, err := source.ExecStream(ctx, "/opt/studio/studio-agent", []string{"export-layer"},
		msb.WithExecUser("root"), msb.WithExecTimeout(ownerDeathExecTimeout))
	if err != nil {
		return templateexport.Layer{}, fmt.Errorf("start export-layer stream: %w", err)
	}
	defer handle.Close()
	reader, writer := io.Pipe()
	received := make(chan error, 1)
	var layer templateexport.Layer
	go func() {
		var err error
		layer, err = templateexport.Receive(ctx, dir, reader, ocilayer.Limits{})
		received <- err
	}()
	var stderr []byte
	exit := -1
	for exit < 0 {
		ev, err := handle.Recv(ctx)
		if err != nil {
			_ = writer.CloseWithError(err)
			return templateexport.Layer{}, errors.Join(err, <-received)
		}
		switch ev.Kind {
		case msb.ExecEventStdout:
			if _, err := writer.Write(ev.Data); err != nil {
				_ = handle.Kill(context.Background())
			}
		case msb.ExecEventStderr:
			stderr = appendPrefix(stderr, ev.Data, 4<<10)
		case msb.ExecEventExited:
			exit = ev.ExitCode
		case msb.ExecEventDone, msb.ExecEventFailed:
			exit = max(exit, 255)
		}
	}
	_ = writer.Close()
	if err := <-received; err != nil || exit != 0 {
		if layer.Path != "" {
			_ = os.Remove(layer.Path)
		}
		return templateexport.Layer{}, errors.Join(err, fmt.Errorf("export-layer exited %d: %s", exit, stderr))
	}
	return layer, nil
}

// runCaptureFault streams export-layer output into the same artifact receiver
// used by the template builder while retaining only a bounded diagnostic prefix. The
// callback runs on the first stdout event, or the first data frame when asked.
func runCaptureFault(ctx context.Context, source *msb.Sandbox, dir string, afterFirstDataFrame bool, fault func(*msb.ExecHandle, uint32) error) (faultProbeReport, error) {
	var report faultProbeReport
	handle, err := source.ExecStream(ctx, "/opt/studio/studio-agent", []string{"export-layer"},
		msb.WithExecUser("root"), msb.WithExecTimeout(ownerDeathExecTimeout))
	if err != nil {
		return report, fmt.Errorf("start export-layer stream: %w", err)
	}

	pipeReader, pipeWriter := io.Pipe()
	boundaryDone := make(chan failedCaptureResult, 1)
	go func() {
		defer pipeReader.Close()
		layer, err := templateexport.Receive(ctx, dir, pipeReader, ocilayer.Limits{})
		boundaryDone <- failedCaptureResult{layer: layer, err: err}
	}()
	var observer exportFrameObserver
	var streamDone bool
	var triggerAttempted bool
	recvCtx := ctx
	var cleanupCancel context.CancelFunc
	for !streamDone {
		ev, recvErr := handle.Recv(recvCtx)
		if recvErr != nil {
			report.recvErr = errors.Join(report.recvErr, recvErr)
			if cleanupCancel != nil {
				break
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cleanupCancel = cancel
			_ = handle.Kill(cleanupCtx)
			recvCtx = cleanupCtx
			continue
		}
		switch ev.Kind {
		case msb.ExecEventStarted:
			report.pid = ev.PID
		case msb.ExecEventStdout:
			if len(ev.Data) == 0 {
				continue
			}
			if !report.firstStdout {
				report.firstStdout = true
				if !afterFirstDataFrame {
					triggerAttempted = true
					report.triggerErr = invokeCaptureFault(fault, handle, report.pid)
					report.triggerConfirmed = report.triggerErr == nil
				}
			}
			observer.add(ev.Data)
			if afterFirstDataFrame && observer.sawDataFrame && !triggerAttempted {
				triggerAttempted = true
				report.triggerErr = invokeCaptureFault(fault, handle, report.pid)
				report.triggerConfirmed = report.triggerErr == nil
			}
			report.stdoutPrefix = appendPrefix(report.stdoutPrefix, ev.Data, ownerDeathPrefixLimit)
			_, _ = pipeWriter.Write(ev.Data)
		case msb.ExecEventStderr:
			report.stderrPrefix = appendPrefix(report.stderrPrefix, ev.Data, 4<<10)
		case msb.ExecEventExited:
			report.sawExited = true
			report.exitCode = ev.ExitCode
		case msb.ExecEventFailed:
			if ev.Failure != nil {
				report.recvErr = errors.Join(report.recvErr, fmt.Errorf("export-layer failed to start: %s", ev.Failure.Message))
			}
		case msb.ExecEventDone:
			report.sawDone = true
			streamDone = true
		}
	}
	if cleanupCancel != nil {
		cleanupCancel()
	}
	if report.sawDone {
		_ = pipeWriter.Close()
	} else {
		_ = pipeWriter.CloseWithError(errors.New("export-layer event stream ended before Done"))
	}
	closeErr := handle.Close() // Recv has returned; the handle is no longer in use.
	boundary := <-boundaryDone
	report.receiverErr = boundary.err
	report.sawCompletion = observer.sawCompletion
	var cleanupErr error
	if boundary.layer.Path != "" {
		cleanupErr = fmt.Errorf("artifact receiver returned unexpected layer path %q for faulted capture", boundary.layer.Path)
		if err := os.Remove(boundary.layer.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove unexpected layer artifact: %w", err))
		}
	}
	if err := assertNoLayerArtifacts(dir); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if cleanupErr != nil {
		return report, fmt.Errorf("faulted capture artifact cleanup failed: %w", cleanupErr)
	}

	if !report.firstStdout {
		return report, fmt.Errorf("export-layer emitted no stdout data; stream error: %v; stderr: %q", report.recvErr, report.stderrPrefix)
	}
	if !report.triggerConfirmed {
		return report, fmt.Errorf("fault trigger was not confirmed: %v; stderr: %q", report.triggerErr, report.stderrPrefix)
	}
	if !report.sawDone || !report.sawExited {
		return report, fmt.Errorf("faulted export did not drain through Exited and Done: exit=%v code=%d stream error=%v", report.sawExited, report.exitCode, report.recvErr)
	}
	if report.exitCode == 0 {
		return report, errors.New("faulted export-layer exited successfully")
	}
	if report.sawCompletion {
		return report, errors.New("faulted export emitted an export completion frame")
	}
	if report.receiverErr == nil {
		return report, errors.New("artifact receiver accepted faulted export or reported no failure")
	}
	if closeErr != nil {
		return report, fmt.Errorf("close drained export-layer handle: %w", closeErr)
	}
	return report, nil
}

func invokeCaptureFault(fault func(*msb.ExecHandle, uint32) error, handle *msb.ExecHandle, pid uint32) error {
	if fault == nil {
		return errors.New("capture fault trigger is unavailable")
	}
	return fault(handle, pid)
}

func appendPrefix(prefix, data []byte, limit int) []byte {
	remaining := limit - len(prefix)
	if remaining <= 0 {
		return prefix
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	return append(prefix, data...)
}

type exportFrameObserver struct {
	header        [5]byte
	headerBytes   int
	payloadRemain uint64
	sawCompletion bool
	sawDataFrame  bool
}

func (o *exportFrameObserver) add(data []byte) {
	for len(data) > 0 {
		if o.payloadRemain > 0 {
			consumed := min(uint64(len(data)), o.payloadRemain)
			data = data[int(consumed):]
			o.payloadRemain -= consumed
			continue
		}
		copied := copy(o.header[o.headerBytes:], data)
		o.headerBytes += copied
		data = data[copied:]
		if o.headerBytes == len(o.header) {
			if o.header[0] == agentproto.ExportFrameComplete {
				o.sawCompletion = true
			}
			if o.header[0] == agentproto.ExportFrameData {
				o.sawDataFrame = true
			}
			o.payloadRemain = uint64(binary.BigEndian.Uint32(o.header[1:]))
			o.headerBytes = 0
		}
	}
}

func assertNoLayerArtifacts(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".oci-layer-") {
			return fmt.Errorf("incomplete layer artifact remains: %s", entry.Name())
		}
	}
	return nil
}

const watchdogControlPort = 19384

type watchdogControlResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	sawExit  bool
	sawDone  bool
	recvErr  error
}

type watchdogControl struct {
	handle   *msb.ExecHandle
	done     chan watchdogControlResult
	cancel   context.CancelFunc
	finished bool
	result   watchdogControlResult
}

func qualifyWatchdogDeath(ctx context.Context, source *msb.Sandbox, hub *agentchan.Hub, id, dir string) error {
	control, err := startWatchdogControl(ctx, source)
	if err != nil {
		return err
	}
	var watchdogPID uint32
	report, captureErr := runCaptureFault(ctx, source, dir, true, func(_ *msb.ExecHandle, exportPID uint32) error {
		if exportPID <= 1 {
			return fmt.Errorf("refusing watchdog fault without valid export-layer PID: %d", exportPID)
		}
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		conn, err := hub.DialTCP(dialCtx, id, watchdogControlPort)
		if err != nil {
			return fmt.Errorf("connect to watchdog control server: %w", err)
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return fmt.Errorf("set watchdog control deadline: %w", err)
		}
		if _, err := io.WriteString(conn, "KILL-WATCHDOG\n"); err != nil {
			return fmt.Errorf("send watchdog fault command: %w", err)
		}
		line, err := bufio.NewReader(io.LimitReader(conn, 64)).ReadString('\n')
		if err != nil {
			return fmt.Errorf("read watchdog fault acknowledgement: %w", err)
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "ACK" {
			return fmt.Errorf("unexpected watchdog fault acknowledgement %q", strings.TrimSpace(line))
		}
		pid, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil || pid <= 1 {
			return fmt.Errorf("invalid watchdog PID in acknowledgement %q", strings.TrimSpace(line))
		}
		watchdogPID = uint32(pid)
		return nil
	})
	if captureErr != nil {
		return errors.Join(captureErr, control.stopAndDrain())
	}
	result, waitErr := control.wait(ctx)
	if waitErr != nil {
		return errors.Join(waitErr, control.stopAndDrain())
	}
	closeErr := control.close()
	if result.recvErr != nil || !result.sawExit || !result.sawDone || result.exitCode != 0 {
		return errors.Join(fmt.Errorf("watchdog control server did not exit cleanly: exited=%v code=%d done=%v recv=%v stdout=%q stderr=%q",
			result.sawExit, result.exitCode, result.sawDone, result.recvErr, result.stdout, result.stderr), closeErr)
	}
	if watchdogPID <= 1 {
		return errors.Join(errors.New("watchdog control server did not confirm a killed helper PID"), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close drained watchdog control handle: %w", closeErr)
	}
	fmt.Printf("PASS: killed exact freeze watchdog PID %d after first export data frame; export PID %d failed with no completion\n",
		watchdogPID, report.pid)
	return nil
}

func startWatchdogControl(ctx context.Context, source *msb.Sandbox) (*watchdogControl, error) {
	handle, err := source.ExecStream(ctx, "python3", []string{"-u", "-c", watchdogControlServer},
		msb.WithExecUser("root"), msb.WithExecTimeout(ownerDeathExecTimeout))
	if err != nil {
		return nil, fmt.Errorf("start watchdog control server: %w", err)
	}
	recvCtx, cancel := context.WithCancel(context.Background())
	ready := make(chan error, 1)
	control := &watchdogControl{handle: handle, done: make(chan watchdogControlResult, 1), cancel: cancel}
	go func() {
		var result watchdogControlResult
		readySent := false
		sendReady := func(err error) {
			if readySent {
				return
			}
			readySent = true
			ready <- err
		}
		for {
			ev, err := handle.Recv(recvCtx)
			if err != nil {
				result.recvErr = errors.Join(result.recvErr, err)
				sendReady(fmt.Errorf("watchdog control server stream ended before readiness: %w", result.recvErr))
				break
			}
			switch ev.Kind {
			case msb.ExecEventStdout:
				result.stdout = appendPrefix(result.stdout, ev.Data, 4<<10)
				if strings.Contains(string(result.stdout), "READY\n") {
					sendReady(nil)
				}
			case msb.ExecEventStderr:
				result.stderr = appendPrefix(result.stderr, ev.Data, 4<<10)
			case msb.ExecEventExited:
				result.sawExit = true
				result.exitCode = ev.ExitCode
			case msb.ExecEventFailed:
				if ev.Failure != nil {
					result.recvErr = errors.Join(result.recvErr, fmt.Errorf("watchdog control server failed to start: %s", ev.Failure.Message))
				}
			case msb.ExecEventDone:
				result.sawDone = true
				if !readySent {
					sendReady(fmt.Errorf("watchdog control server exited before readiness; stdout=%q stderr=%q", result.stdout, result.stderr))
				}
				control.done <- result
				return
			}
		}
		control.done <- result
	}()
	select {
	case readyErr := <-ready:
		if readyErr != nil {
			_, _ = control.wait(context.Background())
			_ = control.close()
			return nil, readyErr
		}
		return control, nil
	case <-ctx.Done():
		return nil, errors.Join(fmt.Errorf("wait for watchdog control readiness: %w", ctx.Err()), control.stopAndDrain())
	}
}

func (c *watchdogControl) wait(ctx context.Context) (watchdogControlResult, error) {
	if c.finished {
		return c.result, nil
	}
	select {
	case c.result = <-c.done:
		c.finished = true
		return c.result, nil
	case <-ctx.Done():
		return watchdogControlResult{}, ctx.Err()
	}
}

func (c *watchdogControl) stopAndDrain() error {
	var killErr error
	if !c.finished {
		select {
		case c.result = <-c.done:
			c.finished = true
		default:
			killCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			killErr = c.handle.Kill(killCtx)
			cancel()
			select {
			case c.result = <-c.done:
				c.finished = true
			case <-time.After(10 * time.Second):
				c.cancel()
				c.result = <-c.done
				c.finished = true
			}
		}
	}
	closeErr := c.handle.Close()
	c.cancel()
	if !c.result.sawDone {
		return errors.Join(killErr, fmt.Errorf("watchdog control server stream ended before Done: %v", c.result.recvErr), closeErr)
	}
	return closeErr
}

func (c *watchdogControl) close() error {
	if !c.finished {
		return errors.New("cannot close watchdog control handle before its stream is drained")
	}
	err := c.handle.Close()
	c.cancel()
	return err
}

func denyAll() *msb.NetworkConfig {
	return &msb.NetworkConfig{DefaultEgress: msb.PolicyActionDeny, DefaultIngress: msb.PolicyActionDeny}
}

func removeVM(sb *msb.Sandbox, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	stop := sb.Stop(ctx)
	remove := msb.RemoveSandbox(ctx, name)
	return errors.Join(stop, remove)
}

func python(ctx context.Context, sb *msb.Sandbox, script string) (string, error) {
	out, err := sb.Exec(ctx, "python3", []string{"-c", script}, msb.WithExecUser("root"), msb.WithExecTimeout(30*time.Second))
	if err != nil {
		return "", err
	}
	if !out.Success() {
		return "", fmt.Errorf("guest assertion failed: %s", out.Stderr())
	}
	return out.Stdout(), nil
}

const roundtripCanonicalSpec = `{"schema":"sandbox-studio/layer-export-roundtrip-v1","cpus":1,"memoryMiB":512,"network":"deny-all"}`

// roundtripSealer is an AAD-bound plaintext fake for this private live probe.
// It avoids connecting the probe to any real keychain or vault.
type roundtripSealer struct{}

func (roundtripSealer) Seal(plain, aad []byte) []byte {
	sealed := append(append([]byte(nil), aad...), 0)
	return append(sealed, plain...)
}

func (roundtripSealer) Unseal(sealed, aad []byte) ([]byte, error) {
	prefix := append(append([]byte(nil), aad...), 0)
	if !bytes.HasPrefix(sealed, prefix) {
		return nil, errors.New("roundtrip registry sealed value has mismatched AAD")
	}
	return append([]byte(nil), sealed[len(prefix):]...), nil
}

type registryProbe struct {
	store                         *store.Store
	env                           store.Environment
	registry                      *templateregistry.Registry
	root                          string
	dbPath                        string
	addr                          string
	listener                      net.Listener
	server                        *http.Server
	serveDone                     chan error
	authenticatedManifestRequests atomic.Uint64
}

func openRegistryProbe(ctx context.Context, tempRoot string) (*registryProbe, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &registryProbe{
		root: filepath.Join(tempRoot, "registry-data"), dbPath: filepath.Join(tempRoot, "registry-catalog.sqlite"),
		addr: listener.Addr().String(), listener: listener,
	}
	fail := func(err error) (*registryProbe, error) { return nil, errors.Join(err, p.Close()) }
	p.store, err = store.Open(ctx, p.dbPath)
	if err != nil {
		return fail(err)
	}
	p.env, err = p.store.CreateEnvironment(ctx, "layer-export-roundtrip")
	if err != nil {
		return fail(err)
	}
	p.registry, err = templateregistry.Open(ctx, p.store, p.root, p.addr, roundtripSealer{})
	if err != nil {
		return fail(err)
	}
	if err := p.startHTTP(); err != nil {
		return fail(err)
	}
	return p, nil
}

func (p *registryProbe) startHTTP() error {
	if p.registry == nil || p.listener == nil {
		return errors.New("registry HTTP server is missing its registry or listener")
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: countAuthenticatedManifests(&p.authenticatedManifestRequests, p.registry.Handler())}
	done := make(chan error, 1)
	listener := p.listener
	p.server, p.serveDone = server, done
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			err = nil
		}
		done <- err
	}()
	return nil
}

func (p *registryProbe) stopHTTP() error {
	server, listener, done := p.server, p.listener, p.serveDone
	p.server, p.listener, p.serveDone = nil, nil, nil
	var stopErr error
	if server != nil {
		if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			stopErr = errors.Join(stopErr, err)
		}
	}
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			stopErr = errors.Join(stopErr, err)
		}
	}
	if done != nil {
		if err := <-done; err != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("serve registry HTTP: %w", err))
		}
	}
	return stopErr
}

func (p *registryProbe) Close() error {
	var closeErr error
	closeErr = errors.Join(closeErr, p.stopHTTP())
	if p.registry != nil {
		closeErr = errors.Join(closeErr, p.registry.Close())
		p.registry = nil
	}
	if p.store != nil {
		closeErr = errors.Join(closeErr, p.store.Close())
		p.store = nil
	}
	return closeErr
}

func (p *registryProbe) Restart(ctx context.Context) error {
	if err := p.stopHTTP(); err != nil {
		return err
	}
	envID := p.env.ID
	if p.registry != nil {
		if err := p.registry.Close(); err != nil {
			return err
		}
		p.registry = nil
	}
	if p.store != nil {
		if err := p.store.Close(); err != nil {
			return err
		}
		p.store = nil
	}
	reopenedStore, err := store.Open(ctx, p.dbPath)
	if err != nil {
		return fmt.Errorf("reopen private registry catalog: %w", err)
	}
	p.store = reopenedStore
	p.env, err = p.store.Environment(ctx, envID)
	if err != nil {
		return fmt.Errorf("reload registry environment after restart: %w", err)
	}
	registry, err := templateregistry.Open(ctx, p.store, p.root, p.addr, roundtripSealer{})
	if err != nil {
		return err
	}
	p.registry = registry
	listener, err := net.Listen("tcp", p.addr)
	if err != nil {
		return fmt.Errorf("rebind template registry to original address %s: %w", p.addr, err)
	}
	p.listener = listener
	return p.startHTTP()
}

func (p *registryProbe) StagingDir(ctx context.Context) (string, error) {
	return p.registry.StagingDir(ctx, p.env.ID)
}

func (p *registryProbe) CreateEnvironment(ctx context.Context, name string) (store.Environment, error) {
	return p.store.CreateEnvironment(ctx, name)
}

func (p *registryProbe) Publish(ctx context.Context, base templateimage.Base, layer templateexport.Layer, spec string) (store.Template, templateimage.Image, templateregistry.Reference, error) {
	platform := base.OS + "/" + base.Architecture
	if base.Variant != "" {
		platform += "/" + base.Variant
	}
	cacheKey, err := templateimage.CacheKey([]byte(spec), base.Digest, platform, templateimage.ExporterVersion)
	if err != nil {
		return store.Template{}, templateimage.Image{}, templateregistry.Reference{}, err
	}
	image, err := templateimage.Compose(base, layer)
	if err != nil {
		return store.Template{}, templateimage.Image{}, templateregistry.Reference{}, err
	}
	in := store.Template{
		EnvironmentID: p.env.ID, CacheKey: cacheKey, Spec: spec,
		BaseRef: base.Reference, BaseDigest: base.Digest, Platform: platform,
		ExporterVersion: templateimage.ExporterVersion,
	}
	tmpl, err := p.registry.Publish(ctx, in, image, layer)
	if err != nil {
		return store.Template{}, templateimage.Image{}, templateregistry.Reference{}, err
	}
	ref, err := p.registry.Resolve(ctx, p.env.ID, tmpl.ID)
	return tmpl, image, ref, err
}

type responseStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseStatusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func countAuthenticatedManifests(counter *atomic.Uint64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, hasBasicAuth := r.BasicAuth()
		manifestRoute := (r.Method == http.MethodGet || r.Method == http.MethodHead) && isManifestRoute(r.URL.Path)
		statusWriter := &responseStatusWriter{ResponseWriter: w}
		next.ServeHTTP(statusWriter, r)
		if hasBasicAuth && manifestRoute && statusWriter.status == http.StatusOK {
			counter.Add(1)
		}
	})
}

func isManifestRoute(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) == 7 && parts[0] == "" && parts[1] == "v2" && parts[2] == "studio" && parts[5] == "manifests"
}

func inspectRegistryBase(ctx context.Context) (templateimage.Base, error) {
	detail, err := msb.Image.Inspect(ctx, "sandbox-studio-base:dev")
	if err != nil {
		return templateimage.Base{}, err
	}
	if detail == nil || detail.Config == nil {
		return templateimage.Base{}, errors.New("SDK returned incomplete dev-base metadata")
	}
	base := templateimage.Base{
		Reference: detail.Reference(), Digest: detail.ManifestDigest(),
		Architecture: detail.Architecture(), OS: detail.OS(),
		Config: templateimage.Config{
			Env: append([]string(nil), detail.Config.Env...), Cmd: append([]string(nil), detail.Config.Cmd...),
			Entrypoint: append([]string(nil), detail.Config.Entrypoint...), WorkingDir: detail.Config.WorkingDir,
			User: detail.Config.User, StopSignal: detail.Config.StopSignal, Labels: detail.Config.Labels,
		},
		Layers: make([]templateimage.BaseLayer, 0, len(detail.Layers)),
	}
	for _, layer := range detail.Layers {
		if layer.CompressedSizeBytes == nil {
			return templateimage.Base{}, errors.New("SDK base layer compressed size is missing")
		}
		base.Layers = append(base.Layers, templateimage.BaseLayer{
			Descriptor: templateimage.Descriptor{MediaType: layer.MediaType, Digest: layer.BlobDigest, Size: *layer.CompressedSizeBytes},
			DiffID:     layer.DiffID,
		})
	}
	return base, nil
}

func registryManifestPath(envID, templateID, digest string) string {
	return "/v2/studio/" + envID + "/" + templateID + "/manifests/" + digest
}

func registryGET(ctx context.Context, addr, path, username, password string) (int, http.Header, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	if username != "" || password != "" {
		request.SetBasicAuth(username, password)
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	return response.StatusCode, response.Header.Clone(), body, errors.Join(readErr, closeErr)
}

func checkRegistryHTTP(ctx context.Context, addr string, tmpl store.Template, image templateimage.Image, ref templateregistry.Reference, otherEnvID string) error {
	digest := image.ManifestDescriptor.Digest
	status, _, _, err := registryGET(ctx, addr, registryManifestPath(tmpl.EnvironmentID, tmpl.ID, digest), "", "")
	if err != nil {
		return err
	}
	if status != http.StatusUnauthorized {
		return fmt.Errorf("unauthenticated manifest request returned %d, want 401", status)
	}
	status, _, _, err = registryGET(ctx, addr, registryManifestPath(otherEnvID, tmpl.ID, digest), ref.Username, ref.Password)
	if err != nil {
		return err
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("cross-environment manifest request returned %d, want 404", status)
	}
	return checkAuthenticatedManifest(ctx, addr, tmpl, image, ref)
}

func checkAuthenticatedManifest(ctx context.Context, addr string, tmpl store.Template, image templateimage.Image, ref templateregistry.Reference) error {
	status, header, body, err := registryGET(ctx, addr, registryManifestPath(tmpl.EnvironmentID, tmpl.ID, image.ManifestDescriptor.Digest), ref.Username, ref.Password)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("authenticated manifest request returned %d, want 200", status)
	}
	if header.Get("Content-Type") != templateimage.MediaManifest || header.Get("Docker-Content-Digest") != image.ManifestDescriptor.Digest {
		return errors.New("authenticated manifest response has incorrect OCI headers")
	}
	if !bytes.Equal(body, image.Manifest) {
		return errors.New("authenticated manifest response bytes differ from published image")
	}
	return nil
}

const fixtures = `import os, pathlib, shutil
p=pathlib.Path('/usr/local/lib/studio-export-fixture');p.mkdir()
(p/'file').write_text('export roundtrip\n');(p/'file').chmod(0o751)
os.link(p/'file',p/'hard');os.symlink('file',p/'symbolic')
assert pathlib.Path('/etc/issue.net').is_file();pathlib.Path('/etc/issue.net').unlink()
d=pathlib.Path('/usr/share/doc/base-files');assert (d/'copyright').is_file()
shutil.rmtree(d);d.mkdir();(d/'only-this').write_text('opaque\n')
assert pathlib.Path('/etc/issue').is_file()
`
const largeExportFixture = `import os, pathlib
p=pathlib.Path('/usr/local/lib/studio-export-fixture/large')
with p.open('wb') as f:
 for _ in range(64):
  f.write(os.urandom(1<<20))
 f.flush();os.fsync(f.fileno())
assert p.is_file() and p.stat().st_size==64*1024*1024
print('created 64 MiB incompressible owner-death fixture')
`
const ownerDeathResumption = `import pathlib
fixture=pathlib.Path('/usr/local/lib/studio-export-fixture/large')
assert fixture.is_file() and fixture.stat().st_size==64*1024*1024
p=pathlib.Path('/tmp/studio-owner-death-write-check');p.write_text('thawed after owner death')
assert p.read_text()=='thawed after owner death';p.unlink()
print('source writes resumed after owner death')
`
const watchdogDeathResumptionAndCleanup = `import pathlib
fixture=pathlib.Path('/usr/local/lib/studio-export-fixture/large')
assert fixture.is_file() and fixture.stat().st_size==64*1024*1024
p=pathlib.Path('/tmp/studio-watchdog-death-write-check');p.write_text('thawed after watchdog death')
assert p.read_text()=='thawed after watchdog death';p.unlink()
fixture.unlink();assert not fixture.exists()
print('source writes resumed after watchdog death; large fixture removed')
`
const watchdogControlServer = `import os,signal,socket,sys
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(('127.0.0.1',19384));s.listen(1)
print('READY',flush=True)
conn,_=s.accept()
with conn:
 command=conn.makefile('rb').readline(64)
 if command!=b'KILL-WATCHDOG\n':
  conn.sendall(b'ERROR unexpected command\n');sys.exit(1)
 matches=[]
 for entry in os.listdir('/proc'):
  if not entry.isdecimal(): continue
  pid=int(entry)
  if pid<=1: continue
  try: argv=open('/proc/'+entry+'/cmdline','rb').read().split(b'\0')
  except (OSError,PermissionError): continue
  if len(argv)>=2 and argv[0]==b'/opt/studio/studio-agent' and argv[1]==b'__capture-watchdog':
   matches.append(pid)
 if len(matches)!=1:
  conn.sendall(('ERROR expected one watchdog, found %d\n'%len(matches)).encode())
  sys.exit(1)
 pid=matches[0]
 try: os.kill(pid,signal.SIGKILL)
 except OSError as err:
  conn.sendall(('ERROR kill failed: %s\n'%err).encode());sys.exit(1)
 conn.sendall(('ACK %d\n'%pid).encode())
s.close()
`
const rootDigest = `import hashlib;print(hashlib.sha256(open('/etc/ssl/certs/ca-certificates.crt','rb').read()).hexdigest())`
const privateFixtures = `from pathlib import Path
for name in ['/workspace/studio-export-excluded','/var/lib/docker/studio-export-excluded','/etc/sandbox-studio/env','/tmp/studio-export-excluded','/run/studio-export-excluded','/var/log/studio-export-excluded','/usr/local/share/ca-certificates/sandbox-studio.crt']:
 p=Path(name);p.parent.mkdir(parents=True,exist_ok=True);p.write_text('private export fixture\n')
Path('/etc/ssl/certs/ca-certificates.crt').write_text('environment specific roots\n')
`
const verify = `import os, pathlib, stat
p=pathlib.Path('/usr/local/lib/studio-export-fixture')
assert (p/'file').read_text()=='export roundtrip\n'
assert stat.S_IMODE((p/'file').stat().st_mode)==0o751
assert os.readlink(p/'symbolic')=='file'
assert (p/'hard').stat().st_ino==(p/'file').stat().st_ino
assert not pathlib.Path('/etc/issue.net').exists()
assert sorted(x.name for x in pathlib.Path('/usr/share/doc/base-files').iterdir())==['only-this']
assert pathlib.Path('/etc/issue').is_file()
for name in ['/workspace/studio-export-excluded','/var/lib/docker/studio-export-excluded','/etc/sandbox-studio/env','/tmp/studio-export-excluded','/run/studio-export-excluded','/var/log/studio-export-excluded','/usr/local/share/ca-certificates/sandbox-studio.crt']:
 assert not pathlib.Path(name).exists(), name
`
