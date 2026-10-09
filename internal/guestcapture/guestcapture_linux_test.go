//go:build linux

package guestcapture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"golang.org/x/sys/unix"
)

type fakeWatchdog struct {
	mu             sync.Mutex
	readyErr       error
	releaseErr     error
	waitErr        error
	releaseCheck   func() error
	releaseStarted chan struct{}
	stop           bool
	waits          int
	released       bool
}

func (w *fakeWatchdog) waitReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.readyErr
}

func (w *fakeWatchdog) releaseAndWait(ctx context.Context) error {
	if w.releaseCheck != nil {
		if err := w.releaseCheck(); err != nil {
			return err
		}
	}
	if w.releaseStarted != nil {
		close(w.releaseStarted)
	}
	if w.releaseErr == nil {
		if w.waitErr != nil {
			return w.waitErr
		}
		w.mu.Lock()
		w.released = true
		w.mu.Unlock()
		return nil
	}
	if errors.Is(w.releaseErr, errWaitForCancel) {
		<-ctx.Done()
		return ctx.Err()
	}
	return w.releaseErr
}

func (w *fakeWatchdog) requestStop() {
	w.mu.Lock()
	w.stop = true
	w.mu.Unlock()
}

func (w *fakeWatchdog) wait() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waits++
	return w.waitErr
}

var errWaitForCancel = errors.New("wait for cancellation")

type fakeWatchdogRuntime struct {
	freezeErr error
	thawErr   error
	readyErr  error
	reason    string
	waitErr   error
	freezes   int
	thaws     int
	events    []watchdogEvent
}

func (r *fakeWatchdogRuntime) freeze() error {
	r.freezes++
	return r.freezeErr
}

func (r *fakeWatchdogRuntime) thaw() error {
	r.thaws++
	return r.thawErr
}

func (r *fakeWatchdogRuntime) send(event watchdogEvent) error {
	r.events = append(r.events, event)
	if event.Event == "ready" {
		return r.readyErr
	}
	return nil
}

func (r *fakeWatchdogRuntime) waitUntil(time.Time) (string, error) {
	return r.reason, r.waitErr
}

func TestWatchdogMachineThawsForReleaseCancelDisconnectAndTimeout(t *testing.T) {
	for _, reason := range []string{"release", "cancel", "owner-gone", "timeout", "signal"} {
		t.Run(reason, func(t *testing.T) {
			runtime := &fakeWatchdogRuntime{reason: reason}
			if err := runWatchdogMachine(runtime, time.Now); err != nil {
				t.Fatalf("run watchdog state machine: %v", err)
			}
			if runtime.freezes != 1 || runtime.thaws != 1 {
				t.Fatalf("freeze/thaw calls = %d/%d, want 1/1", runtime.freezes, runtime.thaws)
			}
			if len(runtime.events) != 2 || runtime.events[0].Event != "ready" || runtime.events[1].Event != "thawed" || runtime.events[1].Reason != reason || !runtime.events[1].Thawed {
				t.Fatalf("watchdog events = %+v", runtime.events)
			}
		})
	}
}

func TestWatchdogMachineThawsAfterUncertainFreezeAndFailedReady(t *testing.T) {
	t.Run("interrupted freeze", func(t *testing.T) {
		runtime := &fakeWatchdogRuntime{freezeErr: unix.EINTR}
		if err := runWatchdogMachine(runtime, time.Now); err == nil {
			t.Fatal("interrupted freeze succeeded")
		}
		if runtime.freezes != 1 || runtime.thaws != 1 || len(runtime.events) != 1 || runtime.events[0].Event != "error" {
			t.Fatalf("unexpected interrupted-freeze cleanup: freezes=%d thaws=%d events=%+v", runtime.freezes, runtime.thaws, runtime.events)
		}
	})

	t.Run("ready notification failed", func(t *testing.T) {
		runtime := &fakeWatchdogRuntime{readyErr: errors.New("parent disconnected")}
		if err := runWatchdogMachine(runtime, time.Now); err == nil {
			t.Fatal("failed ready notification succeeded")
		}
		if runtime.freezes != 1 || runtime.thaws != 1 {
			t.Fatalf("freeze/thaw calls = %d/%d, want 1/1", runtime.freezes, runtime.thaws)
		}
	})
}

func TestExportSuccessWaitsForNormalThawAck(t *testing.T) {
	files := testCaptureFiles(t)
	wd := &fakeWatchdog{}
	writerFinished := false
	var stream bytes.Buffer
	deps := fakeExportDeps(files, wd)
	wd.releaseCheck = func() error {
		if !writerFinished {
			return errors.New("release started before the raw tar writer finished")
		}
		return nil
	}
	deps.writeLayer = func(_ context.Context, dst io.Writer, _ *os.File, limits ocilayer.Limits) error {
		if limits != (ocilayer.Limits{}) {
			t.Fatalf("upperlayer limits = %+v, want zero-value limits", limits)
		}
		if _, err := io.WriteString(dst, "raw upper tar"); err != nil {
			return err
		}
		writerFinished = true
		return nil
	}
	wd.releaseErr = nil
	if err := exportWith(context.Background(), &stream, deps); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !writerFinished || !wd.released {
		t.Fatalf("writerFinished=%v released=%v", writerFinished, wd.released)
	}
	var raw bytes.Buffer
	if _, err := agentproto.ReadExport(context.Background(), &raw, bytes.NewReader(stream.Bytes()), 1<<20); err != nil {
		t.Fatalf("read export stream: %v", err)
	}
	if raw.String() != "raw upper tar" {
		t.Fatalf("export payload = %q", raw.String())
	}
}

func TestExportPreflightRefusalIsFramed(t *testing.T) {
	for _, stage := range []string{"lock", "discovery", "runner"} {
		t.Run(stage, func(t *testing.T) {
			var stream bytes.Buffer
			deps := fakeExportDeps(testCaptureFiles(t), &fakeWatchdog{})
			cause := errors.New(stage + " refused")
			switch stage {
			case "lock":
				deps.lock = func(context.Context) (*os.File, error) { return nil, cause }
			case "discovery":
				deps.discover = func() (*captureFiles, error) { return nil, cause }
			case "runner":
				deps.start = func(*os.File, *os.File) (watchdog, error) { return nil, cause }
			}
			err := exportWith(context.Background(), &stream, deps)
			if err == nil || !strings.Contains(err.Error(), cause.Error()) {
				t.Fatalf("export error = %v", err)
			}
			typ, payload, readErr := agentproto.ReadFrame(bytes.NewReader(stream.Bytes()))
			if readErr != nil || typ != agentproto.ExportFrameError || !strings.Contains(string(payload), cause.Error()) {
				t.Fatalf("refusal frame type=%d payload=%q err=%v", typ, payload, readErr)
			}
		})
	}
}

func TestExportReadinessFailureIsFramedAndCleansWatchdog(t *testing.T) {
	wd := &fakeWatchdog{readyErr: errors.New("malformed ready event")}
	var stream bytes.Buffer
	deps := fakeExportDeps(testCaptureFiles(t), wd)
	writerCalled := false
	deps.writeLayer = func(context.Context, io.Writer, *os.File, ocilayer.Limits) error {
		writerCalled = true
		return nil
	}
	err := exportWith(context.Background(), &stream, deps)
	if err == nil || !strings.Contains(err.Error(), "malformed ready event") {
		t.Fatalf("export error = %v", err)
	}
	if writerCalled || !wd.stop || wd.waits == 0 {
		t.Fatalf("writerCalled=%v stopped=%v waits=%d", writerCalled, wd.stop, wd.waits)
	}
	typ, _, readErr := agentproto.ReadFrame(bytes.NewReader(stream.Bytes()))
	if readErr != nil || typ != agentproto.ExportFrameError {
		t.Fatalf("refusal frame type=%d err=%v", typ, readErr)
	}
}

func TestExportDoesNotCompleteAfterTimeoutOrMalformedThawAck(t *testing.T) {
	for _, failure := range []string{"timeout", "malformed thaw ack", "helper exit failure"} {
		t.Run(failure, func(t *testing.T) {
			wd := &fakeWatchdog{releaseErr: errors.New(failure)}
			if failure == "helper exit failure" {
				wd.releaseErr = nil
				wd.waitErr = errors.New("helper exited unsuccessfully")
			}
			var stream bytes.Buffer
			deps := fakeExportDeps(testCaptureFiles(t), wd)
			deps.writeLayer = func(_ context.Context, dst io.Writer, _ *os.File, _ ocilayer.Limits) error {
				_, err := io.WriteString(dst, "raw tar bytes")
				return err
			}
			err := exportWith(context.Background(), &stream, deps)
			if err == nil {
				t.Fatal("export succeeded without verified normal thaw")
			}
			types := readFrameTypes(t, stream.Bytes())
			for _, typ := range types {
				if typ == agentproto.ExportFrameComplete {
					t.Fatalf("completion frame present after %s: %v", failure, types)
				}
			}
			if len(types) == 0 || types[len(types)-1] != agentproto.ExportFrameError {
				t.Fatalf("missing final error frame after %s: %v", failure, types)
			}
			if !wd.stop || wd.waits == 0 {
				t.Fatalf("helper cleanup missing after %s: stopped=%v waits=%d", failure, wd.stop, wd.waits)
			}
		})
	}
}

func TestExportCancelClosesPipeAndWatchdogControl(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd := &fakeWatchdog{releaseErr: errWaitForCancel, releaseStarted: make(chan struct{})}
	var stream bytes.Buffer
	deps := fakeExportDeps(testCaptureFiles(t), wd)
	deps.writeLayer = func(_ context.Context, dst io.Writer, _ *os.File, _ ocilayer.Limits) error {
		_, err := io.WriteString(dst, "raw tar bytes")
		return err
	}
	done := make(chan error, 1)
	go func() { done <- exportWith(ctx, &stream, deps) }()
	select {
	case <-wd.releaseStarted:
	case <-time.After(time.Second):
		t.Fatal("producer did not request normal release")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled export succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled export did not clean up")
	}
	if !wd.stop || wd.waits == 0 {
		t.Fatalf("helper cleanup missing after cancel: stopped=%v waits=%d", wd.stop, wd.waits)
	}
	for _, typ := range readFrameTypes(t, stream.Bytes()) {
		if typ == agentproto.ExportFrameComplete {
			t.Fatal("canceled export emitted a completion frame")
		}
	}
}

func TestDecodeWatchdogEventRejectsMalformedAck(t *testing.T) {
	for _, raw := range []string{
		`{"event":`,
		`{"event":"thawed","reason":"release","thawed":true,"unexpected":1}`,
		`{"event":"thawed","event":"ready","reason":"release","thawed":true}`,
		`{"event":"thawed","reason":"release","thawed":true} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if event, err := decodeWatchdogEvent([]byte(raw)); err == nil {
				t.Fatalf("malformed thaw acknowledgement was accepted: %+v", event)
			}
		})
	}
}

func TestReleaseAndWaitRejectsUnsuccessfulThawEvents(t *testing.T) {
	for _, raw := range []string{
		`{"event":"thawed","reason":"release","thawed":false}`,
		`{"event":"thawed","reason":"timeout","thawed":true}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := decodeWatchdogEvent([]byte(raw)); err != nil {
				t.Fatalf("test acknowledgement should be structurally valid: %v", err)
			}
			parent, helper := net.Pipe()
			defer parent.Close()
			defer helper.Close()
			wd := &processWatchdog{control: parent, ready: true}
			helperDone := make(chan error, 1)
			go func() {
				var command [1]byte
				if _, err := io.ReadFull(helper, command[:]); err != nil {
					helperDone <- err
					return
				}
				var commandErr error
				if command[0] != 'R' {
					commandErr = errors.New("parent did not request normal release")
				}
				_, writeErr := io.WriteString(helper, raw+"\n")
				helperDone <- errors.Join(commandErr, writeErr)
			}()

			if err := wd.releaseAndWait(context.Background()); err == nil {
				t.Fatal("releaseAndWait accepted an unsuccessful thaw event")
			}
			if err := <-helperDone; err != nil {
				t.Fatalf("fake helper exchange: %v", err)
			}
		})
	}
}

func TestWatchdogDeathAttemptsEmergencyThawOnlyAfterWait(t *testing.T) {
	root, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	exitErr := errors.New("watchdog was killed")
	var order []string
	wd := &processWatchdog{
		root: root,
		waitProcess: func() error {
			order = append(order, "wait")
			return exitErr
		},
		emergencyThaw: func(*os.File) error {
			order = append(order, "thaw")
			return nil
		},
	}
	if err := wd.wait(); !errors.Is(err, exitErr) {
		t.Fatalf("wait error = %v, want helper exit error", err)
	}
	if len(order) != 2 || order[0] != "wait" || order[1] != "thaw" {
		t.Fatalf("recovery order = %v, want helper wait before emergency thaw", order)
	}
}

func TestWatchdogDeathReportsFailedEmergencyThaw(t *testing.T) {
	root, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	exitErr := errors.New("watchdog was killed")
	thawErr := errors.New("emergency thaw failed")
	wd := &processWatchdog{
		root:        root,
		waitProcess: func() error { return exitErr },
		emergencyThaw: func(*os.File) error {
			return thawErr
		},
	}
	if err := wd.wait(); !errors.Is(err, exitErr) || !errors.Is(err, thawErr) {
		t.Fatalf("wait error = %v, want helper exit and thaw failures", err)
	}
}

func fakeExportDeps(files *captureFiles, wd *fakeWatchdog) exportDeps {
	return exportDeps{
		lock:     func(context.Context) (*os.File, error) { return os.Open(os.DevNull) },
		discover: func() (*captureFiles, error) { return files, nil },
		start:    func(*os.File, *os.File) (watchdog, error) { return wd, nil },
		writeLayer: func(_ context.Context, dst io.Writer, _ *os.File, _ ocilayer.Limits) error {
			_, err := io.WriteString(dst, "raw tar")
			return err
		},
	}
}

func TestInheritedCaptureLockRetainsFlockAfterParentClose(t *testing.T) {
	parent, err := os.CreateTemp(t.TempDir(), "capture-lock-")
	if err != nil {
		t.Fatal(err)
	}
	path := parent.Name()
	defer os.Remove(path)
	if err := unix.Flock(int(parent.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		parent.Close()
		t.Fatalf("lock parent capture file: %v", err)
	}

	// ExtraFiles carries the same open-file-description into exec; dup models
	// that inherited reference without starting a helper or freezing a filesystem.
	inheritedFD, err := unix.Dup(int(parent.Fd()))
	if err != nil {
		parent.Close()
		t.Fatalf("duplicate inherited lock descriptor: %v", err)
	}
	inherited := os.NewFile(uintptr(inheritedFD), "inherited-capture-lock")
	defer func() {
		if inherited != nil {
			_ = inherited.Close()
		}
	}()
	contender, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		parent.Close()
		t.Fatalf("open independent lock contender: %v", err)
	}
	defer contender.Close()

	if err := parent.Close(); err != nil {
		t.Fatalf("close original parent lock descriptor: %v", err)
	}
	err = unix.Flock(int(contender.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("lock after parent close = %v, want contention while inherited descriptor stays open", err)
	}

	if err := inherited.Close(); err != nil {
		t.Fatalf("close inherited helper lock descriptor: %v", err)
	}
	inherited = nil
	if err := unix.Flock(int(contender.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("lock after inherited descriptor closes: %v", err)
	}
}

func testCaptureFiles(t *testing.T) *captureFiles {
	t.Helper()
	root, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := os.Open(os.DevNull)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = root.Close()
		_ = upper.Close()
	})
	return &captureFiles{root: root, upper: upper}
}

func readFrameTypes(t *testing.T, raw []byte) []byte {
	t.Helper()
	r := bytes.NewReader(raw)
	var types []byte
	for {
		typ, _, err := agentproto.ReadFrame(r)
		if errors.Is(err, io.EOF) {
			return types
		}
		if err != nil {
			t.Fatalf("read export frame: %v", err)
		}
		types = append(types, typ)
	}
}
