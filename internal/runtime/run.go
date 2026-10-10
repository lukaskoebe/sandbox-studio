package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	runStartupTimeout      = 30 * time.Second
	runCleanupTimeout      = 12 * time.Second
	runCancelReturnTimeout = 12 * time.Second
	runDrainTimeout        = 5 * time.Second
	runOutputChunkLimit    = 64 * 1024
	runVMKillTimeout       = 10 * time.Second
	runStdinChunk          = 256 * 1024
)

// ErrRunPending means this Runtime still owns a run in the same VM whose native
// SDK work has not finished. Call PendingRun before asking a worker to start
// another job.
var ErrRunPending = errors.New("runtime has an active or pending run")

var errOwnedVMNotFound = errors.New("owned VM not found")

// OwnedVM identifies a VM that the caller is authorized to run or remove. Every
// supplied label must match the SDK's current VM record before a command or
// cleanup operation is allowed.
type OwnedVM struct {
	Name   string
	Labels map[string]string
}

// RunCommand is a single command executed inside an owned VM.
//
// Stdin, when set, is streamed to the process's stdin, which is closed after
// Stdin returns io.EOF. A read error cancels the command. Read may outlive Run;
// the caller should make it return when it abandons the run.
//
// Stdout, when set, receives every stdout byte in order instead of the output
// channel. Run waits for each Write, so a slow writer slows the guest rather
// than dropping data. A Write error cancels the command and is returned by Run.
// Run cannot finish cleanup while a Write is blocked, so Write must return once
// the caller's context is done.
type RunCommand struct {
	Path    string
	Args    []string
	User    string
	Cwd     string
	Env     map[string]string
	Timeout time.Duration
	Stdin   io.Reader
	Stdout  io.Writer
}

// RunOutput is one stdout or stderr chunk. The caller owns the received bytes
// and must not close the output channel passed to Run.
type RunOutput struct {
	Stderr bool
	Data   []byte
}

// RunResult reports process exit and output state. ExitCodeKnown is false when
// the stream ended before an Exited event. CleanupPending means process or SDK
// cleanup was still pending when Run returned. PendingRun remains true only
// while late calls are active or cleanup is permanently uncertain, such as an
// ExecStream cancellation that may have registered a native handle but returned
// no Go handle.
type RunResult struct {
	ExitCode       int
	ExitCodeKnown  bool
	OutputDropped  bool
	CleanupPending bool
}

type runLimits struct {
	startup      time.Duration
	cleanup      time.Duration
	cancelReturn time.Duration
	drain        time.Duration
	outputChunk  int
}

func (r *Runtime) effectiveRunLimits() runLimits {
	limits := r.runLimits
	if limits.startup <= 0 {
		limits.startup = runStartupTimeout
	}
	if limits.cleanup <= 0 {
		limits.cleanup = runCleanupTimeout
	}
	if limits.cancelReturn <= 0 {
		limits.cancelReturn = runCancelReturnTimeout
	}
	if limits.drain <= 0 {
		limits.drain = runDrainTimeout
	}
	if limits.outputChunk <= 0 {
		limits.outputChunk = runOutputChunkLimit
	}
	return limits
}

type runBackend interface {
	lookup(context.Context, string) (runVM, error)
}

type runVM interface {
	name() string
	id() string
	status() Status
	labels(context.Context) (map[string]string, error)
	connect(context.Context) (runConnection, error)
	kill(context.Context) error
	remove(context.Context) error
}

type runConnection interface {
	id() string
	execStream(context.Context, RunCommand) (runExec, error)
	detach(context.Context) error
}

type runExec interface {
	recv(context.Context) (runEvent, error)
	kill(context.Context) error
	close() error
	// stdin returns the stdin pipe, or nil when the command has none.
	stdin() runStdin
}

// runStdin may overlap Recv and Kill, but not exec Close.
type runStdin interface {
	write(context.Context, []byte) error
	close() error
}

type runEventKind uint8

const (
	runEventOther runEventKind = iota
	runEventStdout
	runEventStderr
	runEventExited
	runEventFailed
	runEventStdinError
	runEventDone
)

type runEvent struct {
	kind     runEventKind
	data     []byte
	exitCode int
	message  string
}

type runOutcome struct {
	result RunResult
	err    error
}

type runTask struct {
	runtime *Runtime
	owned   OwnedVM
	command RunCommand
	output  chan<- RunOutput
	ctx     context.Context
	limits  runLimits

	result       chan runOutcome
	started      chan struct{}
	startedOnce  sync.Once
	settled      chan struct{}
	operationEnd chan struct{}
	activityMu   sync.Mutex
	activities   int
	activityCh   chan struct{}

	mu                sync.Mutex
	vm                runVM
	exec              runExec
	recvStarted       bool
	recvStartedSignal chan struct{}
	recvCancel        context.CancelFunc
	recvDone          chan struct{}
	stdinCancel       context.CancelFunc
	stdinDone         chan struct{}
	stdoutFailed      bool
	execKillSealed    bool
	execKillCalls     []<-chan struct{}
	exitCode          int
	exitCodeKnown     bool
	processErr        error
	streamErr         error
	cleanupErr        error
	sawDone           atomic.Bool
	outputDropped     atomic.Bool
	cleanupPending    bool
	quarantine        bool
	execKillStarted   bool
	vmKillStarted     bool
	vmKillFinished    bool
	detachOnce        sync.Once
}

// Run executes cmd in an already-running, exactly-owned VM. It uses a single
// sequential Recv loop. Output is nonblocking: a full channel drops chunks, and
// chunks larger than the bounded output size are truncated. A nil output channel
// discards output. The caller must not close a non-nil output channel.
// Command.Stdout replaces the channel for stdout with lossless, blocking writes.
// A nonzero process exit is returned in RunResult without a Go error. Runs in
// different VMs may overlap; a second run in the same VM gets ErrRunPending.
//
// Startup contexts request a bounded operation, but microsandbox's FFI may wait
// for a native call to return after cancellation. In particular, canceled
// ExecStream startup can race after native handle registration and return without
// exposing that handle to Go. Run kills the exact owned VM as a fallback and
// detaches any returned connection, but cannot prove the hidden registration is
// gone; it permanently quarantines the Runtime slot until process restart (or
// explicit SDK proof). Known late native calls and Close retain the slot only
// until they actually return. Command.Timeout also cancels host-side waits, but
// neither it nor startup/cleanup contexts impose a hard bound on a native FFI
// call that ignores cancellation.
func (r *Runtime) Run(ctx context.Context, owned OwnedVM, command RunCommand, output chan<- RunOutput) (RunResult, error) {
	if r == nil {
		return RunResult{}, errors.New("run command: nil runtime")
	}
	if ctx == nil {
		return RunResult{}, errors.New("run command: nil context")
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	if err := validateOwnedVM(owned); err != nil {
		return RunResult{}, err
	}
	if err := validateRunCommand(command); err != nil {
		return RunResult{}, err
	}

	owned.Labels = cloneLabels(owned.Labels)
	command.Args = append([]string(nil), command.Args...)
	command.Env = cloneLabels(command.Env)
	task := &runTask{
		runtime: r, owned: owned, command: command, output: output, ctx: ctx,
		limits: r.effectiveRunLimits(), result: make(chan runOutcome, 1),
		started: make(chan struct{}), settled: make(chan struct{}), operationEnd: make(chan struct{}),
		recvStartedSignal: make(chan struct{}), activityCh: make(chan struct{}),
	}
	r.runMu.Lock()
	if r.runSlots[owned.Name] != nil {
		r.runMu.Unlock()
		return RunResult{}, ErrRunPending
	}
	if r.runSlots == nil {
		r.runSlots = make(map[string]*runTask)
	}
	r.runSlots[owned.Name] = task
	r.runMu.Unlock()

	go task.execute()
	startupTimer := time.NewTimer(task.limits.startup + task.limits.cleanup)
	defer startupTimer.Stop()
	startupWait := (<-chan time.Time)(startupTimer.C)
	startedWait := (<-chan struct{})(task.started)
	for {
		select {
		case outcome := <-task.result:
			return outcome.result, outcome.err
		case <-startedWait:
			startupWait = nil
			startedWait = nil
		case <-startupWait:
			select {
			case <-task.started:
				startupWait = nil
				startedWait = nil
				continue
			default:
			}
			return task.pendingOutcome(errors.New("run startup is still inside a native SDK call"))
		case <-ctx.Done():
			timer := time.NewTimer(task.limits.cancelReturn)
			select {
			case outcome := <-task.result:
				timer.Stop()
				return outcome.result, outcome.err
			case <-timer.C:
				return task.pendingOutcome(ctx.Err())
			}
		}
	}
}

func (t *runTask) pendingOutcome(cause error) (RunResult, error) {
	result := t.currentResult()
	result.CleanupPending = true
	if cause == nil {
		cause = errors.New("run cleanup is still pending")
	}
	return result, fmt.Errorf("run %q cleanup pending: %w", t.owned.Name, cause)
}

func (t *runTask) execute() {
	defer close(t.settled)
	// This watcher can request process termination while ExecStream or Recv is
	// blocked in an FFI call. The call itself may still outlive its context.
	t.goTracked(func() {
		select {
		case <-t.ctx.Done():
			select {
			case <-t.operationEnd:
				return
			default:
				t.requestCancellation()
			}
		case <-t.operationEnd:
		}
	})

	outcome := t.runSession()
	close(t.operationEnd)
	reportedEarly := outcome.result.CleanupPending
	if reportedEarly {
		t.report(outcome)
	}
	if !reportedEarly && !t.waitActivitiesFor(t.limits.cleanup) {
		t.markCleanupPending(errors.New("native SDK cleanup is still active"))
		outcome.result.CleanupPending = true
		outcome.err = errors.Join(outcome.err, t.combinedError())
		reportedEarly = true
		t.report(outcome)
	}
	t.waitActivities()
	if !reportedEarly {
		state := t.currentResult()
		if state.CleanupPending {
			outcome.result.CleanupPending = true
			outcome.err = errors.Join(outcome.err, t.combinedError())
		}
	}
	t.mu.Lock()
	quarantined := t.quarantine
	t.mu.Unlock()
	if !quarantined {
		t.runtime.runMu.Lock()
		if t.runtime.runSlots[t.owned.Name] == t {
			delete(t.runtime.runSlots, t.owned.Name)
		}
		t.runtime.runMu.Unlock()
	}
	if !reportedEarly {
		t.report(outcome)
	}
}

func (t *runTask) report(outcome runOutcome) {
	select {
	case t.result <- outcome:
	default:
	}
}

func (t *runTask) markStarted() { t.startedOnce.Do(func() { close(t.started) }) }

func (t *runTask) runSession() runOutcome {
	backend := t.runtime.runBackend
	if backend == nil {
		backend = sdkRunBackend{}
	}
	startupCtx, cancelStartup := context.WithTimeout(t.ctx, t.limits.startup)
	startupDone := make(chan struct{})
	var finishStartupOnce sync.Once
	finishStartup := func() {
		finishStartupOnce.Do(func() {
			close(startupDone)
			cancelStartup()
		})
	}
	t.goTracked(func() {
		select {
		case <-startupCtx.Done():
			select {
			case <-startupDone:
				return
			default:
				t.requestCancellation()
			}
		case <-startupDone:
		}
	})
	defer finishStartup()

	vm, err := backend.lookup(startupCtx, t.owned.Name)
	if err != nil {
		t.markStarted()
		return runOutcome{err: fmt.Errorf("lookup owned VM %q: %w", t.owned.Name, err)}
	}
	if err := validateRunOwnership(startupCtx, vm, t.owned); err != nil {
		t.markStarted()
		return runOutcome{err: err}
	}
	t.mu.Lock()
	t.vm = vm
	t.mu.Unlock()
	if startupCtx.Err() != nil || t.ctx.Err() != nil {
		t.requestCancellation()
		t.markStarted()
		cause := startupCtx.Err()
		if cause == nil {
			cause = t.ctx.Err()
		}
		return runOutcome{result: RunResult{CleanupPending: true}, err: fmt.Errorf("owned VM %q was canceled during startup: %w", t.owned.Name, cause)}
	}
	if !vm.status().IsRunning() {
		t.markStarted()
		return runOutcome{err: fmt.Errorf("run command in VM %q: state is %q, want running", t.owned.Name, vm.status())}
	}

	conn, err := vm.connect(startupCtx)
	if err != nil {
		startupErr := startupCtx.Err()
		finishStartup()
		t.markStarted()
		if conn != nil {
			t.detachConnection(conn)
		}
		if startupErr != nil || t.ctx.Err() != nil {
			t.requestCancellation()
			return runOutcome{result: RunResult{CleanupPending: true}, err: fmt.Errorf("connect owned VM %q during startup; VM cleanup requested: %w", t.owned.Name, err)}
		}
		return runOutcome{err: fmt.Errorf("connect owned VM %q: %w", t.owned.Name, err)}
	}
	if conn == nil || conn.id() == "" || conn.id() != vm.id() {
		finishStartup()
		t.markStarted()
		if conn != nil {
			t.detachConnection(conn)
		}
		return runOutcome{err: fmt.Errorf("connect owned VM %q: SDK returned a different VM identity", t.owned.Name)}
	}
	var commandCtx context.Context
	var cancelCommand context.CancelFunc
	if t.command.Timeout > 0 {
		commandCtx, cancelCommand = context.WithTimeout(t.ctx, t.command.Timeout)
	} else {
		commandCtx, cancelCommand = context.WithCancel(t.ctx)
	}
	commandWatcherDone := make(chan struct{})
	t.goTracked(func() {
		select {
		case <-commandCtx.Done():
			select {
			case <-commandWatcherDone:
				return
			default:
				t.requestCancellation()
			}
		case <-commandWatcherDone:
		}
	})
	defer func() {
		close(commandWatcherDone)
		cancelCommand()
	}()
	stopStartupOnCommandEnd := context.AfterFunc(commandCtx, cancelStartup)
	defer stopStartupOnCommandEnd()
	exec, err := conn.execStream(startupCtx, t.command)
	startupErr := startupCtx.Err()
	commandErr := commandCtx.Err()
	finishStartup()
	t.markStarted()
	if err != nil {
		t.detachConnection(conn)
		if commandErr != nil || t.ctx.Err() != nil || startupErr != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			t.markQuarantined(errors.New("canceled ExecStream may have registered a native exec handle that was not returned to Go"))
			t.requestCancellation()
			return runOutcome{
				result: RunResult{CleanupPending: true},
				err:    fmt.Errorf("start command in owned VM %q; SDK cancellation may have lost a registered exec handle: %w", t.owned.Name, err),
			}
		}
		return runOutcome{result: t.currentResult(), err: fmt.Errorf("start command in owned VM %q: %w", t.owned.Name, err)}
	}
	if exec == nil {
		t.detachConnection(conn)
		t.markQuarantined(errors.New("SDK returned no exec handle after command startup"))
		t.requestCancellation()
		return runOutcome{result: RunResult{CleanupPending: true}, err: errors.New("start command: SDK returned an empty exec handle")}
	}
	t.mu.Lock()
	t.exec = exec
	t.mu.Unlock()
	if t.command.Stdin != nil {
		if sink := exec.stdin(); sink != nil {
			stdinCtx, cancelStdin := context.WithCancel(context.Background())
			t.stdinCancel, t.stdinDone = cancelStdin, make(chan struct{})
			t.goTracked(func() { t.pumpStdin(stdinCtx, sink) })
		} else {
			t.setProcessError(errors.New("SDK returned no stdin pipe"))
			t.requestCancellation()
		}
	}

	recvEvents := make(chan runEvent, 8)
	recvDone := make(chan struct{})
	finalizerDone := make(chan struct{})
	recvCtx, cancelRecv := context.WithCancel(context.Background())
	t.mu.Lock()
	t.recvStarted = true
	close(t.recvStartedSignal)
	t.recvCancel = cancelRecv
	t.recvDone = recvDone
	vmKillFinished := t.vmKillFinished
	t.mu.Unlock()
	if vmKillFinished {
		cancelRecv()
	}
	t.goTracked(func() { t.receive(exec, recvCtx, cancelRecv, recvEvents, recvDone) })
	t.goTracked(func() { t.finalize(exec, conn, recvDone, finalizerDone) })
	if commandCtx.Err() != nil || startupErr != nil {
		t.requestCancellation()
	}

	for {
		select {
		case event := <-recvEvents:
			switch event.kind {
			case runEventDone:
				return t.finishOutcome(finalizerDone, commandCtx.Err())
			case runEventFailed, runEventStdinError:
				t.setProcessError(errors.New(nonEmpty(event.message, "guest command reported an exec failure")))
			case runEventOther:
				if event.message != "" {
					t.requestCancellation()
					return t.finishOutcome(finalizerDone, commandCtx.Err())
				}
			}
		case <-commandCtx.Done():
			t.requestCancellation()
			timer := time.NewTimer(t.limits.drain)
			for {
				select {
				case event := <-recvEvents:
					if event.kind == runEventDone {
						timer.Stop()
						return t.finishOutcome(finalizerDone, commandCtx.Err())
					}
					if event.kind == runEventOther && event.message != "" {
						timer.Stop()
						t.requestCancellation()
						return t.finishOutcome(finalizerDone, commandCtx.Err())
					}
				case <-timer.C:
					return runOutcome{result: t.currentResultWithPending(), err: commandCtx.Err()}
				}
			}
		}
	}
}

func (t *runTask) receive(exec runExec, ctx context.Context, cancel context.CancelFunc, events chan<- runEvent, recvDone chan<- struct{}) {
	defer close(recvDone)
	defer cancel()
	for {
		event, err := exec.recv(ctx)
		if err != nil {
			t.setStreamError(err)
			// Recv has returned before this Kill begins, and finalization seals
			// further Kill scheduling after recvDone, so Close cannot race it.
			t.requestCancellation()
			select {
			case events <- runEvent{kind: runEventOther, message: err.Error()}:
			default:
			}
			return
		}
		switch event.kind {
		case runEventStdout:
			if t.command.Stdout != nil {
				t.writeStdout(event.data)
			} else {
				t.emitOutput(false, event.data)
			}
		case runEventStderr:
			t.emitOutput(true, event.data)
		case runEventExited:
			t.mu.Lock()
			t.exitCode, t.exitCodeKnown = event.exitCode, true
			t.mu.Unlock()
		case runEventFailed:
			t.setProcessError(errors.New(nonEmpty(event.message, "guest command failed to start")))
		case runEventStdinError:
			t.setProcessError(errors.New(nonEmpty(event.message, "guest command stdin failed")))
		case runEventDone:
			t.sawDone.Store(true)
			select {
			case events <- runEvent{kind: runEventDone}:
			default:
			}
			return
		}
	}
}

func (t *runTask) finalize(exec runExec, conn runConnection, recvDone <-chan struct{}, finalizerDone chan<- struct{}) {
	defer close(finalizerDone)
	<-recvDone
	// Recv has returned, so Close cannot race it. Seal exec Kill scheduling and
	// wait for every native Kill call before Close; the SDK only permits Kill to
	// overlap Recv, and Close cannot overlap Kill.
	t.mu.Lock()
	t.execKillSealed = true
	killCalls := append([]<-chan struct{}(nil), t.execKillCalls...)
	t.mu.Unlock()
	for _, done := range killCalls {
		<-done
	}
	// Stdin writes may overlap Recv and Kill, but not Close.
	if t.stdinDone != nil {
		t.stdinCancel()
		<-t.stdinDone
	}
	closeResult, closeDone := t.startCall(exec.close)
	closeTimer := time.NewTimer(t.limits.cleanup)
	var closeErr error
	select {
	case closeErr = <-closeResult:
		closeTimer.Stop()
	case <-closeTimer.C:
		t.markCleanupPending(errors.New("exec handle Close is still inside a native SDK call"))
		// Close is contextless. Keep the Runtime quarantined until the native
		// call returns, then detach the sandbox connection in order.
		<-closeDone
		closeErr = <-closeResult
	}
	if closeErr != nil {
		t.markQuarantined(fmt.Errorf("close exec handle: %w", closeErr))
	}
	t.detachConnection(conn)
}

func (t *runTask) finishOutcome(finalizerDone <-chan struct{}, cause error) runOutcome {
	timer := time.NewTimer(t.limits.cleanup)
	defer timer.Stop()
	select {
	case <-finalizerDone:
	case <-timer.C:
		t.markCleanupPending(errors.New("exec receiver or cleanup has not finished"))
		return runOutcome{result: t.currentResultWithPending(), err: errors.Join(cause, t.combinedError())}
	}
	result := t.currentResult()
	cleanupErr := t.combinedError()
	if cleanupErr != nil {
		result.CleanupPending = true
	}
	return runOutcome{result: result, err: errors.Join(cause, t.processError(), t.streamError(), cleanupErr)}
}

func (t *runTask) requestCancellation() {
	if t.sawDone.Load() {
		return
	}
	t.mu.Lock()
	exec := t.exec
	vm := t.vm
	var killResult <-chan error
	var killDone <-chan struct{}
	startExecKill := exec != nil && !t.execKillStarted && !t.execKillSealed
	if startExecKill {
		t.execKillStarted = true
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), t.limits.cleanup)
		killResult, killDone = t.startCall(func() error {
			defer cancel()
			return exec.kill(cleanupCtx)
		})
		t.execKillCalls = append(t.execKillCalls, killDone)
	}
	startVMKill := exec == nil && vm != nil && !t.vmKillStarted
	if startVMKill {
		t.vmKillStarted = true
	}
	t.mu.Unlock()
	if startExecKill {
		t.goTracked(func() { t.killExecAndDrain(vm, killResult) })
	} else if startVMKill {
		t.goTracked(func() { t.performVMKill(vm) })
	}
}

func (t *runTask) killExecAndDrain(vm runVM, result <-chan error) {
	timer := time.NewTimer(t.limits.cleanup)
	var killErr error
	completed := false
	select {
	case killErr = <-result:
		completed = true
		timer.Stop()
	case <-timer.C:
		t.markCleanupPending(errors.New("exec Kill is still inside a native SDK call"))
	}
	if !completed || killErr != nil {
		t.startVMKill(vm)
		return
	}
	t.mu.Lock()
	recvStarted := t.recvStarted
	recvDone := t.recvDone
	t.mu.Unlock()
	if !recvStarted {
		timer := time.NewTimer(t.limits.drain)
		defer timer.Stop()
		select {
		case <-t.receiverStarted():
			t.mu.Lock()
			recvDone = t.recvDone
			t.mu.Unlock()
			if recvDone == nil {
				t.startVMKill(vm)
				return
			}
		case <-timer.C:
			t.startVMKill(vm)
			return
		}
	}
	if recvDone == nil {
		t.startVMKill(vm)
		return
	}
	timer = time.NewTimer(t.limits.drain)
	defer timer.Stop()
	select {
	case <-recvDone:
		if !t.sawDone.Load() {
			t.startVMKill(vm)
		}
	case <-timer.C:
		t.startVMKill(vm)
	}
}

func (t *runTask) receiverStarted() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.recvStarted {
		ready := make(chan struct{})
		close(ready)
		return ready
	}
	return t.recvStartedSignal
}

func (t *runTask) startVMKill(vm runVM) {
	if vm == nil {
		return
	}
	t.mu.Lock()
	if t.vmKillStarted {
		finished := t.vmKillFinished
		t.mu.Unlock()
		if finished {
			t.cancelReceiver()
		}
		return
	}
	t.vmKillStarted = true
	t.mu.Unlock()
	t.goTracked(func() { t.performVMKill(vm) })
}

func (t *runTask) performVMKill(vm runVM) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), runVMKillTimeout)
	result, done := t.startCall(func() error { return vm.kill(cleanupCtx) })
	timer := time.NewTimer(runVMKillTimeout)
	var err error
	completed := false
	select {
	case err = <-result:
		completed = true
		timer.Stop()
	case <-timer.C:
		t.markCleanupPending(errors.New("owned VM kill is still inside a native SDK call"))
		t.goTracked(func() {
			<-done
			if lateErr := <-result; lateErr != nil {
				t.markQuarantined(fmt.Errorf("kill owned VM: %w", lateErr))
			}
		})
	}
	cancel()
	if completed && err != nil {
		t.markQuarantined(fmt.Errorf("kill owned VM: %w", err))
	}
	t.mu.Lock()
	t.vmKillFinished = true
	t.mu.Unlock()
	// A VM kill is the cancellation fallback after process Kill/drain. Cancel the
	// independent receiver only once that fallback has at least returned or
	// reached its native-call bound.
	t.cancelReceiver()
}

func (t *runTask) cancelReceiver() {
	t.mu.Lock()
	cancel := t.recvCancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (t *runTask) startCall(call func() error) (<-chan error, <-chan struct{}) {
	result := make(chan error, 1)
	done := make(chan struct{})
	t.goTracked(func() {
		defer close(done)
		result <- call()
	})
	return result, done
}

func (t *runTask) detachConnection(conn runConnection) {
	if conn == nil {
		return
	}
	t.detachOnce.Do(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), t.limits.cleanup)
		result, done := t.startCall(func() error { return conn.detach(cleanupCtx) })
		timer := time.NewTimer(t.limits.cleanup)
		var err error
		completed := false
		select {
		case err = <-result:
			completed = true
			timer.Stop()
		case <-timer.C:
			t.markCleanupPending(errors.New("sandbox detach is still inside a native SDK call"))
			t.goTracked(func() {
				<-done
				if lateErr := <-result; lateErr != nil {
					t.markQuarantined(fmt.Errorf("detach owned VM: %w", lateErr))
				}
			})
		}
		cancel()
		if completed && err != nil {
			t.markQuarantined(fmt.Errorf("detach owned VM: %w", err))
		}
	})
}

func (t *runTask) goTracked(fn func()) {
	t.activityMu.Lock()
	t.activities++
	t.activityMu.Unlock()
	go func() {
		defer func() {
			t.activityMu.Lock()
			t.activities--
			close(t.activityCh)
			t.activityCh = make(chan struct{})
			t.activityMu.Unlock()
		}()
		fn()
	}()
}

// waitActivities is called only after runSession has stopped creating work. Any
// tracked activity that can create a child activity remains counted until after
// that child is registered, so the count cannot reach zero prematurely.
func (t *runTask) waitActivities() {
	for {
		t.activityMu.Lock()
		if t.activities == 0 {
			t.activityMu.Unlock()
			return
		}
		changed := t.activityCh
		t.activityMu.Unlock()
		<-changed
	}
}

func (t *runTask) waitActivitiesFor(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		t.activityMu.Lock()
		if t.activities == 0 {
			t.activityMu.Unlock()
			return true
		}
		changed := t.activityCh
		t.activityMu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return false
		}
	}
}

func (t *runTask) emitOutput(stderr bool, data []byte) {
	if t.output == nil || len(data) == 0 {
		return
	}
	if len(data) > t.limits.outputChunk {
		data = data[:t.limits.outputChunk]
		t.outputDropped.Store(true)
	}
	copyOfData := append([]byte(nil), data...)
	select {
	case t.output <- RunOutput{Stderr: stderr, Data: copyOfData}:
	default:
		t.outputDropped.Store(true)
	}
}

// writeStdout runs only on the receiver goroutine. Blocking here also stops Recv,
// which is the backpressure toward the guest.
func (t *runTask) writeStdout(data []byte) {
	if t.stdoutFailed || len(data) == 0 {
		return
	}
	if _, err := t.command.Stdout.Write(data); err != nil {
		t.stdoutFailed = true
		t.setProcessError(fmt.Errorf("write command stdout: %w", err))
		t.requestCancellation()
	}
}

// pumpStdin copies Command.Stdin to the guest and closes the pipe at EOF. The
// caller's Read runs in an untracked goroutine because it calls no SDK code and
// may block until the caller abandons the run; finalize cancels ctx and waits
// for the native writes here before closing the exec handle.
func (t *runTask) pumpStdin(ctx context.Context, sink runStdin) {
	defer close(t.stdinDone)
	chunks := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		for {
			chunk := make([]byte, runStdinChunk)
			n, err := t.command.Stdin.Read(chunk)
			if n > 0 {
				select {
				case chunks <- chunk[:n]:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()
	fail := func(err error) {
		if ctx.Err() == nil {
			t.setProcessError(err)
			t.requestCancellation()
		}
	}
	for {
		select {
		case chunk := <-chunks:
			if err := sink.write(ctx, chunk); err != nil {
				fail(fmt.Errorf("write command stdin: %w", err))
				return
			}
		case err := <-readErr:
			if !errors.Is(err, io.EOF) {
				fail(fmt.Errorf("read command stdin: %w", err))
				return
			}
			if err := sink.close(); err != nil {
				fail(fmt.Errorf("close command stdin: %w", err))
			}
			return
		case <-ctx.Done():
			return
		}
	}
}

func (t *runTask) setProcessError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.processErr == nil {
		t.processErr = err
	}
	t.mu.Unlock()
}

func (t *runTask) setStreamError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.streamErr == nil {
		t.streamErr = err
	}
	t.mu.Unlock()
}

func (t *runTask) markCleanupPending(err error) {
	t.mu.Lock()
	t.cleanupPending = true
	if err != nil && t.cleanupErr == nil {
		t.cleanupErr = err
	}
	t.mu.Unlock()
}

func (t *runTask) markQuarantined(err error) {
	t.mu.Lock()
	t.cleanupPending = true
	t.quarantine = true
	if err != nil && t.cleanupErr == nil {
		t.cleanupErr = err
	}
	t.mu.Unlock()
}

func (t *runTask) currentResult() RunResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	return RunResult{
		ExitCode: t.exitCode, ExitCodeKnown: t.exitCodeKnown,
		OutputDropped: t.outputDropped.Load(), CleanupPending: t.cleanupPending,
	}
}

func (t *runTask) currentResultWithPending() RunResult {
	result := t.currentResult()
	result.CleanupPending = true
	return result
}

func (t *runTask) processError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.processErr
}

func (t *runTask) streamError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.streamErr
}

func (t *runTask) combinedError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cleanupErr
}

func validateOwnedVM(owned OwnedVM) error {
	if strings.TrimSpace(owned.Name) == "" {
		return errors.New("owned VM name is required")
	}
	if len(owned.Labels) == 0 {
		return errors.New("owned VM requires at least one expected label")
	}
	for key := range owned.Labels {
		if strings.TrimSpace(key) == "" {
			return errors.New("owned VM label names must not be empty")
		}
	}
	return nil
}

func validateRunCommand(command RunCommand) error {
	if strings.TrimSpace(command.Path) == "" {
		return errors.New("run command path is required")
	}
	if command.Timeout < 0 {
		return errors.New("run command timeout must not be negative")
	}
	return nil
}

func validateRunOwnership(ctx context.Context, vm runVM, owned OwnedVM) error {
	if vm == nil || vm.name() != owned.Name || vm.id() == "" {
		return fmt.Errorf("owned VM %q identity did not match", owned.Name)
	}
	labels, err := vm.labels(ctx)
	if err != nil {
		return fmt.Errorf("read owned VM %q labels: %w", owned.Name, err)
	}
	if !expectedLabelsMatch(owned.Labels, labels) {
		return fmt.Errorf("owned VM %q labels did not match; left untouched", owned.Name)
	}
	return nil
}

func expectedLabelsMatch(expected, actual map[string]string) bool {
	if len(expected) == 0 {
		return false
	}
	for key, value := range expected {
		got, exists := actual[key]
		if !exists || got != value {
			return false
		}
	}
	return true
}

func cloneLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	clone := make(map[string]string, len(labels))
	for key, value := range labels {
		clone[key] = value
	}
	return clone
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// PendingRun reports whether any VM still has a run or late SDK operation that
// has not fully returned. A pending run blocks another Run call in the same VM.
// It clears after known late calls and cleanup finish, but remains true for the
// lifetime of this Runtime if canceled ExecStream startup may have registered an
// exec handle that the SDK did not return to Go.
func (r *Runtime) PendingRun() bool {
	if r == nil {
		return false
	}
	r.runMu.Lock()
	pending := len(r.runSlots) > 0
	r.runMu.Unlock()
	return pending
}

// RemoveOwned force-stops and removes only a VM whose name, expected labels, and
// persisted SDK identity match. A missing VM is already clean. It verifies the
// identity again before removal and verifies absence afterward. Cleanup uses a
// bounded context detached from caller cancellation; the FFI may still wait for
// a native call to return after that context expires.
func (r *Runtime) RemoveOwned(ctx context.Context, owned OwnedVM) error {
	if r == nil {
		return errors.New("remove owned VM: nil runtime")
	}
	if ctx == nil {
		return errors.New("remove owned VM: nil context")
	}
	if err := validateOwnedVM(owned); err != nil {
		return err
	}
	owned.Labels = cloneLabels(owned.Labels)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runCleanupTimeout)
	defer cancel()
	backend := r.runBackend
	if backend == nil {
		backend = sdkRunBackend{}
	}
	initial, err := backend.lookup(cleanupCtx, owned.Name)
	if errors.Is(err, errOwnedVMNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup owned VM %q: %w", owned.Name, err)
	}
	if err := validateRunOwnership(cleanupCtx, initial, owned); err != nil {
		return err
	}
	if !terminalVMStatus(initial.status()) {
		if err := initial.kill(cleanupCtx); err != nil {
			return fmt.Errorf("kill owned VM %q: %w", owned.Name, err)
		}
	}
	current, err := backend.lookup(cleanupCtx, owned.Name)
	if errors.Is(err, errOwnedVMNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify owned VM %q after kill: %w", owned.Name, err)
	}
	if current == nil {
		return fmt.Errorf("verify owned VM %q after kill: SDK returned an empty identity", owned.Name)
	}
	if current.id() != initial.id() {
		return fmt.Errorf("owned VM %q identity changed during cleanup; replacement left untouched", owned.Name)
	}
	if err := validateRunOwnership(cleanupCtx, current, owned); err != nil {
		return err
	}
	if !terminalVMStatus(current.status()) {
		return fmt.Errorf("owned VM %q remains in nonterminal state %q", owned.Name, current.status())
	}
	if err := current.remove(cleanupCtx); err != nil {
		return fmt.Errorf("remove owned VM %q: %w", owned.Name, err)
	}
	remaining, err := backend.lookup(cleanupCtx, owned.Name)
	if errors.Is(err, errOwnedVMNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify removal of owned VM %q: %w", owned.Name, err)
	}
	if remaining == nil {
		return fmt.Errorf("verify removal of owned VM %q: SDK returned an empty identity", owned.Name)
	}
	if remaining.id() != initial.id() {
		return fmt.Errorf("owned VM %q was replaced during removal; replacement left untouched", owned.Name)
	}
	return fmt.Errorf("owned VM %q remains after removal", owned.Name)
}

func terminalVMStatus(status Status) bool {
	return status == StatusCreated || status == StatusStopped || status == StatusCrashed || status == StatusAbsent
}

type sdkRunBackend struct{}

func (sdkRunBackend) lookup(ctx context.Context, name string) (runVM, error) {
	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			return nil, errOwnedVMNotFound
		}
		return nil, err
	}
	if handle == nil {
		return nil, errors.New("microsandbox returned an empty sandbox handle")
	}
	return sdkRunVM{handle: handle}, nil
}

type sdkRunVM struct{ handle *msb.SandboxHandle }

func (v sdkRunVM) name() string   { return v.handle.Name() }
func (v sdkRunVM) id() string     { return v.handle.ID() }
func (v sdkRunVM) status() Status { return statusOf(v.handle.Status()) }
func (v sdkRunVM) labels(context.Context) (map[string]string, error) {
	config, err := v.handle.Config()
	if err != nil {
		return nil, err
	}
	return cloneLabels(config.Labels), nil
}
func (v sdkRunVM) connect(ctx context.Context) (runConnection, error) {
	sandbox, err := v.handle.Connect(ctx)
	if sandbox == nil {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("microsandbox returned an empty connected sandbox")
	}
	connection := sdkRunConnection{sandbox: sandbox}
	return connection, err
}
func (v sdkRunVM) kill(ctx context.Context) error {
	return v.handle.Kill(ctx, msb.WithKillTimeout(runVMKillTimeout))
}
func (v sdkRunVM) remove(ctx context.Context) error { return v.handle.Remove(ctx) }

type sdkRunConnection struct{ sandbox *msb.Sandbox }

func (c sdkRunConnection) id() string { return c.sandbox.ID() }
func (c sdkRunConnection) detach(ctx context.Context) error {
	return c.sandbox.Detach(ctx)
}
func (c sdkRunConnection) execStream(ctx context.Context, command RunCommand) (runExec, error) {
	options := make([]msb.ExecOption, 0, 5)
	if command.User != "" {
		options = append(options, msb.WithExecUser(command.User))
	}
	if command.Cwd != "" {
		options = append(options, msb.WithExecCwd(command.Cwd))
	}
	if command.Env != nil {
		options = append(options, msb.WithExecEnv(cloneLabels(command.Env)))
	}
	if command.Timeout > 0 {
		options = append(options, msb.WithExecTimeout(command.Timeout))
	}
	if command.Stdin != nil {
		options = append(options, msb.WithExecStdinPipe())
	}
	handle, err := c.sandbox.ExecStream(ctx, command.Path, append([]string(nil), command.Args...), options...)
	if err != nil {
		return nil, err
	}
	if handle == nil {
		return nil, errors.New("microsandbox returned an empty exec handle")
	}
	exec := sdkRunExec{handle: handle}
	if command.Stdin != nil {
		if sink := handle.TakeStdin(); sink != nil {
			exec.sink = sdkRunStdin{sink: sink}
		}
	}
	return exec, nil
}

type sdkRunExec struct {
	handle *msb.ExecHandle
	sink   runStdin
}

func (e sdkRunExec) recv(ctx context.Context) (runEvent, error) {
	event, err := e.handle.Recv(ctx)
	if err != nil {
		return runEvent{}, err
	}
	if event == nil {
		return runEvent{}, errors.New("microsandbox returned an empty exec event")
	}
	switch event.Kind {
	case msb.ExecEventStdout:
		return runEvent{kind: runEventStdout, data: event.Data}, nil
	case msb.ExecEventStderr:
		return runEvent{kind: runEventStderr, data: event.Data}, nil
	case msb.ExecEventExited:
		return runEvent{kind: runEventExited, exitCode: event.ExitCode}, nil
	case msb.ExecEventFailed:
		message := "guest command failed to start"
		if event.Failure != nil {
			message = nonEmpty(event.Failure.Message, message)
		}
		return runEvent{kind: runEventFailed, message: message}, nil
	case msb.ExecEventStdinError:
		message := "guest command stdin failed"
		if event.Failure != nil {
			message = nonEmpty(event.Failure.Message, message)
		}
		return runEvent{kind: runEventStdinError, message: message}, nil
	case msb.ExecEventDone:
		return runEvent{kind: runEventDone}, nil
	default:
		return runEvent{kind: runEventOther}, nil
	}
}
func (e sdkRunExec) kill(ctx context.Context) error { return e.handle.Kill(ctx) }
func (e sdkRunExec) close() error                   { return e.handle.Close() }
func (e sdkRunExec) stdin() runStdin                { return e.sink }

type sdkRunStdin struct{ sink *msb.ExecSink }

func (s sdkRunStdin) write(ctx context.Context, data []byte) error {
	_, err := s.sink.WriteCtx(ctx, data)
	return err
}
func (s sdkRunStdin) close() error { return s.sink.Close() }
