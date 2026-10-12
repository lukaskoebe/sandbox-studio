package sandboxes

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// answerGuest connects a guest agent to socket, as a booted VM's agent does, and answers the
// requests a creation makes: the session list and the configuration.
func answerGuest(socket string) error {
	nc, err := net.Dial("unix", socket)
	if err != nil {
		return err
	}
	sess, err := yamux.Client(nc, nil)
	if err != nil {
		nc.Close()
		return err
	}
	hello, err := sess.Open()
	if err != nil {
		sess.Close()
		return err
	}
	_ = agentproto.WriteJSONLine(hello, agentproto.Header{Kind: agentproto.KindHello})
	_ = agentproto.WriteJSONLine(hello, agentproto.Hello{Version: "test", Arch: "amd64"})
	_ = hello.Close()
	go func() {
		for {
			stream, err := sess.Accept()
			if err != nil {
				return
			}
			go answerRequest(stream)
		}
	}()
	return nil
}

func answerRequest(stream net.Conn) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	var header agentproto.Header
	if agentproto.ReadJSONLine(br, &header) != nil {
		return
	}
	switch header.Kind {
	case agentproto.KindSessions:
		_ = agentproto.WriteJSONLine(stream, []agentproto.Session{})
	case agentproto.KindConfig:
		var cfg agentproto.Config
		if agentproto.ReadJSONLine(br, &cfg) == nil {
			_ = agentproto.WriteJSONLine(stream, struct{}{})
		}
	}
}

// progressRuntime reports a create's pull the way the SDK does, then waits until the test
// releases it. It fails with failure if set, and otherwise boots the VM.
type progressRuntime struct {
	*fakeSandboxRuntime
	pulled  chan struct{} // closed once the pull is reported
	release chan struct{} // closed by the test to let the create go on
	failure error
}

func (r *progressRuntime) Create(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	spec.Progress(runtime.Progress{Phase: runtime.PhasePull, Pull: runtime.PullProgress{Layers: 2, LayersDone: 1, Bytes: 100, TotalBytes: 300}})
	close(r.pulled)
	select {
	case <-r.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if r.failure != nil {
		return r.failure
	}
	spec.Progress(runtime.Progress{Phase: runtime.PhaseVM})
	spec.Progress(runtime.Progress{Phase: runtime.PhaseBoot})
	return r.fakeSandboxRuntime.Create(ctx, name, spec, socket, labels)
}

func newProgressRuntime(f *checkpointFixture, failure error) *progressRuntime {
	f.runtime.guestBoots = true
	rt := &progressRuntime{fakeSandboxRuntime: f.runtime, pulled: make(chan struct{}), release: make(chan struct{}), failure: failure}
	f.manager.Runtime = rt
	return rt
}

func TestCreateAnswersAtOnceAndReportsItsSteps(t *testing.T) {
	f := newCheckpointFixture(t)
	f.manager.Bus = &events.Bus{}
	changes, unsubscribe := f.manager.Bus.Subscribe()
	defer unsubscribe()
	rt := newProgressRuntime(f, nil)

	view, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{Name: "first-run"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != StatusCreating || view.Progress == nil || view.Progress.State != ProgressRunning || view.Progress.Step != StepPull {
		t.Fatalf("Create answered %+v; want the sandbox creating at its pull", view)
	}
	if got := strings.Join(view.Progress.Steps, ","); got != strings.Join(createSteps, ",") {
		t.Fatalf("steps = %s; want %s", got, strings.Join(createSteps, ","))
	}
	if !publishedFor(changes, view.ID) {
		t.Fatal("Create did not publish a sandboxes event for the new sandbox")
	}

	<-rt.pulled
	pulling, err := f.manager.Get(f.ctx, f.env.ID, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pull := pulling.Progress.Pull; pull == nil || pull.Layers != 2 || pull.LayersDone != 1 || pull.Bytes != 100 || pull.TotalBytes != 300 {
		t.Fatalf("pull progress = %+v; want 1 of 2 layers, 100 of 300 bytes", pulling.Progress)
	}

	close(rt.release)
	if err := f.manager.Wait(f.ctx, view.ID); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	done, err := f.manager.Get(f.ctx, f.env.ID, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != runtime.StatusRunning || done.Progress != nil || done.Agent == nil {
		t.Fatalf("created sandbox = %+v; want it running, its agent connected and no progress", done)
	}
	saved, err := f.store.SettingsWithPrefix(f.ctx, progressKeyPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatalf("progress of a finished create is still saved: %v", saved)
	}
}

func TestFailedCreateKeepsItsRecordUntilDeleted(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := newProgressRuntime(f, errors.New("registry unreachable"))

	view, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{Name: "first-run"})
	if err != nil {
		t.Fatal(err)
	}
	close(rt.release)
	if err := f.manager.Wait(f.ctx, view.ID); err == nil || !strings.Contains(err.Error(), "registry unreachable") {
		t.Fatalf("Wait = %v; want the pull's error", err)
	}

	failed, err := f.manager.Get(f.ctx, f.env.ID, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed || failed.Progress == nil || failed.Progress.State != ProgressFailed ||
		failed.Progress.Step != StepPull || !strings.Contains(failed.Progress.Error, "registry unreachable") {
		t.Fatalf("failed sandbox = %+v; want it failed at its pull with the error", failed)
	}
	if n := f.runtime.countCall("remove:" + VMName(failed.Sandbox)); n != 1 {
		t.Fatalf("the failed create removed its VM %d times; want once", n)
	}
	if _, err := f.manager.Start(f.ctx, f.env.ID, view.ID); err == nil || !strings.Contains(err.Error(), "delete it") {
		t.Fatalf("Start of a failed sandbox = %v; want a refusal to delete it", err)
	}

	if err := f.manager.Delete(f.ctx, f.env.ID, view.ID); err != nil {
		t.Fatalf("Delete of a failed sandbox: %v", err)
	}
	if _, err := f.store.Sandbox(f.ctx, f.env.ID, view.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted sandbox lookup = %v; want not found", err)
	}
	saved, err := f.store.SettingsWithPrefix(f.ctx, progressKeyPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatalf("progress of a deleted sandbox is still saved: %v", saved)
	}
}

func TestRestartFailsCreatesThatWereRunning(t *testing.T) {
	f := newCheckpointFixture(t)
	running := Progress{Operation: OperationCreate, State: ProgressRunning, Steps: createSteps, Step: StepBoot}
	if err := f.manager.saveProgress(f.ctx, f.sandbox.ID, f.env.ID, running); err != nil {
		t.Fatal(err)
	}
	// Progress of a sandbox that no longer exists is dropped, not kept for ever.
	if err := f.manager.saveProgress(f.ctx, "gone", f.env.ID, running); err != nil {
		t.Fatal(err)
	}

	if err := f.manager.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	view, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != StatusFailed || view.Progress == nil || view.Progress.Step != StepBoot ||
		!strings.Contains(view.Progress.Error, "Studio stopped") {
		t.Fatalf("interrupted sandbox = %+v; want it failed at boot because Studio stopped", view)
	}
	if n := f.runtime.countCall("remove:" + VMName(f.sandbox)); n != 1 {
		t.Fatalf("the interrupted create's VM was removed %d times; want once", n)
	}

	// Failed stays failed across restarts, and its VM isn't touched again.
	if err := f.manager.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	again, err := f.manager.Get(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != StatusFailed || again.Progress == nil || again.Progress.Error != view.Progress.Error {
		t.Fatalf("after a second restart = %+v; want the same failure", again)
	}
	if n := f.runtime.countCall("remove:" + VMName(f.sandbox)); n != 1 {
		t.Fatalf("a second restart removed the VM again (%d removals)", n)
	}
	saved, err := f.store.SettingsWithPrefix(f.ctx, progressKeyPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved[progressKeyPrefix+"gone"]; ok {
		t.Fatal("progress of a missing sandbox was kept")
	}
}

// publishedFor reports whether a sandboxes event for id is waiting on changes.
func publishedFor(changes <-chan events.Event, id string) bool {
	for {
		select {
		case e := <-changes:
			if e.Topic == events.TopicSandboxes && e.ID == id {
				return true
			}
		default:
			return false
		}
	}
}
