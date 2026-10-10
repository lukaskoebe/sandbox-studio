package sandboxes

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// dialGuest connects a minimal guest agent to the sandbox's hub socket.
func dialGuest(t *testing.T, f *checkpointFixture) *yamux.Session {
	t.Helper()
	sess, err := connectGuest(f.paths.AgentSocket(f.sandbox.ID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func connectGuest(path string) (*yamux.Session, error) {
	nc, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	sess, err := yamux.Client(nc, nil)
	if err != nil {
		nc.Close()
		return nil, err
	}
	st, err := sess.Open()
	if err != nil {
		sess.Close()
		return nil, err
	}
	agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindHello})
	agentproto.WriteJSONLine(st, agentproto.Hello{Version: "test", Arch: "amd64"})
	st.Close()
	go func() {
		for {
			s, err := sess.Accept()
			if err != nil {
				return
			}
			s.Close()
		}
	}()
	return sess, nil
}

func shortResumeWait(t *testing.T) {
	old := resumeAgentWait
	resumeAgentWait = 50 * time.Millisecond
	t.Cleanup(func() { resumeAgentWait = old })
}

func TestSuspendResumeRunningSandbox(t *testing.T) {
	shortResumeWait(t)
	f := newCheckpointFixture(t)
	vm := VMName(f.sandbox)
	f.runtime.setStatus(vm, runtime.StatusRunning)
	if err := f.manager.Hub.Listen(f.sandbox.ID, f.paths.AgentSocket(f.sandbox.ID)); err != nil {
		t.Fatal(err)
	}
	guest := dialGuest(t, f)
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	if err := f.manager.Hub.WaitConnected(ctx, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}

	view, err := f.manager.Suspend(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != runtime.StatusSuspended || view.Agent != nil {
		t.Fatalf("suspended view = %+v", view)
	}
	if f.runtime.countCall("pause:"+vm) != 1 {
		t.Fatal("runtime was not paused")
	}
	select {
	case <-guest.CloseChan():
	case <-ctx.Done():
		t.Fatal("guest session survived suspend")
	}
	if _, ok := f.manager.Hub.Connected(f.sandbox.ID); ok {
		t.Fatal("hub still connected after suspend")
	}

	// The thawed guest reconnects on its own; simulate that once Resume has run.
	reconnected := make(chan *yamux.Session, 1)
	go func() {
		for {
			if st, _ := f.runtime.Status(f.ctx, vm); st == runtime.StatusRunning {
				sess, _ := connectGuest(f.paths.AgentSocket(f.sandbox.ID))
				reconnected <- sess
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	defer func() {
		if sess := <-reconnected; sess != nil {
			sess.Close()
		}
	}()
	resumeAgentWait = 5 * time.Second
	view, err = f.manager.Resume(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != runtime.StatusRunning || view.Agent == nil {
		t.Fatalf("resumed view = %+v", view)
	}
	if f.runtime.countCall("resume:"+vm) != 1 || f.runtime.countCall("start:"+vm) != 0 {
		t.Fatalf("calls = %v", f.runtime.calls)
	}
}

func TestSuspendResumeRefuseWrongState(t *testing.T) {
	shortResumeWait(t)
	f := newCheckpointFixture(t)
	vm := VMName(f.sandbox)
	for _, st := range []runtime.Status{runtime.StatusStopped, runtime.StatusStarting, runtime.StatusSuspended, runtime.StatusCrashed} {
		f.runtime.setStatus(vm, st)
		if _, err := f.manager.Suspend(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrLifecycleState) {
			t.Fatalf("Suspend from %s: err = %v", st, err)
		}
	}
	for _, st := range []runtime.Status{runtime.StatusStopped, runtime.StatusRunning, runtime.StatusStarting} {
		f.runtime.setStatus(vm, st)
		if _, err := f.manager.Resume(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrLifecycleState) {
			t.Fatalf("Resume from %s: err = %v", st, err)
		}
	}
	f.runtime.setStatus(vm, runtime.StatusSuspended)
	if _, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("Start while suspended: err = %v", err)
	}
	if n := f.runtime.countCall("pause:"+vm) + f.runtime.countCall("resume:"+vm) + f.runtime.countCall("start:"+vm); n != 0 {
		t.Fatalf("runtime touched on refusal: %v", f.runtime.calls)
	}
}

func TestSuspendIsSerialized(t *testing.T) {
	f := newCheckpointFixture(t)
	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	unlock, err := f.manager.tryMutation(f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := f.manager.Suspend(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Suspend err = %v, want ErrBusy", err)
	}
	if _, err := f.manager.Resume(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Resume err = %v, want ErrBusy", err)
	}
}

func TestSuspendedSandboxRefusesForkRebaseAndCheckpoint(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	vm := VMName(f.sandbox)
	tf.setStatus(vm, runtime.StatusSuspended)
	if _, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy"); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("Fork err = %v", err)
	}
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID); !errors.Is(err, ErrLifecycleState) {
		t.Fatalf("Rebase err = %v", err)
	}
	if _, err := f.manager.CreateCheckpoint(f.ctx, f.env.ID, f.sandbox.ID, "cp"); !errors.Is(err, ErrCheckpointState) {
		t.Fatalf("CreateCheckpoint err = %v", err)
	}
	if status, _ := tf.Status(f.ctx, vm); status != runtime.StatusSuspended {
		t.Fatalf("sandbox is %s after refusals", status)
	}
}

func TestStopAndDeleteSuspendedSandbox(t *testing.T) {
	f := newCheckpointFixture(t)
	vm := VMName(f.sandbox)
	f.runtime.setStatus(vm, runtime.StatusSuspended)
	view, err := f.manager.Stop(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || view.Status != runtime.StatusStopped {
		t.Fatalf("Stop: %+v, %v", view, err)
	}
	f.runtime.setStatus(vm, runtime.StatusSuspended)
	if err := f.manager.Delete(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if f.runtime.hasVM(vm) {
		t.Fatal("VM remains after delete")
	}
}

func TestReconcileLeavesSuspendedSandboxSuspended(t *testing.T) {
	f := newCheckpointFixture(t)
	vm := VMName(f.sandbox)
	f.runtime.setStatus(vm, runtime.StatusSuspended)
	if err := f.manager.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.runtime.Status(f.ctx, vm); status != runtime.StatusSuspended {
		t.Fatalf("status after reconcile = %s", status)
	}
	for _, call := range []string{"resume:" + vm, "start:" + vm, "stop:" + vm} {
		if f.runtime.countCall(call) != 0 {
			t.Fatalf("reconcile called %s", call)
		}
	}
	// The listener is back, so the agent can reconnect after a later resume.
	dialGuest(t, f)
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	if err := f.manager.Hub.WaitConnected(ctx, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
}
