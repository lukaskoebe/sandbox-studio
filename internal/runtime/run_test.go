package runtime

import (
	"context"
	"encoding/json"
	"errors"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

type fakeRunReply struct {
	event runEvent
	err   error
}

type fakeRunTrace struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeRunTrace) add(event string) {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
}

func (f *fakeRunTrace) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

type fakeRunBackend struct {
	mu      sync.Mutex
	vm      *fakeRunVM
	lookups int
}

func (f *fakeRunBackend) lookup(ctx context.Context, name string) (runVM, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.vm == nil || f.vm.name() != name {
		return nil, errOwnedVMNotFound
	}
	return f.vm, nil
}

type fakeRunVM struct {
	mu         sync.Mutex
	backend    *fakeRunBackend
	trace      *fakeRunTrace
	vmName     string
	vmID       string
	vmStatus   Status
	vmLabels   map[string]string
	connection *fakeRunConnection
	connectFn  func(context.Context) (runConnection, error)
	killErr    error
	removeErr  error
	onKill     func()
	kills      int
	removals   int
}

func (v *fakeRunVM) name() string { v.mu.Lock(); defer v.mu.Unlock(); return v.vmName }
func (v *fakeRunVM) id() string   { v.mu.Lock(); defer v.mu.Unlock(); return v.vmID }
func (v *fakeRunVM) status() Status {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.vmStatus
}
func (v *fakeRunVM) labels(context.Context) (map[string]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return cloneLabels(v.vmLabels), nil
}
func (v *fakeRunVM) connect(ctx context.Context) (runConnection, error) {
	if v.connectFn != nil {
		return v.connectFn(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return v.connection, nil
}
func (v *fakeRunVM) kill(context.Context) error {
	v.mu.Lock()
	v.kills++
	err := v.killErr
	if err == nil {
		v.vmStatus = StatusStopped
	}
	onKill := v.onKill
	v.mu.Unlock()
	v.trace.add("vm-kill")
	if onKill != nil {
		onKill()
	}
	return err
}
func (v *fakeRunVM) remove(context.Context) error {
	v.mu.Lock()
	v.removals++
	err := v.removeErr
	v.mu.Unlock()
	v.trace.add("vm-remove")
	if err != nil {
		return err
	}
	v.backend.mu.Lock()
	if v.backend.vm == v {
		v.backend.vm = nil
	}
	v.backend.mu.Unlock()
	return nil
}

type fakeRunConnection struct {
	vmID         string
	exec         *fakeRunExec
	execStreamFn func(context.Context, RunCommand) (runExec, error)
	streamErr    error
	streamArgs   RunCommand
	trace        *fakeRunTrace
	detachErr    error
	detachCalls  int
}

func (c *fakeRunConnection) id() string { return c.vmID }
func (c *fakeRunConnection) execStream(ctx context.Context, command RunCommand) (runExec, error) {
	c.streamArgs = command
	c.trace.add("exec-stream")
	if c.execStreamFn != nil {
		return c.execStreamFn(ctx, command)
	}
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.exec, nil
}
func (c *fakeRunConnection) detach(context.Context) error {
	c.detachCalls++
	c.trace.add("detach")
	return c.detachErr
}

type fakeRunExec struct {
	replies          chan fakeRunReply
	recvEntered      chan struct{}
	recvOnce         sync.Once
	recvActive       atomic.Int32
	closeCalls       int
	closeRaced       bool
	closeErr         error
	closeGate        chan struct{}
	closeEntered     chan struct{}
	closeOnce        sync.Once
	killErr          error
	doneOnKill       bool
	killGate         chan struct{}
	killEntered      chan struct{}
	killOnce         sync.Once
	killActive       atomic.Int32
	ignoreRecvCancel bool
	trace            *fakeRunTrace
	mu               sync.Mutex
}

func newFakeRunExec(trace *fakeRunTrace) *fakeRunExec {
	return &fakeRunExec{
		replies: make(chan fakeRunReply, 16), recvEntered: make(chan struct{}),
		closeEntered: make(chan struct{}), killEntered: make(chan struct{}), trace: trace,
	}
}

func (e *fakeRunExec) recv(ctx context.Context) (runEvent, error) {
	e.recvActive.Add(1)
	defer e.recvActive.Add(-1)
	e.recvOnce.Do(func() { close(e.recvEntered) })
	if e.ignoreRecvCancel {
		reply := <-e.replies
		return reply.event, reply.err
	}
	select {
	case reply := <-e.replies:
		return reply.event, reply.err
	case <-ctx.Done():
		return runEvent{}, ctx.Err()
	}
}

func (e *fakeRunExec) kill(context.Context) error {
	e.killActive.Add(1)
	defer e.killActive.Add(-1)
	e.trace.add("exec-kill")
	e.killOnce.Do(func() { close(e.killEntered) })
	e.mu.Lock()
	err := e.killErr
	done := e.doneOnKill
	gate := e.killGate
	e.mu.Unlock()
	if done {
		e.replies <- fakeRunReply{event: runEvent{kind: runEventDone}}
	}
	if gate != nil {
		<-gate
	}
	e.trace.add("exec-kill-return")
	return err
}

func (e *fakeRunExec) close() error {
	e.mu.Lock()
	e.closeCalls++
	if e.recvActive.Load() != 0 || e.killActive.Load() != 0 {
		e.closeRaced = true
	}
	err := e.closeErr
	gate := e.closeGate
	e.mu.Unlock()
	e.closeOnce.Do(func() { close(e.closeEntered) })
	e.trace.add("exec-close")
	if gate != nil {
		<-gate
	}
	return err
}

func newFakeRunRuntime(exec *fakeRunExec, labels map[string]string, limits runLimits) (*Runtime, *fakeRunBackend, *fakeRunVM, *fakeRunTrace) {
	trace := exec.trace
	conn := &fakeRunConnection{vmID: "run-vm-id", exec: exec, trace: trace}
	backend := &fakeRunBackend{}
	vm := &fakeRunVM{
		backend: backend, trace: trace, vmName: "build-job-1", vmID: "run-vm-id",
		vmStatus: StatusRunning, vmLabels: cloneLabels(labels), connection: conn,
	}
	backend.vm = vm
	runtime := New(Options{})
	runtime.runBackend = backend
	runtime.runLimits = limits
	return runtime, backend, vm, trace
}

func quickRunLimits() runLimits {
	return runLimits{
		startup: 20 * time.Millisecond, cleanup: 20 * time.Millisecond,
		cancelReturn: 50 * time.Millisecond, drain: 20 * time.Millisecond,
		outputChunk: 4,
	}
}

func fakeOwnedVM() OwnedVM {
	return OwnedVM{Name: "build-job-1", Labels: map[string]string{"studio.build-job": "job-1", "studio.kind": "build"}}
}

func TestRunDrainsSequentiallyAndClosesAfterDoneAndReceiverExit(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventStdout, data: []byte("stdout")}}
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventStderr, data: []byte("stderr")}}
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventExited, exitCode: 7}}
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventDone}}
	labels := map[string]string{"studio.build-job": "job-1", "studio.kind": "build", "extra": "kept"}
	runtime, _, vm, _ := newFakeRunRuntime(exec, labels, quickRunLimits())
	output := make(chan RunOutput, 2)
	command := RunCommand{Path: "/bin/sh", Args: []string{"-c", "exit 7"}, User: "agent", Cwd: "/workspace", Env: map[string]string{"HOME": "/home/agent"}, Timeout: time.Second}

	result, err := runtime.Run(context.Background(), fakeOwnedVM(), command, output)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ExitCodeKnown || result.ExitCode != 7 || result.CleanupPending {
		t.Fatalf("unexpected run result: %+v", result)
	}
	if got := vm.connection.streamArgs; got.Path != command.Path || got.User != command.User || got.Cwd != command.Cwd || got.Timeout != command.Timeout ||
		len(got.Args) != len(command.Args) || got.Args[0] != command.Args[0] || got.Env["HOME"] != command.Env["HOME"] {
		t.Fatalf("SDK command options were not forwarded: %+v", got)
	}
	first, second := <-output, <-output
	if first.Stderr || string(first.Data) != "stdo" || !second.Stderr || string(second.Data) != "stde" {
		t.Fatalf("unexpected bounded output: first=%+v second=%+v", first, second)
	}
	if !result.OutputDropped {
		t.Fatal("truncated output was not reported")
	}
	exec.mu.Lock()
	closeCalls, closeRaced := exec.closeCalls, exec.closeRaced
	exec.mu.Unlock()
	if closeCalls != 1 || closeRaced {
		t.Fatalf("Close calls=%d concurrent-with-Recv=%v", closeCalls, closeRaced)
	}
	events := trace.snapshot()
	closeIndex, detachIndex := eventIndex(events, "exec-close"), eventIndex(events, "detach")
	if closeIndex < 0 || detachIndex < 0 || closeIndex > detachIndex {
		t.Fatalf("cleanup ordering = %v, want Close before detach", events)
	}
	if runtime.PendingRun() {
		t.Fatal("Runtime retained a completed run slot")
	}
}

func TestRunCancellationKillsThenDrainsBeforeClose(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.doneOnKill = true
	runtime, _, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan runOutcome, 1)
	go func() {
		result, err := runtime.Run(ctx, fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil)
		resultCh <- runOutcome{result: result, err: err}
	}()
	<-exec.recvEntered
	cancel()
	select {
	case outcome := <-resultCh:
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", outcome.err)
		}
		if outcome.result.CleanupPending {
			t.Fatalf("cleanup unexpectedly pending: %+v", outcome.result)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not finish cancellation cleanup")
	}
	events := trace.snapshot()
	if eventIndex(events, "exec-kill") < 0 || eventIndex(events, "exec-close") < eventIndex(events, "exec-kill") {
		t.Fatalf("cancellation lifecycle order = %v", events)
	}
	vm.mu.Lock()
	kills := vm.kills
	vm.mu.Unlock()
	if kills != 0 {
		t.Fatalf("VM fallback kill count = %d, want exec kill and stream drain to suffice", kills)
	}
	if runtime.PendingRun() {
		t.Fatal("Runtime retained a completed canceled run slot")
	}
}

func TestRunImmediatelyAllowsNextSuccessfulRun(t *testing.T) {
	trace := &fakeRunTrace{}
	first := newFakeRunExec(trace)
	second := newFakeRunExec(trace)
	first.replies <- fakeRunReply{event: runEvent{kind: runEventDone}}
	second.replies <- fakeRunReply{event: runEvent{kind: runEventDone}}
	runtime, _, vm, _ := newFakeRunRuntime(first, fakeOwnedVM().Labels, quickRunLimits())
	var streams int
	vm.connection.execStreamFn = func(context.Context, RunCommand) (runExec, error) {
		streams++
		if streams == 1 {
			return first, nil
		}
		return second, nil
	}

	for index := 0; index < 2; index++ {
		result, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil)
		if err != nil || result.CleanupPending {
			t.Fatalf("Run %d result=%+v err=%v", index+1, result, err)
		}
	}
	if streams != 2 || runtime.PendingRun() {
		t.Fatalf("streams=%d pending=%v after consecutive successes", streams, runtime.PendingRun())
	}
}

func TestRunDropsOutputWithoutBlocking(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventStdout, data: []byte("large output")}}
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventDone}}
	runtime, _, _, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())

	result, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, make(chan RunOutput))
	if err != nil {
		t.Fatal(err)
	}
	if !result.OutputDropped || result.CleanupPending {
		t.Fatalf("unexpected blocked-output result: %+v", result)
	}
}

func TestRunQuarantinesRuntimeWhileRecvIsStalled(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.ignoreRecvCancel = true
	runtime, _, _, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan runOutcome, 1)
	go func() {
		result, err := runtime.Run(ctx, fakeOwnedVM(), RunCommand{Path: "/bin/sleep", Args: []string{"60"}}, nil)
		resultCh <- runOutcome{result: result, err: err}
	}()
	<-exec.recvEntered
	cancel()
	var outcome runOutcome
	select {
	case outcome = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its bounded cancellation drain")
	}
	if !errors.Is(outcome.err, context.Canceled) || !outcome.result.CleanupPending || !runtime.PendingRun() {
		t.Fatalf("stalled Recv was not quarantined: outcome=%+v pending=%v", outcome, runtime.PendingRun())
	}
	if _, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil); !errors.Is(err, ErrRunPending) {
		t.Fatalf("second Run error = %v, want ErrRunPending", err)
	}
	// Let the one receiver observe Done. The finalizer can then close safely and
	// the worker slot is released only after that late native call has returned.
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventDone}}
	waitForRunSlotRelease(t, runtime)
	exec.mu.Lock()
	closeCalls, closeRaced := exec.closeCalls, exec.closeRaced
	exec.mu.Unlock()
	if closeCalls != 1 || closeRaced {
		t.Fatalf("late cleanup Close calls=%d concurrent-with-Recv=%v", closeCalls, closeRaced)
	}
}

func TestRunQuarantinesRuntimeUntilLateCloseReturns(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.closeGate = make(chan struct{})
	exec.replies <- fakeRunReply{event: runEvent{kind: runEventDone}}
	runtime, _, _, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
	resultCh := make(chan runOutcome, 1)
	go func() {
		result, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil)
		resultCh <- runOutcome{result: result, err: err}
	}()
	<-exec.closeEntered
	var outcome runOutcome
	select {
	case outcome = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("Run did not return its bounded Close-pending result")
	}
	if !outcome.result.CleanupPending || !runtime.PendingRun() {
		t.Fatalf("late Close was not quarantined: outcome=%+v pending=%v", outcome, runtime.PendingRun())
	}
	if _, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil); !errors.Is(err, ErrRunPending) {
		t.Fatalf("second Run error = %v, want ErrRunPending", err)
	}
	close(exec.closeGate)
	waitForRunSlotRelease(t, runtime)
	events := trace.snapshot()
	if eventIndex(events, "exec-close") > eventIndex(events, "detach") {
		t.Fatalf("late cleanup ordering = %v, want detach after Close returns", events)
	}
}

func TestRunDoesNotCloseExecWhileNativeKillIsStillRunning(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.doneOnKill = true
	exec.killGate = make(chan struct{})
	runtime, _, _, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan runOutcome, 1)
	go func() {
		result, err := runtime.Run(ctx, fakeOwnedVM(), RunCommand{Path: "/bin/sleep", Args: []string{"60"}}, nil)
		resultCh <- runOutcome{result: result, err: err}
	}()
	<-exec.recvEntered
	cancel()
	<-exec.killEntered
	var outcome runOutcome
	select {
	case outcome = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("Run did not return its bounded pending result")
	}
	if !outcome.result.CleanupPending || !runtime.PendingRun() {
		t.Fatalf("blocked native Kill was not retained: outcome=%+v pending=%v", outcome, runtime.PendingRun())
	}
	exec.mu.Lock()
	closeCalls, closeRaced := exec.closeCalls, exec.closeRaced
	exec.mu.Unlock()
	if closeCalls != 0 || closeRaced {
		t.Fatalf("Close ran while Kill was still blocked: calls=%d raced=%v", closeCalls, closeRaced)
	}

	close(exec.killGate)
	waitForRunSlotRelease(t, runtime)
	exec.mu.Lock()
	closeCalls, closeRaced = exec.closeCalls, exec.closeRaced
	exec.mu.Unlock()
	if closeCalls != 1 || closeRaced {
		t.Fatalf("post-Kill Close calls=%d raced=%v", closeCalls, closeRaced)
	}
	events := trace.snapshot()
	if eventIndex(events, "exec-close") < eventIndex(events, "exec-kill-return") {
		t.Fatalf("Close did not follow native Kill return: %v", events)
	}
}

func TestRunClosesAndDetachesAfterRecvErrorWithoutDone(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.replies <- fakeRunReply{err: errors.New("stream reset")}
	runtime, _, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
	result, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil)
	if err == nil || !strings.Contains(err.Error(), "stream reset") {
		t.Fatalf("Run error = %v, want stream reset", err)
	}
	if result.CleanupPending || runtime.PendingRun() {
		t.Fatalf("terminal Recv error did not clean up: result=%+v pending=%v", result, runtime.PendingRun())
	}
	exec.mu.Lock()
	closeCalls, closeRaced := exec.closeCalls, exec.closeRaced
	exec.mu.Unlock()
	if closeCalls != 1 || closeRaced {
		t.Fatalf("Recv-error Close calls=%d raced=%v", closeCalls, closeRaced)
	}
	if eventIndex(trace.snapshot(), "detach") < 0 {
		t.Fatal("connection was not detached after terminal Recv error")
	}
	vm.mu.Lock()
	kills := vm.kills
	vm.mu.Unlock()
	if kills != 1 {
		t.Fatalf("VM fallback kills=%d, want 1 after Recv error without Done", kills)
	}
}

func TestRunEnforcesCommandTimeoutWithHostContext(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	exec.doneOnKill = true
	runtime, _, _, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
	result, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/sleep", Timeout: 10 * time.Millisecond}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want command deadline", err)
	}
	if result.CleanupPending || runtime.PendingRun() {
		t.Fatalf("timed-out command did not finish cleanup: result=%+v pending=%v", result, runtime.PendingRun())
	}
}

func TestRunStartupCancellationPreservesLostHandleUncertainty(t *testing.T) {
	trace := &fakeRunTrace{}
	exec := newFakeRunExec(trace)
	runtime, _, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
	conn := vm.connection
	entered := make(chan struct{})
	release := make(chan struct{})
	conn.execStreamFn = func(ctx context.Context, _ RunCommand) (runExec, error) {
		close(entered)
		<-release // models an FFI call that outlives context cancellation
		return nil, ctx.Err()
	}

	result, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil)
	if err == nil || !result.CleanupPending || !runtime.PendingRun() {
		t.Fatalf("stalled startup was not reported as pending: result=%+v err=%v pending=%v", result, err, runtime.PendingRun())
	}
	runtime.runMu.Lock()
	task := runtime.runSlots[fakeOwnedVM().Name]
	runtime.runMu.Unlock()
	if task == nil {
		t.Fatal("pending startup lost its Runtime task before native ExecStream returned")
	}
	<-entered
	waitForVMKill(t, vm)
	close(release)
	waitForRunEvent(t, trace, "detach")
	select {
	case <-task.settled:
	case <-time.After(time.Second):
		t.Fatal("lost-registration cleanup did not settle its known native work")
	}
	if !runtime.PendingRun() {
		t.Fatal("lost native registration was unquarantined after the known calls returned")
	}
}

func TestRunDetachesConnectionsOnEarlyErrors(t *testing.T) {
	t.Run("canceled connect returns a handle", func(t *testing.T) {
		trace := &fakeRunTrace{}
		exec := newFakeRunExec(trace)
		runtime, _, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
		vm.connectFn = func(ctx context.Context) (runConnection, error) {
			<-ctx.Done()
			return vm.connection, ctx.Err()
		}
		if _, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil); err == nil {
			t.Fatal("Run accepted canceled connection")
		}
		if vm.connection.detachCalls != 1 {
			t.Fatalf("detach calls=%d, want 1", vm.connection.detachCalls)
		}
	})

	t.Run("connect returns handle with error", func(t *testing.T) {
		trace := &fakeRunTrace{}
		exec := newFakeRunExec(trace)
		runtime, _, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
		vm.connectFn = func(context.Context) (runConnection, error) {
			return vm.connection, errors.New("connect failed")
		}
		if _, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil); err == nil {
			t.Fatal("Run accepted failed connection")
		}
		if vm.connection.detachCalls != 1 {
			t.Fatalf("detach calls=%d, want 1", vm.connection.detachCalls)
		}
	})

	t.Run("wrong connection identity", func(t *testing.T) {
		trace := &fakeRunTrace{}
		exec := newFakeRunExec(trace)
		runtime, _, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
		vm.connection.vmID = "different-vm"
		if _, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil); err == nil {
			t.Fatal("Run accepted a different connection identity")
		}
		if vm.connection.detachCalls != 1 {
			t.Fatalf("detach calls=%d, want 1", vm.connection.detachCalls)
		}
		vm.mu.Lock()
		kills := vm.kills
		vm.mu.Unlock()
		if kills != 0 {
			t.Fatalf("wrong-identity VM was killed %d times", kills)
		}
	})

	t.Run("exec startup error", func(t *testing.T) {
		trace := &fakeRunTrace{}
		exec := newFakeRunExec(trace)
		runtime, _, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
		vm.connection.execStreamFn = func(context.Context, RunCommand) (runExec, error) {
			return nil, errors.New("exec failed")
		}
		if _, err := runtime.Run(context.Background(), fakeOwnedVM(), RunCommand{Path: "/bin/true"}, nil); err == nil {
			t.Fatal("Run accepted failed ExecStream")
		}
		if vm.connection.detachCalls != 1 {
			t.Fatalf("detach calls=%d, want 1", vm.connection.detachCalls)
		}
	})
}

func TestExpectedLabelsMatchRequiresPresentEmptyLabels(t *testing.T) {
	if expectedLabelsMatch(map[string]string{"optional": ""}, map[string]string{}) {
		t.Fatal("missing empty-valued expected label matched")
	}
	if !expectedLabelsMatch(map[string]string{"optional": ""}, map[string]string{"optional": ""}) {
		t.Fatal("present empty-valued expected label did not match")
	}
}

func TestRemoveOwnedChecksAllLabelsAndExactIdentity(t *testing.T) {
	t.Run("label mismatch leaves VM alone", func(t *testing.T) {
		trace := &fakeRunTrace{}
		exec := newFakeRunExec(trace)
		runtime, _, vm, _ := newFakeRunRuntime(exec, map[string]string{"studio.build-job": "other", "studio.kind": "build"}, quickRunLimits())
		if err := runtime.RemoveOwned(context.Background(), fakeOwnedVM()); err == nil {
			t.Fatal("mismatched labels were accepted")
		}
		vm.mu.Lock()
		defer vm.mu.Unlock()
		if vm.kills != 0 || vm.removals != 0 {
			t.Fatalf("mismatched VM was modified: kills=%d removals=%d", vm.kills, vm.removals)
		}
	})

	t.Run("matching owner is killed removed and verified", func(t *testing.T) {
		trace := &fakeRunTrace{}
		exec := newFakeRunExec(trace)
		runtime, backend, vm, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
		if err := runtime.RemoveOwned(context.Background(), fakeOwnedVM()); err != nil {
			t.Fatal(err)
		}
		vm.mu.Lock()
		kills, removals := vm.kills, vm.removals
		vm.mu.Unlock()
		backend.mu.Lock()
		remaining := backend.vm
		backend.mu.Unlock()
		if kills != 1 || removals != 1 || remaining != nil {
			t.Fatalf("owned cleanup kills=%d removals=%d remaining=%v", kills, removals, remaining)
		}
	})

	t.Run("same-name replacement is preserved", func(t *testing.T) {
		trace := &fakeRunTrace{}
		exec := newFakeRunExec(trace)
		runtime, backend, original, _ := newFakeRunRuntime(exec, fakeOwnedVM().Labels, quickRunLimits())
		replacement := &fakeRunVM{
			backend: backend, trace: trace, vmName: "build-job-1", vmID: "replacement-id",
			vmStatus: StatusRunning, vmLabels: map[string]string{"studio.build-job": "job-1", "studio.kind": "build"},
		}
		original.onKill = func() {
			backend.mu.Lock()
			backend.vm = replacement
			backend.mu.Unlock()
		}
		if err := runtime.RemoveOwned(context.Background(), fakeOwnedVM()); err == nil {
			t.Fatal("same-name replacement was treated as the original VM")
		}
		backend.mu.Lock()
		remaining := backend.vm
		backend.mu.Unlock()
		if remaining != replacement || replacement.removals != 0 {
			t.Fatalf("replacement was modified: remaining=%p replacement=%p removals=%d", remaining, replacement, replacement.removals)
		}
	})
}

func TestRuntimeBaseReferenceAndTargetPlatform(t *testing.T) {
	runtime := New(Options{Image: "registry.example/base@sha256:abc"})
	if got := runtime.BaseReference(); got != "registry.example/base@sha256:abc" {
		t.Fatalf("BaseReference() = %q", got)
	}
	want := ""
	if goruntime.GOARCH == "amd64" || goruntime.GOARCH == "arm64" {
		want = "linux/" + goruntime.GOARCH
	}
	if got := runtime.TargetPlatform(); got != want {
		t.Fatalf("TargetPlatform() = %q, want %q", got, want)
	}
}

func waitForRunSlotRelease(t *testing.T, runtime *Runtime) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for runtime.PendingRun() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runtime.PendingRun() {
		t.Fatalf("run slot was not released before %s", deadline)
	}
}

func waitForRunEvent(t *testing.T, trace *fakeRunTrace, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if eventIndex(trace.snapshot(), want) >= 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("event %q was not recorded before %s; events=%v", want, deadline, trace.snapshot())
}

func waitForVMKill(t *testing.T, vm *fakeRunVM) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		vm.mu.Lock()
		kills := vm.kills
		vm.mu.Unlock()
		if kills > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("canceled startup did not request exact owned-VM fallback kill")
}

func eventIndex(events []string, want string) int {
	for index, event := range events {
		if event == want {
			return index
		}
	}
	return -1
}

const privateSourceTestEnv = "abcdefghijkl2"

func validPrivateSourceForTest() *ImageSource {
	return &ImageSource{
		Reference: "127.0.0.1:7880/studio/" + privateSourceTestEnv + "/base@sha256:" + strings.Repeat("a", 64),
		Username:  privateSourceTestEnv,
		Password:  "registry-secret-for-tests",
	}
}

func TestValidatePrivateImageSource(t *testing.T) {
	valid := validPrivateSourceForTest()
	if err := validatePrivateImageSource(*valid); err != nil {
		t.Fatalf("valid base source rejected: %v", err)
	}
	template := *valid
	template.Reference = "127.0.0.1:7880/studio/" + privateSourceTestEnv + "/mnopqrstuvwx2@sha256:" + strings.Repeat("b", 64)
	if err := validatePrivateImageSource(template); err != nil {
		t.Fatalf("valid template source rejected: %v", err)
	}

	tests := []struct {
		name   string
		change func(*ImageSource)
	}{
		{name: "missing password", change: func(source *ImageSource) { source.Password = "" }},
		{name: "username is not an environment id", change: func(source *ImageSource) { source.Username = "not-an-id" }},
		{name: "username does not match path scope", change: func(source *ImageSource) { source.Username = "mnopqrstuvwx2" }},
		{name: "non-loopback IPv4", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, "127.0.0.1", "192.0.2.1", 1)
		}},
		{name: "hostname registry", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, "127.0.0.1", "localhost", 1)
		}},
		{name: "IPv6 loopback", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, "127.0.0.1:7880", "[::1]:7880", 1)
		}},
		{name: "noncanonical IPv4", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, "127.0.0.1", "127.000.0.1", 1)
		}},
		{name: "missing port", change: func(source *ImageSource) { source.Reference = strings.Replace(source.Reference, ":7880", "", 1) }},
		{name: "noncanonical port", change: func(source *ImageSource) { source.Reference = strings.Replace(source.Reference, ":7880", ":07880", 1) }},
		{name: "out of range port", change: func(source *ImageSource) { source.Reference = strings.Replace(source.Reference, ":7880", ":65536", 1) }},
		{name: "scheme", change: func(source *ImageSource) { source.Reference = "http://" + source.Reference }},
		{name: "wrong route prefix", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, "/studio/", "/v2/studio/", 1)
		}},
		{name: "invalid path environment id", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, privateSourceTestEnv, "invalid", 1)
		}},
		{name: "wrong repository name", change: func(source *ImageSource) { source.Reference = strings.Replace(source.Reference, "/base@", "/Base@", 1) }},
		{name: "tagged image", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, "@sha256:", ":dev@sha256:", 1)
		}},
		{name: "uppercase digest", change: func(source *ImageSource) {
			source.Reference = strings.Replace(source.Reference, strings.Repeat("a", 64), strings.Repeat("A", 64), 1)
		}},
		{name: "query string", change: func(source *ImageSource) { source.Reference += "?token=hidden" }},
		{name: "extra path segment", change: func(source *ImageSource) { source.Reference += "/extra" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := validPrivateSourceForTest()
			test.change(source)
			err := validatePrivateImageSource(*source)
			if err == nil {
				t.Fatal("invalid private image source was accepted")
			}
			if (source.Password != "" && strings.Contains(err.Error(), source.Password)) || (source.Username != "" && strings.Contains(err.Error(), source.Username)) {
				t.Fatalf("validation error leaked credentials: %v", err)
			}
		})
	}
}

func TestPrivateImageSourceOptionsStayHostSide(t *testing.T) {
	options, err := privateImageSourceOptions(validPrivateSourceForTest())
	if err != nil {
		t.Fatalf("privateImageSourceOptions: %v", err)
	}
	config := msb.SandboxConfig{}
	for _, option := range options {
		option(&config)
	}
	if config.Image != validPrivateSourceForTest().Reference {
		t.Fatalf("SDK image option did not use the private reference")
	}
	if config.RegistryAuth == nil || config.RegistryAuth.Username != privateSourceTestEnv || config.RegistryAuth.Password != "registry-secret-for-tests" {
		t.Fatal("SDK registry auth option did not retain the private source credentials")
	}
	if !config.RegistryInsecure || config.PullPolicy != msb.PullPolicyIfMissing {
		t.Fatalf("SDK private image policy was not configured: insecure=%t pull=%q", config.RegistryInsecure, config.PullPolicy)
	}
	if len(config.Env) != 0 || len(config.Vsock) != 0 {
		t.Fatal("private registry credentials were added to guest env or vsock options")
	}

	imageJSON, err := json.Marshal(validPrivateSourceForTest())
	if err != nil {
		t.Fatal(err)
	}
	specJSON, err := json.Marshal(Spec{Image: validPrivateSourceForTest()})
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range [][]byte{imageJSON, specJSON} {
		if strings.Contains(string(encoded), "registry-secret-for-tests") || strings.Contains(string(encoded), "127.0.0.1:7880") {
			t.Fatalf("private image source was serialized: %s", encoded)
		}
	}

	defaultOptions, err := privateImageSourceOptions(nil)
	if err != nil || len(defaultOptions) != 0 {
		t.Fatalf("nil image source options = %d, %v; want no options", len(defaultOptions), err)
	}
}
