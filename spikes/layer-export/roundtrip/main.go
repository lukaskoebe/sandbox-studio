// Command roundtrip qualifies guest layer export against the installed Studio dev base.
// It creates and removes its own two deny-all VMs; it never attaches to a user's VM.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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
	"syscall"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
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
	defer os.RemoveAll(dir)
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
	layer, err := hub.Export(ctx, id, dir, ocilayer.Limits{})
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
	ref, closeRegistry, err := registry(ctx, layer)
	if err != nil {
		return err
	}
	defer closeRegistry()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if image, err := msb.Image.Get(cleanup, ref); err == nil {
			retErr = errors.Join(retErr, image.Remove(cleanup, false))
		}
	}()
	destination, err := msb.CreateSandbox(ctx, id+"-import", msb.WithImage(ref), msb.WithRegistryInsecure(),
		msb.WithCPUs(1), msb.WithMemory(512), msb.WithDetached(), msb.WithNetwork(denyAll()))
	if err != nil {
		return fmt.Errorf("import exported layer: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, removeVM(destination, id+"-import")) }()
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

// runCaptureFault streams export-layer output into the same artifact receiver
// used by Hub.Export while retaining only a bounded diagnostic prefix. The
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

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }

// This qualification registry deliberately relies on the dev base just used by source.
// Production templates need the stable authenticated registry and cache recovery in PLAN.md.
func registry(ctx context.Context, layer templateexport.Layer) (string, func(), error) {
	base, err := msb.Image.Inspect(ctx, "sandbox-studio-base:dev")
	if err != nil {
		return "", nil, err
	}
	var layers []descriptor
	var diffIDs []string
	for _, l := range base.Layers {
		if l.CompressedSizeBytes == nil {
			return "", nil, errors.New("base layer size missing")
		}
		mt := l.MediaType
		if strings.Contains(mt, "docker") {
			mt = "application/vnd.oci.image.layer.v1.tar"
			if strings.Contains(l.MediaType, "gzip") {
				mt += "+gzip"
			}
		}
		layers = append(layers, descriptor{mt, l.BlobDigest, *l.CompressedSizeBytes})
		diffIDs = append(diffIDs, l.DiffID)
	}
	layers = append(layers, descriptor{"application/vnd.oci.image.layer.v1.tar+gzip", layer.Digest, layer.Size})
	diffIDs = append(diffIDs, layer.DiffID)
	c := base.Config
	cb, err := json.Marshal(map[string]any{"architecture": base.Architecture(), "os": base.OS(), "config": map[string]any{
		"Env": c.Env, "Cmd": c.Cmd, "Entrypoint": c.Entrypoint, "WorkingDir": c.WorkingDir, "User": c.User, "Labels": c.Labels, "StopSignal": c.StopSignal},
		"rootfs": map[string]any{"type": "layers", "diff_ids": diffIDs}})
	if err != nil {
		return "", nil, err
	}
	cd := digest(cb)
	mb, err := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": descriptor{"application/vnd.oci.image.config.v1+json", cd, int64(len(cb))}, "layers": layers})
	if err != nil {
		return "", nil, err
	}
	md := digest(mb)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		var body []byte
		switch r.URL.Path {
		case "/v2/":
			return
		case "/v2/studio/template/manifests/export", "/v2/studio/template/manifests/" + md:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", md)
			body = mb
		case "/v2/studio/template/blobs/" + cd:
			w.Header().Set("Content-Type", "application/vnd.oci.image.config.v1+json")
			w.Header().Set("Docker-Content-Digest", cd)
			body = cb
		case "/v2/studio/template/blobs/" + layer.Digest:
			f, err := os.Open(layer.Path)
			if err != nil {
				http.Error(w, "layer unavailable", 500)
				return
			}
			defer f.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", layer.Digest)
			http.ServeContent(w, r, "layer", time.Time{}, f)
			return
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if r.Method == "GET" {
			_, _ = w.Write(body)
		}
	})}
	go srv.Serve(ln)
	return ln.Addr().String() + "/studio/template:export", func() { _ = srv.Close() }, nil
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
