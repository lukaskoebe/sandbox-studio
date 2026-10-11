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
