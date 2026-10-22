package sandboxes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/forkmerge"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// mergeFake answers studio-agent merge commands: an export writes "archive:<vm>", an apply
// keeps what it read, and the reports carry conflicts.
type mergeFake struct {
	*transferFake
	mu              sync.Mutex
	conflicts       int
	replayConflicts []string
	applied         map[string]string // vm → archive it read
}

func newMergeFake(f *checkpointFixture) *mergeFake {
	tf := newTransferFake(f)
	tf.guestBoots = true
	mf := &mergeFake{transferFake: tf, applied: map[string]string{}}
	f.manager.Runtime = mf
	return mf
}

func (f *mergeFake) RunAgentCommand(ctx context.Context, owned runtime.OwnedVM, args []string, stdin io.Reader, stdout io.Writer) error {
	f.record("agent:" + owned.Name + ":" + args[0])
	if err := f.running(ctx, owned.Name); err != nil {
		return err
	}
	if err := f.failure("agent:" + owned.Name + ":" + args[0]); err != nil {
		return err
	}
	f.mu.Lock()
	conflicts, replay := f.conflicts, f.replayConflicts
	f.mu.Unlock()
	report := func() error {
		return json.NewEncoder(stdout).Encode(forkmerge.Report{Fork: "copy", Conflicts: conflicts, Files: []forkmerge.FileResult{}})
	}
	switch args[0] {
	case "merge-export", "merge-replay-export":
		_, err := io.WriteString(stdout, "archive:"+owned.Name)
		return err
	case "merge-apply", "merge-replay":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		f.mu.Lock()
		f.applied[owned.Name] = string(b)
		f.mu.Unlock()
		if args[0] == "merge-replay" {
			return json.NewEncoder(stdout).Encode(forkmerge.ReplayResult{Conflicts: replay})
		}
		return report()
	case "merge-status":
		return report()
	}
	return nil
}

func (f *mergeFake) set(conflicts int, replay []string) {
	f.mu.Lock()
	f.conflicts, f.replayConflicts = conflicts, replay
	f.mu.Unlock()
}

func (f *mergeFake) archive(vm string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applied[vm]
}

// forkForMerge forks the fixture's sandbox, running, into "copy".
func forkForMerge(t *testing.T, f *checkpointFixture, mf *mergeFake) store.Sandbox {
	t.Helper()
	mf.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	v, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if mf.countCall("agent:"+VMName(v.Sandbox)+":merge-base") != 1 {
		t.Fatal("the fork recorded no merge base")
	}
	return v.Sandbox
}

func waitMerge(t *testing.T, f *checkpointFixture, id string) MergeView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := f.manager.GetMerge(f.ctx, f.env.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != store.MergePreparing || time.Now().After(deadline) {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMergeForkIntoSource(t *testing.T) {
	f := newCheckpointFixture(t)
	mf := newMergeFake(f)
	fork := forkForMerge(t, f, mf)
	mf.set(1, nil)

	started, err := f.manager.StartMerge(f.ctx, f.env.ID, fork.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.StartMerge(f.ctx, f.env.ID, fork.ID); !errors.Is(err, ErrMergeOpen) {
		t.Fatalf("a second merge into the same sandbox: %v", err)
	}
	mg := waitMerge(t, f, started.ID)
	if mg.State != store.MergeConflicts || mg.Report == nil || mg.Report.Conflicts != 1 || mg.ScratchName != "checkpoint-box-merge-1" {
		t.Fatalf("merge = %+v", mg)
	}
	scratch, err := f.manager.PublicSandbox(f.ctx, f.env.ID, mg.ScratchID)
	if err != nil {
		t.Fatal(err)
	}
	if mf.countCall("agent:"+VMName(scratch)+":merge-base") != 1 || mf.archive(VMName(scratch)) != "archive:"+VMName(fork) {
		t.Fatalf("scratch sandbox: base %d, archive %q", mf.countCall("agent:"+VMName(scratch)+":merge-base"), mf.archive(VMName(scratch)))
	}
	if _, err := f.manager.AcceptMerge(f.ctx, f.env.ID, mg.ID); !errors.Is(err, ErrMergeNotReady) {
		t.Fatalf("accept with conflicts: %v", err)
	}

	// The source changed a path since: the replay refuses, and the merge stays open.
	mf.set(0, []string{"workspace/a.txt"})
	var replayErr *MergeReplayError
	if _, err := f.manager.AcceptMerge(f.ctx, f.env.ID, mg.ID); !errors.As(err, &replayErr) || replayErr.Paths[0] != "workspace/a.txt" {
		t.Fatalf("accept over a changed source: %v", err)
	}
	if v, _ := f.manager.GetMerge(f.ctx, f.env.ID, mg.ID); v.State != store.MergeReady || v.CheckpointID == "" {
		t.Fatalf("after a refused replay: %+v", v)
	}

	mf.set(0, nil)
	accepted, err := f.manager.AcceptMerge(f.ctx, f.env.ID, mg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.State != store.MergeAccepted || mf.archive(VMName(f.sandbox)) != "archive:"+VMName(scratch) {
		t.Fatalf("accepted = %+v, source read %q", accepted, mf.archive(VMName(f.sandbox)))
	}
	if _, err := f.manager.PublicSandbox(f.ctx, f.env.ID, scratch.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("scratch sandbox after accept: %v", err)
	}
	events, err := f.manager.Store.SandboxEvents(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Kind != store.EventMerge || last.OtherSandboxID != fork.ID {
		t.Fatalf("last event = %+v", last)
	}
	checkpoints, err := f.manager.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
	// One per Accept: the refused one, and this one.
	if err != nil || len(checkpoints) != 2 || accepted.CheckpointID != checkpoints[0].ID || checkpoints[0].Name != "before merging copy (2)" {
		t.Fatalf("checkpoints = %+v, %v", checkpoints, err)
	}
}

func TestAbortMergeDeletesScratch(t *testing.T) {
	f := newCheckpointFixture(t)
	mf := newMergeFake(f)
	fork := forkForMerge(t, f, mf)
	started, err := f.manager.StartMerge(f.ctx, f.env.ID, fork.ID)
	if err != nil {
		t.Fatal(err)
	}
	mg := waitMerge(t, f, started.ID)
	if mg.State != store.MergeReady {
		t.Fatalf("merge = %+v", mg)
	}
	if err := f.manager.AbortMerge(f.ctx, f.env.ID, mg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.PublicSandbox(f.ctx, f.env.ID, mg.ScratchID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("scratch sandbox after abort: %v", err)
	}
	if v, _ := f.manager.GetMerge(f.ctx, f.env.ID, mg.ID); v.State != store.MergeAborted {
		t.Fatalf("state = %s", v.State)
	}
	if mf.archive(VMName(f.sandbox)) != "" {
		t.Fatal("abort touched the source")
	}
}

func TestMergeNeedsARunningForkOfALiveSandbox(t *testing.T) {
	f := newCheckpointFixture(t)
	mf := newMergeFake(f)
	if _, err := f.manager.StartMerge(f.ctx, f.env.ID, f.sandbox.ID); !errors.Is(err, ErrNotAFork) {
		t.Fatalf("merge of a sandbox that isn't a fork: %v", err)
	}
	fork := forkForMerge(t, f, mf)
	mf.setStatus(VMName(fork), runtime.StatusStopped)
	var notRunning *MergeNotRunningError
	if _, err := f.manager.StartMerge(f.ctx, f.env.ID, fork.ID); !errors.As(err, &notRunning) || notRunning.Name != "copy" {
		t.Fatalf("merge of a stopped fork: %v", err)
	}
	mf.setStatus(VMName(fork), runtime.StatusRunning)
	mf.fail("agent:"+VMName(fork)+":merge-export", errors.New("disk on fire"))
	started, err := f.manager.StartMerge(f.ctx, f.env.ID, fork.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mg := waitMerge(t, f, started.ID); mg.State != store.MergeFailed || mg.Error == "" {
		t.Fatalf("merge after a failed export = %+v", mg)
	}
}
