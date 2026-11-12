package sandboxes

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Deleting an environment waits for none of its computers: a busy one refuses it, and
// otherwise every VM goes, of every kind, before the records do.
func TestDeleteEnvironmentRemovesItsVMs(t *testing.T) {
	f := newCheckpointFixture(t)
	p := browserTeammate(t, f)
	browser, err := f.manager.createBrowser(f.ctx, f.env.ID, p.ID, "sandbox-studio-browser:dev")
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.setStatus(VMName(browser), runtime.StatusRunning)
	removed := 0
	remove := func(context.Context) error { removed++; return nil }

	unlock, err := f.manager.tryMutation(f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.DeleteEnvironment(f.ctx, f.env.ID, remove); !errors.Is(err, ErrBusy) || removed != 0 {
		t.Fatalf("a busy computer: %v, removed %d", err, removed)
	}
	unlock()
	if status, _ := f.runtime.Status(f.ctx, VMName(browser)); status != runtime.StatusRunning {
		t.Fatalf("the refused delete touched the browser: %s", status)
	}

	if err := f.manager.DeleteEnvironment(f.ctx, f.env.ID, remove); err != nil || removed != 1 {
		t.Fatalf("delete: %v, removed %d", err, removed)
	}
	for _, sb := range []store.Sandbox{f.sandbox, browser} {
		if status, _ := f.runtime.Status(f.ctx, VMName(sb)); status != runtime.StatusAbsent {
			t.Fatalf("%s's VM is %s", sb.Name, status)
		}
	}
	// The locks are free again.
	unlock, err = f.manager.tryMutation(f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
