package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/tasks"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// newBrowser makes the browser broker (docs/browser.md). Its Runner and VMs, the hub and
// the sandbox manager, are set once they exist; the gateway needs its Driver first.
func newBrowser(st *store.Store, vault *secrets.Vault, bus *events.Bus, data, image string, log *slog.Logger) *browser.Service {
	return &browser.Service{
		Store: st, Keys: vault, Log: log, Image: browserImage(image), Dir: filepath.Join(data, "browser"),
		Notify: func(envID string) { bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID}) },
	}
}

// sandboxStopped tells the browser broker when an agent sandbox's VM no longer runs, so the
// ephemeral browser sessions it opened end. A suspended sandbox still counts as running.
func sandboxStopped(mgr *sandboxes.Manager) browser.Stopped {
	return func(ctx context.Context, envID, id string) (bool, error) {
		v, err := mgr.Get(ctx, envID, id)
		if err != nil {
			return false, err
		}
		return !v.Status.IsRunning() && v.Status != runtime.StatusSuspended && v.Status != runtime.StatusDraining, nil
	}
}

// browserImage is the browser image that goes with a base image: the same registry and
// version, named sandbox-studio-browser. A release build's own base gives its pinned
// browser image; another digest-pinned base gives the version's tag.
func browserImage(base string) string {
	if version.BrowserImage != "" && base == version.BaseImage {
		return version.BrowserImage
	}
	if i := strings.Index(base, "@"); i >= 0 {
		base = base[:i] + ":" + version.Version
	}
	return strings.Replace(base, "sandbox-studio-base", "sandbox-studio-browser", 1)
}

// routeCalls sends a guest's call to the integration that serves the MCP tool it names, its
// browser tools to the broker, its task tools to the task service, and the rest to next
// (memory). Every call comes from a sandbox the channel names; a browser VM or a template build
// makes none.
func routeCalls(st *store.Store, integs integrations.Set, brw *browser.Service, tsk *tasks.Service, log *slog.Logger,
	next func(ctx context.Context, id, method string, params json.RawMessage) (any, error)) func(ctx context.Context, id, method string, params json.RawMessage) (any, error) {
	return func(ctx context.Context, id, method string, params json.RawMessage) (any, error) {
		if t, ok := integs.Tool(method); ok {
			sb, err := st.LookupSandbox(ctx, id)
			if err != nil {
				return nil, errors.New("unknown sandbox")
			}
			if sb.Kind != "" || sb.BuildJobID != "" {
				return nil, errors.New("only agent sandboxes make tool calls")
			}
			res, err := t.Handle(ctx, sb, params)
			if err != nil && !integrations.IsToolError(err) {
				log.Warn("tool failed", "tool", method, "sandbox", id, "err", err)
				return nil, errors.New(method + " failed")
			}
			return res, err
		}
		if strings.HasPrefix(method, agentproto.BrowserMethodPrefix) {
			return brw.HandleCall(ctx, id, method, params)
		}
		if strings.HasPrefix(method, agentproto.TaskMethodPrefix) {
			return tsk.HandleCall(ctx, id, method, params)
		}
		return next(ctx, id, method, params)
	}
}
