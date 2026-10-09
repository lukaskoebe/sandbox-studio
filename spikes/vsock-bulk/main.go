//go:build linux

// Command vsock-bulk compares a raw vsock byte stream with the same stream over yamux.
// It creates one private deny-all VM and never looks up a pre-existing Studio VM.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	studioruntime "github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

const (
	baseImage         = "sandbox-studio-base:dev"
	vsockPort         = 5001 // Studio's production agent route uses port 5000.
	guestMount        = "/opt/vsock-bulk"
	guestBinaryName   = "vsock-bulk"
	payloadSize       = uint64(128 << 20)
	readBufferSize    = 128 << 10
	progressInterval  = uint64(8 << 20)
	modeDeadline      = 45 * time.Second
	commandDeadline   = 45 * time.Second
	cleanupDeadline   = 15 * time.Second
	pendingRunWait    = 12 * time.Second
	totalDeadline     = 4 * time.Minute
	maxHandshakeBytes = 1024
	maxGuestOutput    = 32 << 10
	unixSocketMaxPath = 107
)

var protectedVMNames = [...]string{
	"ss-uodz4lvcfgduw-g1",
	"ss-flqvyhrs4pxqa-g1",
	"ss-cakcsogfdpwb4-g1",
}

type handshake struct {
	Mode string `json:"mode"`
	Size uint64 `json:"size"`
}

type acknowledgement struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type transferStats struct {
	Bytes uint64
	Hash  string
	EOF   bool
	Err   error
}

type modeResult struct {
	Mode            string
	Received        uint64
	Hash            string
	Duration        time.Duration
	Verified        bool
	ReceiverSettled bool
	Err             error
}

type recoveryRecord struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type diagnosticCapture struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func main() {
	guestPath := flag.String("guest", "", "absolute path to a compiled Linux guest binary")
	scratch := flag.String("scratch", "/tmp", "short existing directory for private spike state")
	maxWrite := flag.Int("max-write", 0, "maximum bytes per underlying vsock Write call (0 leaves writes unchanged)")
	flag.Parse()
	if *guestPath == "" || !filepath.IsAbs(*guestPath) {
		fmt.Fprintln(os.Stderr, "-guest must be an absolute path to a compiled Linux guest binary")
		os.Exit(2)
	}
	if *maxWrite < 0 || *maxWrite > 65536 {
		fmt.Fprintln(os.Stderr, "-max-write must be 0 or between 1 and 65536")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), totalDeadline)
	defer cancel()
	if err := run(ctx, *guestPath, *scratch, *maxWrite); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL vsock-bulk:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, guestSource, scratch string, maxWrite int) (retErr error) {
	guestInfo, err := os.Stat(guestSource)
	if err != nil {
		return fmt.Errorf("inspect guest binary: %w", err)
	}
	if !guestInfo.Mode().IsRegular() || guestInfo.Mode()&0o111 == 0 {
		return errors.New("-guest must be an executable regular file")
	}

	scratchPath, err := filepath.Abs(scratch)
	if err != nil {
		return fmt.Errorf("resolve -scratch directory: %w", err)
	}
	scratchInfo, err := os.Stat(scratchPath)
	if err != nil {
		return fmt.Errorf("inspect -scratch directory: %w", err)
	}
	if !scratchInfo.IsDir() {
		return errors.New("-scratch must name an existing directory")
	}
	root, err := os.MkdirTemp(scratchPath, "ss-vsock-bulk-")
	if err != nil {
		return fmt.Errorf("create private spike root: %w", err)
	}
	socketPath := filepath.Join(root, "host-vsock.sock")
	if len(socketPath) > unixSocketMaxPath {
		_ = os.RemoveAll(root)
		return errors.New("-scratch path is too long for the Unix socket route; choose a short directory such as /tmp")
	}
	keepRoot := false
	vmCreateAttempted := false
	cleanupAllowed := true
	uncertainState := ""
	recoveryPath := filepath.Join(root, "recovery.json")
	recoveryRemoved := false
	ownedCleanupConfirmed := false
	var listener *net.UnixListener
	var rt *studioruntime.Runtime
	var owner studioruntime.OwnedVM

	defer func() {
		if vmCreateAttempted {
			if !cleanupAllowed {
				keepRoot = true
				if uncertainState == "" {
					uncertainState = "VM state is uncertain"
				}
				retErr = errors.Join(retErr, fmt.Errorf("%s; recovery record and private path retained", uncertainState))
			} else if rt.PendingRun() {
				keepRoot = true
				retErr = errors.Join(retErr, errors.New("native guest command is still pending; owned VM was left untouched and recovery record retained"))
			} else {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupDeadline)
				if err := guardUnprotected(owner.Name); err != nil {
					keepRoot = true
					retErr = errors.Join(retErr, err)
				} else {
					removeErr, pending := removeOwnedBounded(cleanupCtx, rt, owner)
					if pending {
						keepRoot = true
						retErr = errors.Join(retErr, fmt.Errorf("owned VM cleanup remains inside the runtime; recovery path retained for %q", owner.Name))
					} else if removeErr != nil {
						keepRoot = true
						retErr = errors.Join(retErr, fmt.Errorf("remove owned VM %q; recovery path retained: %w", owner.Name, removeErr))
					} else {
						if err := os.Remove(recoveryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
							keepRoot = true
							retErr = errors.Join(retErr, fmt.Errorf("remove verified recovery record; private path retained: %w", err))
						} else {
							recoveryRemoved = true
							ownedCleanupConfirmed = true
							fmt.Println("PASS owned VM removal verified")
						}
					}
				}
				cleanupCancel()
			}
		}

		if listener != nil {
			if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				keepRoot = true
				retErr = errors.Join(retErr, fmt.Errorf("close host vsock route; private path retained: %w", closeErr))
			}
		}
		if keepRoot {
			fmt.Fprintln(os.Stderr, "RETAINED private spike path:", root)
			if vmCreateAttempted && !recoveryRemoved {
				fmt.Fprintln(os.Stderr, "RETAINED recovery record:", recoveryPath)
			}
			if vmCreateAttempted && owner.Name != "" && !ownedCleanupConfirmed {
				fmt.Fprintln(os.Stderr, "RETAINED owned VM name:", owner.Name)
			}
			return
		}
		if err := os.RemoveAll(root); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove private spike root: %w", err))
		}
	}()

	suffix, err := randomSuffix()
	if err != nil {
		return fmt.Errorf("create unique VM identity: %w", err)
	}
	vmName := "ss-vsock-bulk-" + suffix
	owner = studioruntime.OwnedVM{Name: vmName, Labels: map[string]string{
		"studio.kind":         "spike",
		"studio.spike":        "vsock-bulk",
		"studio.spike-owner":  suffix,
		"studio.sandbox-name": vmName,
	}}
	if err := guardUnprotected(owner.Name); err != nil {
		return err
	}

	guestDir := filepath.Join(root, "guest-bin")
	if err := os.Mkdir(guestDir, 0o700); err != nil {
		return fmt.Errorf("create private guest binary directory: %w", err)
	}
	guestBinary := filepath.Join(guestDir, guestBinaryName)
	if err := copyGuestBinary(guestSource, guestBinary); err != nil {
		return err
	}

	listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen for private vsock route: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("protect private vsock socket: %w", err)
	}

	rt = studioruntime.New(studioruntime.Options{Image: baseImage})
	if err := persistRecoveryRecord(recoveryPath, owner); err != nil {
		return fmt.Errorf("persist exact VM recovery identity before creation: %w", err)
	}
	vmCreateAttempted = true
	vm, createErr, createPending := createSandboxBounded(ctx, owner.Name,
		msb.WithImage(baseImage),
		msb.WithPullPolicy(msb.PullPolicyNever),
		msb.WithCPUs(1),
		msb.WithMemory(512),
		msb.WithMaxMemory(512),
		msb.WithHostname(owner.Name),
		msb.WithDetached(),
		msb.WithNetwork(denyAll()),
		msb.WithMounts(map[string]msb.MountConfig{
			guestMount: msb.Mount.Bind(guestDir, msb.MountOptions{Readonly: true}),
		}),
		msb.WithVsock(msb.VsockRoute{HostSocket: socketPath, Port: vsockPort}),
		msb.WithLabels(owner.Labels),
	)
	if createPending {
		cleanupAllowed = false
		uncertainState = "VM creation remains inside the native SDK; this run cannot consume a handle returned after it exits"
		return fmt.Errorf("VM creation did not return before its deadline for %q", owner.Name)
	}
	if createErr != nil {
		cleanupAllowed = false
		uncertainState = "CreateSandbox returned an error, and this invocation cannot prove terminal VM state"
		return fmt.Errorf("create private cached-base VM: %w", createErr)
	}
	if vm == nil {
		cleanupAllowed = false
		uncertainState = "microsandbox returned no VM handle, so terminal VM state is unconfirmed"
		return errors.New("create private cached-base VM returned no handle")
	}
	if err := guardUnprotected(owner.Name); err != nil {
		cleanupAllowed = false
		uncertainState = "protected-name guard rejected the returned VM identity"
		return err
	}
	detachErr, detachPending := detachSandboxBounded(ctx, vm)
	if detachPending {
		cleanupAllowed = false
		uncertainState = "VM detach remains inside the native SDK"
		return fmt.Errorf("VM detach did not return before its deadline for %q", owner.Name)
	}
	if detachErr != nil {
		cleanupAllowed = false
		uncertainState = "VM detach failed, so ownership state is unconfirmed"
		return fmt.Errorf("detach private VM: %w", detachErr)
	}
	fmt.Printf("VM created: name=%s cpus=1 memory_mib=512 image=%s pull=never network=deny-all max_write_bytes=%d\n", owner.Name, baseImage, maxWrite)

	expectedHash := payloadHash()
	fmt.Printf("PAYLOAD bytes=%d sha256=%s modes=raw,yamux max_write_bytes=%d\n", payloadSize, expectedHash, maxWrite)

	raw, mayContinue := runMode(ctx, rt, owner, listener, "raw", expectedHash, maxWrite)
	printResult(raw)
	if !raw.ReceiverSettled {
		cleanupAllowed = false
		uncertainState = "raw host receiver did not settle"
	}
	if !mayContinue {
		return errors.Join(raw.Err, errors.New("yamux mode skipped because the raw native command exit was not confirmed or runtime cleanup remains pending"))
	}

	yamuxResult, _ := runMode(ctx, rt, owner, listener, "yamux", expectedHash, maxWrite)
	printResult(yamuxResult)
	if !yamuxResult.ReceiverSettled {
		cleanupAllowed = false
		uncertainState = "yamux host receiver did not settle"
	}
	if raw.Err != nil || yamuxResult.Err != nil {
		return errors.Join(raw.Err, yamuxResult.Err)
	}
	fmt.Println("PASS raw and yamux transfers verified exact byte count, SHA-256, EOF, and sent final ACK")
	return nil
}

func runMode(parent context.Context, rt *studioruntime.Runtime, owner studioruntime.OwnedVM, listener *net.UnixListener, mode, expectedHash string, maxWrite int) (modeResult, bool) {
	started := time.Now()
	result := modeResult{Mode: mode}
	modeCtx, cancel := context.WithTimeout(parent, modeDeadline)
	defer cancel()
	deadline, _ := modeCtx.Deadline()
	if err := listener.SetDeadline(deadline); err != nil {
		result.Err = fmt.Errorf("set %s accept deadline: %w", mode, err)
		return result, false
	}
	received := make(chan modeResult, 1)
	go func() {
		received <- receiveMode(modeCtx, listener, mode, expectedHash)
	}()

	if err := guardUnprotected(owner.Name); err != nil {
		result.Err = err
		listener.SetDeadline(time.Now())
		mergeReceiveResult(&result, <-received)
		listener.SetDeadline(time.Time{})
		return result, false
	}
	output := make(chan studioruntime.RunOutput, 16)
	var diagnostics diagnosticCapture
	stopOutput := make(chan struct{})
	outputDrained := make(chan struct{})
	go drainRunOutput(output, &diagnostics, stopOutput, outputDrained)
	runResult, runErr := rt.Run(modeCtx, owner, studioruntime.RunCommand{
		Path:    guestMount + "/" + guestBinaryName,
		Args:    []string{"-port", fmt.Sprint(vsockPort), "-max-write", fmt.Sprint(maxWrite), mode},
		User:    "root",
		Timeout: commandDeadline,
	}, output)
	if runErr != nil {
		result.Err = errors.Join(result.Err, fmt.Errorf("%s native command: %w", mode, runErr))
	}
	if !runResult.ExitCodeKnown {
		result.Err = errors.Join(result.Err, fmt.Errorf("%s native command exit code is unknown", mode))
	} else if runResult.ExitCode != 0 {
		result.Err = errors.Join(result.Err, fmt.Errorf("%s guest command exited with code %d", mode, runResult.ExitCode))
	}
	if runResult.OutputDropped {
		result.Err = errors.Join(result.Err, fmt.Errorf("%s guest diagnostic output was dropped", mode))
	}

	if rt.PendingRun() {
		waitRuntimeRunClear(parent, rt, pendingRunWait)
	}
	runPending := rt.PendingRun()
	mayContinue := runResult.ExitCodeKnown && !runPending
	if !runPending {
		// No command remains active, so an absent connection cannot arrive later.
		listener.SetDeadline(time.Now())
		close(stopOutput)
		<-outputDrained
	}
	diagnosticBytes, diagnosticTruncated := diagnostics.snapshot()
	printGuestDiagnostics(mode, diagnosticBytes, diagnosticTruncated || runResult.OutputDropped, runPending)

	select {
	case receivedResult := <-received:
		mergeReceiveResult(&result, receivedResult)
	case <-modeCtx.Done():
		select {
		case receivedResult := <-received:
			mergeReceiveResult(&result, receivedResult)
		case <-time.After(time.Second):
			result.Err = errors.Join(result.Err, fmt.Errorf("%s host receiver did not settle before its deadline", mode))
		}
	}
	if !result.ReceiverSettled {
		result.Err = errors.Join(result.Err, fmt.Errorf("%s host receiver remains unsettled", mode))
	}
	if !runPending {
		listener.SetDeadline(time.Time{})
	}
	if result.Duration == 0 {
		result.Duration = time.Since(started)
	}
	mayContinue = mayContinue && result.ReceiverSettled && !rt.PendingRun()
	return result, mayContinue
}

func drainRunOutput(output <-chan studioruntime.RunOutput, capture *diagnosticCapture, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case item := <-output:
			capture.add(item)
		case <-stop:
			for {
				select {
				case item := <-output:
					capture.add(item)
				default:
					return
				}
			}
		}
	}
}

func (c *diagnosticCapture) add(output studioruntime.RunOutput) {
	if len(output.Data) == 0 {
		return
	}
	prefix := []byte("stdout: ")
	if output.Stderr {
		prefix = []byte("stderr: ")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) > 0 {
		c.appendBounded([]byte("\n"))
	}
	c.appendBounded(prefix)
	c.appendBounded(output.Data)
}

func (c *diagnosticCapture) appendBounded(data []byte) {
	remaining := maxGuestOutput - len(c.data)
	if remaining <= 0 {
		if len(data) > 0 {
			c.truncated = true
		}
		return
	}
	if len(data) > remaining {
		data = data[:remaining]
		c.truncated = true
	}
	c.data = append(c.data, data...)
}

func (c *diagnosticCapture) snapshot() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.data...), c.truncated
}

func printGuestDiagnostics(mode string, data []byte, truncated, stillDraining bool) {
	fmt.Printf("DIAGNOSTIC mode=%s bytes=%d truncated=%t still_draining=%t text=%q\n",
		mode, len(data), truncated, stillDraining, data)
}

func receiveMode(ctx context.Context, listener *net.UnixListener, mode, expectedHash string) modeResult {
	started := time.Now()
	result := modeResult{Mode: mode}
	conn, err := listener.AcceptUnix()
	if err != nil {
		result.Duration = time.Since(started)
		result.Err = fmt.Errorf("accept %s transfer: %w", mode, err)
		result.ReceiverSettled = true
		return result
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			result.Err = fmt.Errorf("set %s connection deadline: %w", mode, err)
			result.Duration = time.Since(started)
			result.ReceiverSettled = true
			return result
		}
	}

	var stats transferStats
	var receiveErr error
	switch mode {
	case "raw":
		stats, receiveErr = receiveRaw(conn, mode, expectedHash)
	case "yamux":
		stats, receiveErr = receiveYamux(conn, mode, expectedHash)
	default:
		receiveErr = fmt.Errorf("unsupported receiver mode %q", mode)
	}
	result.Received = stats.Bytes
	result.Hash = stats.Hash
	result.Duration = time.Since(started)
	result.Err = receiveErr
	result.Verified = receiveErr == nil
	result.ReceiverSettled = true
	return result
}

func receiveRaw(conn net.Conn, mode, expectedHash string) (transferStats, error) {
	reader := bufio.NewReaderSize(conn, 4096)
	return receiveTransfer(reader, conn, mode, expectedHash)
}

func receiveYamux(conn net.Conn, mode, expectedHash string) (transferStats, error) {
	session, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		return transferStats{}, fmt.Errorf("start yamux server: %w", err)
	}
	defer session.Close()
	stream, err := session.Accept()
	if err != nil {
		return transferStats{}, fmt.Errorf("accept yamux bulk stream: %w", err)
	}
	defer stream.Close()
	reader := bufio.NewReaderSize(stream, 4096)
	return receiveTransfer(reader, stream, mode, expectedHash)
}

func receiveTransfer(reader *bufio.Reader, writer io.Writer, expectedMode, expectedHash string) (transferStats, error) {
	return receiveTransferSized(reader, writer, expectedMode, payloadSize, expectedHash)
}

func receiveTransferSized(reader *bufio.Reader, writer io.Writer, expectedMode string, expectedSize uint64, expectedHash string) (transferStats, error) {
	var header handshake
	headerLine, headerErr := reader.ReadSlice('\n')
	if errors.Is(headerErr, bufio.ErrBufferFull) || len(headerLine) > maxHandshakeBytes {
		headerErr = errors.New("handshake exceeds the 1024-byte limit")
	} else if headerErr == nil {
		headerErr = json.Unmarshal(headerLine, &header)
	}
	if headerErr == nil && (header.Mode != expectedMode || header.Size != expectedSize) {
		headerErr = fmt.Errorf("handshake announced mode=%q size=%d", header.Mode, header.Size)
	}

	stats := readExpectedPayload(reader, expectedSize, func(received, expected uint64) {
		fmt.Printf("PROGRESS mode=%s received=%d/%d\n", expectedMode, received, expected)
	})
	var verifyErr error
	if headerErr != nil {
		verifyErr = headerErr
	}
	if stats.Err != nil {
		verifyErr = errors.Join(verifyErr, fmt.Errorf("read payload after %d/%d bytes: %w", stats.Bytes, expectedSize, stats.Err))
	}
	if stats.Bytes != expectedSize {
		verifyErr = errors.Join(verifyErr, fmt.Errorf("received %d bytes; expected %d", stats.Bytes, expectedSize))
	}
	if stats.EOF {
		verifyErr = errors.Join(verifyErr, errors.New("guest closed its write side before the receipt ACK"))
	}
	if stats.Hash != expectedHash {
		verifyErr = errors.Join(verifyErr, fmt.Errorf("SHA-256 mismatch: got %s expected %s", stats.Hash, expectedHash))
	}

	if verifyErr != nil {
		verifyErr = errors.Join(verifyErr, writeTransferAck(writer, "nack", expectedMode, stats))
		if stats.Err != nil || stats.EOF {
			return stats, verifyErr
		}
		trailing, eof, tailErr := drainToEOF(reader)
		stats.EOF = eof
		if trailing > 0 {
			verifyErr = errors.Join(verifyErr, fmt.Errorf("received %d trailing bytes after the announced payload", trailing))
		}
		if tailErr != nil {
			stats.Err = tailErr
			verifyErr = errors.Join(verifyErr, fmt.Errorf("wait for EOF after rejected payload (%d bytes received): %w", stats.Bytes, tailErr))
		} else if !eof {
			verifyErr = errors.Join(verifyErr, errors.New("rejected payload stream ended without genuine EOF"))
		}
		return stats, verifyErr
	}
	if err := writeTransferAck(writer, "received", expectedMode, stats); err != nil {
		return stats, fmt.Errorf("write receipt acknowledgement: %w", err)
	}

	trailing, eof, tailErr := drainToEOF(reader)
	stats.EOF = eof
	if trailing > 0 {
		verifyErr = errors.Join(verifyErr, fmt.Errorf("received %d trailing bytes after the announced payload", trailing))
	}
	if tailErr != nil {
		stats.Err = tailErr
		verifyErr = errors.Join(verifyErr, fmt.Errorf("wait for genuine EOF after %d payload bytes: %w", stats.Bytes, tailErr))
	} else if !eof {
		verifyErr = errors.Join(verifyErr, errors.New("payload stream ended without genuine EOF"))
	}
	finalStatus := "ack"
	if verifyErr != nil {
		finalStatus = "nack"
	}
	if err := writeTransferAck(writer, finalStatus, expectedMode, stats); err != nil {
		verifyErr = errors.Join(verifyErr, fmt.Errorf("write final acknowledgement: %w", err))
	}
	return stats, verifyErr
}

func writeTransferAck(writer io.Writer, status, mode string, stats transferStats) error {
	return writeJSONLine(writer, acknowledgement{Status: status, Mode: mode, Size: stats.Bytes, SHA256: stats.Hash})
}

func readExpectedPayload(reader io.Reader, expectedSize uint64, reportProgress func(uint64, uint64)) transferStats {
	h := sha256.New()
	buf := make([]byte, readBufferSize)
	var count uint64
	nextProgress := progressInterval
	loggedFirst := false
	stats := transferStats{}
	for count < expectedSize {
		want := len(buf)
		if remaining := expectedSize - count; remaining < uint64(want) {
			want = int(remaining)
		}
		n, err := reader.Read(buf[:want])
		if n > 0 {
			_, _ = h.Write(buf[:n])
			count += uint64(n)
			if !loggedFirst || count >= nextProgress {
				if reportProgress != nil {
					reportProgress(count, expectedSize)
				}
				loggedFirst = true
				for nextProgress <= count {
					nextProgress += progressInterval
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				stats.EOF = true
			} else {
				stats.Err = err
			}
			break
		}
		if n == 0 {
			stats.Err = io.ErrNoProgress
			break
		}
	}
	stats.Bytes = count
	stats.Hash = hex.EncodeToString(h.Sum(nil))
	return stats
}

func drainToEOF(reader io.Reader) (uint64, bool, error) {
	buf := make([]byte, readBufferSize)
	var trailing uint64
	for {
		n, err := reader.Read(buf)
		trailing += uint64(n)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return trailing, true, nil
			}
			return trailing, false, err
		}
		if n == 0 {
			return trailing, false, io.ErrNoProgress
		}
	}
}

func writeJSONLine(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func mergeReceiveResult(dst *modeResult, received modeResult) {
	dst.Received = received.Received
	dst.Hash = received.Hash
	dst.Duration = received.Duration
	dst.Verified = received.Verified
	dst.ReceiverSettled = received.ReceiverSettled
	dst.Err = errors.Join(dst.Err, received.Err)
}

func printResult(result modeResult) {
	status := "FAIL"
	if result.Verified && result.Err == nil {
		status = "PASS"
	}
	errText := ""
	if result.Err != nil {
		errText = result.Err.Error()
	}
	fmt.Printf("RESULT mode=%s status=%s received=%d/%d sha256=%s duration=%s error=%q\n",
		result.Mode, status, result.Received, payloadSize, result.Hash, result.Duration.Round(time.Millisecond), errText)
}

func waitRuntimeRunClear(ctx context.Context, rt *studioruntime.Runtime, timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for rt.PendingRun() {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func createSandboxBounded(ctx context.Context, name string, options ...msb.SandboxOption) (*msb.Sandbox, error, bool) {
	type outcome struct {
		vm  *msb.Sandbox
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		vm, err := msb.CreateSandbox(ctx, name, options...)
		done <- outcome{vm: vm, err: err}
	}()
	select {
	case result := <-done:
		return result.vm, result.err, false
	case <-ctx.Done():
		return nil, ctx.Err(), true
	}
}

func detachSandboxBounded(ctx context.Context, vm *msb.Sandbox) (error, bool) {
	done := make(chan error, 1)
	go func() { done <- vm.Detach(ctx) }()
	select {
	case err := <-done:
		return err, false
	case <-ctx.Done():
		return ctx.Err(), true
	}
}

func removeOwnedBounded(ctx context.Context, rt *studioruntime.Runtime, owner studioruntime.OwnedVM) (error, bool) {
	done := make(chan error, 1)
	go func() { done <- rt.RemoveOwned(ctx, owner) }()
	select {
	case err := <-done:
		return err, false
	case <-ctx.Done():
		return ctx.Err(), true
	}
}

func persistRecoveryRecord(path string, owner studioruntime.OwnedVM) error {
	data, err := json.Marshal(recoveryRecord{Name: owner.Name, Labels: owner.Labels})
	if err != nil {
		return fmt.Errorf("encode exact VM owner: %w", err)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create exclusive mode-0600 recovery record: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set recovery record mode to 0600: %w", err)
	}
	for len(data) > 0 {
		n, writeErr := file.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if writeErr != nil {
			_ = file.Close()
			return fmt.Errorf("write recovery record: %w", writeErr)
		}
		if n == 0 {
			_ = file.Close()
			return errors.New("write recovery record: short write")
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync recovery record: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close recovery record: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open recovery record directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync recovery record directory: %w", err)
	}
	return nil
}

func copyGuestBinary(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open guest binary: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o500)
	if err != nil {
		return fmt.Errorf("create private guest binary copy: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("copy guest binary into private mount: %w", err)
	}
	return nil
}

func randomSuffix() (string, error) {
	var value [10]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value[:])), nil
}

func payloadHash() string {
	h := sha256.New()
	buf := make([]byte, readBufferSize)
	for offset := uint64(0); offset < payloadSize; {
		n := uint64(len(buf))
		if remaining := payloadSize - offset; n > remaining {
			n = remaining
		}
		fillPattern(buf[:n], offset)
		_, _ = h.Write(buf[:n])
		offset += n
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fillPattern(buf []byte, offset uint64) {
	for i := range buf {
		position := offset + uint64(i)
		buf[i] = byte(position*31 + position/256*17 + 0x5a)
	}
}

func denyAll() *msb.NetworkConfig {
	return &msb.NetworkConfig{DefaultEgress: msb.PolicyActionDeny, DefaultIngress: msb.PolicyActionDeny}
}

func guardUnprotected(name string) error {
	for _, protected := range protectedVMNames {
		if name == protected {
			return fmt.Errorf("refusing mutating operation for protected VM %q", name)
		}
	}
	return nil
}
