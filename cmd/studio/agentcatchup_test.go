package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

type catchUpVMs struct {
	statuses map[string]runtime.Status
	started  []string
	catchUp  func(context.Context)
}

func (v *catchUpVMs) Statuses(context.Context, string) (map[string]runtime.Status, error) {
	return v.statuses, nil
}

// Start is runtime.Runtime's: BeforeBoot, then the VM.
func (v *catchUpVMs) Start(ctx context.Context, name string) error {
	v.catchUp(ctx)
	v.started = append(v.started, name)
	return nil
}

func TestTheGuestAgentCatchesUpOnceNoSandboxHoldsIt(t *testing.T) {
	ctx := context.Background()
	vms := &catchUpVMs{statuses: map[string]runtime.Status{"ss-a": runtime.StatusRunning, "ss-b": runtime.StatusStopped}}
	installs := 0
	held := true
	a := &agentCatchUp{vms: vms, log: slog.New(slog.NewTextHandler(io.Discard, nil)), stale: true,
		install: func() error {
			installs++
			if held {
				return agentbin.ErrInUse
			}
			return nil
		}}
	vms.catchUp = a.catchUp

	// One runs: no try, the start goes on.
	if err := vms.Start(ctx, "ss-b"); err != nil || installs != 0 || len(vms.started) != 1 {
		t.Fatalf("while one runs: %v installs=%d started=%v", err, installs, vms.started)
	}
	// A suspended one has the old agent cached too.
	vms.statuses["ss-a"] = runtime.StatusSuspended
	_ = vms.Start(ctx, "ss-b")
	if installs != 0 {
		t.Fatalf("while one is suspended: installs=%d", installs)
	}
	// All stopped, but something still holds it: it stays stale and tries again next time.
	vms.statuses["ss-a"] = runtime.StatusStopped
	_ = vms.Start(ctx, "ss-b")
	if installs != 1 || !a.stale {
		t.Fatalf("still held: installs=%d stale=%v", installs, a.stale)
	}
	held = false
	_ = vms.Start(ctx, "ss-b")
	_ = vms.Start(ctx, "ss-a")
	if installs != 2 || a.stale || len(vms.started) != 5 {
		t.Fatalf("caught up: installs=%d stale=%v started=%v", installs, a.stale, vms.started)
	}
}
