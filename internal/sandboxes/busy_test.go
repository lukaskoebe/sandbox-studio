package sandboxes

import (
	"context"
	"testing"
)

// A restart would cut off a mutation or an import, and only while it runs.
func TestBusySaysWhatARestartWouldCutOff(t *testing.T) {
	f := newCheckpointFixture(t)
	if got := f.manager.Busy(); got != "" {
		t.Fatalf("Busy with nothing running = %q", got)
	}
	unlock, err := f.manager.tryMutation(f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.manager.Busy(); got != "a sandbox operation is running" {
		t.Fatalf("Busy during a mutation = %q", got)
	}
	unlock()
	if got := f.manager.Busy(); got != "" {
		t.Fatalf("Busy after a mutation = %q", got)
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.manager.imports.track("import", cancel)
	if got := f.manager.Busy(); got != "a sandbox import is running" {
		t.Fatalf("Busy during an import = %q", got)
	}
	f.manager.imports.untrack("import")
	if got := f.manager.Busy(); got != "" {
		t.Fatalf("Busy after an import = %q", got)
	}
}
