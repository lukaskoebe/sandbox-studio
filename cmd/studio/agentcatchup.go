package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// agentCatchUp installs the guest agent Studio couldn't install at its start, because a running
// sandbox held the old one open (Windows), before the next VM is created or started, once no VM
// is running or suspended any more: runtime.Options.BeforeBoot. Until then every sandbox keeps
// the old agent: swapping the file under a VM that has it cached shows that VM a mix of both
// binaries.
type agentCatchUp struct {
	vms interface {
		Statuses(context.Context, string) (map[string]runtime.Status, error)
	}
	install func() error
	log     *slog.Logger

	mu    sync.Mutex
	stale bool
}

func (a *agentCatchUp) catchUp(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.stale {
		return
	}
	statuses, err := a.vms.Statuses(ctx, "")
	if err != nil {
		return
	}
	for _, s := range statuses {
		if s != runtime.StatusStopped && s != runtime.StatusCrashed && s != runtime.StatusAbsent && s != runtime.StatusCreated {
			return
		}
	}
	switch err := a.install(); {
	case err == nil:
		a.stale = false
		a.log.Info("the guest agent was updated, now that no sandbox holds it")
	case !errors.Is(err, agentbin.ErrInUse):
		a.log.Warn("updating the guest agent", "err", err)
	}
}
