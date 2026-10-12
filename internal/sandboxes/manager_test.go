package sandboxes

import (
	"errors"
	"testing"
)

func TestDeleteForgetsHostState(t *testing.T) {
	f := newCheckpointFixture(t)
	var forgot []string
	f.manager.Forget = func(envID, id string) error {
		if _, err := f.store.Sandbox(f.ctx, envID, id); err == nil {
			t.Error("host state was forgotten before the sandbox record was deleted")
		}
		forgot = append(forgot, envID+"/"+id)
		return errors.New("cleanup failed")
	}
	// A cleanup failure doesn't fail the delete; the next start sweeps leftovers.
	if err := f.manager.Delete(f.ctx, f.env.ID, f.sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if len(forgot) != 1 || forgot[0] != f.env.ID+"/"+f.sandbox.ID {
		t.Fatalf("forgot %v", forgot)
	}
}

func TestStartRecordsWhenTheVMStarted(t *testing.T) {
	f := newCheckpointFixture(t)
	if f.sandbox.StartedAt != nil {
		t.Fatalf("new sandbox StartedAt = %v, want none", f.sandbox.StartedAt)
	}
	started, err := f.manager.Start(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.StartedAt == nil || stored.StartedAt == nil || stored.StartedAt.Unix() != started.StartedAt.Unix() {
		t.Fatalf("StartedAt: view %v, stored %v", started.StartedAt, stored.StartedAt)
	}
	// A stop keeps the time: it is when the VM last started, not whether it runs now.
	stopped, err := f.manager.Stop(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.StartedAt == nil || stopped.StartedAt.Unix() != started.StartedAt.Unix() {
		t.Fatalf("stopped view StartedAt = %v, want %v", stopped.StartedAt, started.StartedAt)
	}
}
