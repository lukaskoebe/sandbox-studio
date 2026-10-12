package sandboxes

import (
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// historyOf lists the events of a sandbox, including a deleted one's.
func historyOf(t *testing.T, f *checkpointFixture, id string) []store.SandboxEvent {
	t.Helper()
	events, err := f.store.SandboxEvents(f.ctx, f.env.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// forkOf forks the sandbox id and waits until the copy has booted.
func forkOf(t *testing.T, f *checkpointFixture, id, name string) string {
	t.Helper()
	answered, err := f.manager.Fork(f.ctx, f.env.ID, id, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, answered.ID); err != nil {
		t.Fatalf("fork %s: %v", name, err)
	}
	return answered.ID
}

func TestCreateRecordsItsEventOnlyOnSuccess(t *testing.T) {
	f := newCheckpointFixture(t)
	failing := newProgressRuntime(f, errors.New("injected"))
	close(failing.release)
	failed, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{Name: "broken"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, failed.ID); err == nil {
		t.Fatal("create succeeded")
	}
	if events := historyOf(t, f, failed.ID); len(events) != 0 {
		t.Fatalf("a failed create recorded %+v", events)
	}

	ok := newProgressRuntime(f, nil)
	close(ok.release)
	view, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{Name: "first-run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, view.ID); err != nil {
		t.Fatal(err)
	}
	events := historyOf(t, f, view.ID)
	if len(events) != 1 || events[0].Kind != store.EventCreate || events[0].Generation != 1 || events[0].Name != "first-run" {
		t.Fatalf("create events = %+v", events)
	}
}

func TestForkRecordsBothSidesOnSuccessOnly(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	tf.guestBoots = true
	source := f.sandbox.ID

	child := forkOf(t, f, source, "copy")
	childEvents := historyOf(t, f, child)
	if len(childEvents) != 1 || childEvents[0].Kind != store.EventFork || childEvents[0].OtherSandboxID != source || childEvents[0].Name != "copy" {
		t.Fatalf("fork's events = %+v", childEvents)
	}
	sourceEvents := historyOf(t, f, source)
	if len(sourceEvents) != 1 || sourceEvents[0].Kind != store.EventFork || sourceEvents[0].OtherSandboxID != child || sourceEvents[0].Generation != f.sandbox.Generation {
		t.Fatalf("source's events = %+v", sourceEvents)
	}
	if got, err := f.store.Sandbox(f.ctx, f.env.ID, child); err != nil || got.ForkedFromID != source || got.ForkedFromGeneration != f.sandbox.Generation {
		t.Fatalf("fork reference = %+v, %v", got, err)
	}

	tf.fail("export:"+VMName(f.sandbox), errors.New("injected"))
	answered, err := f.manager.Fork(f.ctx, f.env.ID, source, "failed-copy")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, answered.ID); err == nil {
		t.Fatal("fork succeeded")
	}
	if events := historyOf(t, f, answered.ID); len(events) != 0 {
		t.Fatalf("a failed fork recorded %+v", events)
	}
	if n := len(historyOf(t, f, source)); n != 1 {
		t.Fatalf("a failed fork added to the source's history: %d events", n)
	}
}

func TestRebaseAndCheckpointRecordTheirEvents(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	source := VMName(f.sandbox)
	tf.setStatus(source, runtime.StatusRunning)
	tf.setWorkspace(source, "files")

	checkpoint := f.createReadyCheckpoint(t, "before")
	events := historyOf(t, f, f.sandbox.ID)
	if len(events) != 1 || events[0].Kind != store.EventCheckpoint || events[0].CheckpointID != checkpoint.ID || events[0].CheckpointName != "before" {
		t.Fatalf("checkpoint events = %+v", events)
	}

	tf.fail("export:"+source, errors.New("injected"))
	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID); err == nil {
		t.Fatal("rebase succeeded")
	}
	if n := len(historyOf(t, f, f.sandbox.ID)); n != 1 {
		t.Fatalf("a failed rebase recorded events: %d in all", n)
	}

	tf.clearFailure("export:" + source)
	view, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	events = historyOf(t, f, f.sandbox.ID)
	if len(events) != 2 || events[1].Kind != store.EventRebase || events[1].Generation != view.Generation ||
		events[1].TemplateID != template.ID || events[1].FromTemplateID != "" {
		t.Fatalf("rebase events = %+v", events)
	}
}

func TestDeleteLeavesATombstoneUnlessTheCreateFailed(t *testing.T) {
	f := newCheckpointFixture(t)
	newTransferFake(f).guestBoots = true
	source := f.sandbox.ID
	child := forkOf(t, f, source, "copy")

	if err := f.manager.Delete(f.ctx, f.env.ID, child); err != nil {
		t.Fatal(err)
	}
	tombstone, err := f.store.DeleteEvent(f.ctx, f.env.ID, child)
	if err != nil || tombstone.Name != "copy" || tombstone.OtherSandboxID != source || tombstone.Generation != 1 {
		t.Fatalf("tombstone of the fork = %+v, %v", tombstone, err)
	}
	if events := historyOf(t, f, child); len(events) != 2 || events[1].Kind != store.EventDelete {
		t.Fatalf("history of the deleted fork = %+v", events)
	}

	rt := newProgressRuntime(f, errors.New("injected"))
	close(rt.release)
	failed, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{Name: "broken"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, failed.ID); err == nil {
		t.Fatal("create succeeded")
	}
	if err := f.manager.Delete(f.ctx, f.env.ID, failed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DeleteEvent(f.ctx, f.env.ID, failed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a failed create left a tombstone: %v", err)
	}
}

// A family survives its deletions: the tree keeps a deleted fork under its source, with the
// forks of the fork under it, and the lineage of a fork names its deleted source.
func TestLineageAndTreeKeepDeletedForks(t *testing.T) {
	f := newCheckpointFixture(t)
	newTransferFake(f).guestBoots = true
	source := f.sandbox.ID
	child := forkOf(t, f, source, "child")
	grandchild := forkOf(t, f, child, "grandchild")
	if err := f.manager.Delete(f.ctx, f.env.ID, child); err != nil {
		t.Fatal(err)
	}

	f.runtime.setCheckpointRestoreSupported(false)
	checkpoint := f.createReadyCheckpoint(t, "snapshot")
	gated, err := f.manager.Lineage(f.ctx, f.env.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	if gated.CheckpointRestoreSupported || len(gated.Checkpoints) != 1 || gated.Checkpoints[0].ID != checkpoint.ID || gated.Checkpoints[0].RestoreAllowed {
		t.Fatalf("lineage while the restore gate is closed = %+v", gated)
	}
	f.runtime.setCheckpointRestoreSupported(true)
	open, err := f.manager.Lineage(f.ctx, f.env.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	if !open.CheckpointRestoreSupported || !open.Checkpoints[0].RestoreAllowed {
		t.Fatalf("lineage while the restore gate is open = %+v", open)
	}
	if len(open.Children) != 1 || open.Children[0].ID != child || !open.Children[0].Deleted || open.Children[0].Status != StatusDeleted || open.Children[0].ForkGeneration != 1 {
		t.Fatalf("children of the source = %+v", open.Children)
	}

	lineage, err := f.manager.Lineage(f.ctx, f.env.ID, grandchild)
	if err != nil {
		t.Fatal(err)
	}
	if lineage.Parent == nil || lineage.Parent.ID != child || !lineage.Parent.Deleted || lineage.Parent.Name != "child" || lineage.Parent.ForkGeneration != 1 {
		t.Fatalf("parent of the grandchild = %+v", lineage.Parent)
	}
	if len(lineage.Events) != 1 || lineage.Events[0].OtherSandboxID != child {
		t.Fatalf("events of the grandchild = %+v", lineage.Events)
	}
	if _, err := f.manager.Lineage(f.ctx, f.env.ID, child); err == nil {
		t.Fatal("the lineage of a deleted sandbox is served")
	}

	tree, err := f.manager.LineageTree(f.ctx, f.env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Templates) != 1 || tree.Templates[0].ID != "" || len(tree.Templates[0].Sandboxes) != 1 {
		t.Fatalf("tree = %+v", tree)
	}
	root := tree.Templates[0].Sandboxes[0]
	if root.ID != source || len(root.Forks) != 1 || root.Forks[0].ID != child || !root.Forks[0].Deleted {
		t.Fatalf("root of the tree = %+v", root)
	}
	if forks := root.Forks[0].Forks; len(forks) != 1 || forks[0].ID != grandchild || forks[0].Deleted || forks[0].Status != string(runtime.StatusRunning) {
		t.Fatalf("forks of the deleted fork = %+v", forks)
	}
}
