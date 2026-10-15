//go:build e2e

// Package e2e holds the in-process fakes of the end-to-end test build (`go build -tags e2e`):
// a VM runtime whose sandboxes "boot" in milliseconds, a guest agent that speaks the real
// channel protocol over the per-sandbox agent socket, an upstream for the gateway, and a
// loopback control API the Playwright suite drives them with. Production builds never
// contain this package. See docs/architecture.md#testing-and-ci.
package e2e

import (
	"context"
	"errors"
	"log/slog"
	goruntime "runtime"
	"strings"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

// ErrNoVMs is returned for what needs a real VM: template builds and checkpoint restores.
var ErrNoVMs = errors.New("the e2e build has no VMs")

// Runtime fakes the microsandbox runtime. Creating or starting a VM starts a fake guest
// agent that dials the VM's agent socket; stopping it disconnects the guest.
type Runtime struct {
	Image string
	Log   *slog.Logger

	mu     sync.Mutex
	vms    map[string]*vm    // by VM name
	guests map[string]*Guest // by sandbox ID, the guest of the current generation
}

type vm struct {
	sandboxID string
	name      string // the sandbox's name: browser-<teammate> for a browser VM
	status    runtime.Status
	socket    string
	egress    runtime.Egress
	size      runtime.Size
}

// NewRuntime returns a runtime without VMs.
func NewRuntime(image string, log *slog.Logger) *Runtime {
	return &Runtime{Image: image, Log: log, vms: map[string]*vm{}, guests: map[string]*Guest{}}
}

// Guest returns the guest agent of a sandbox, if one ever ran.
func (r *Runtime) Guest(sandboxID string) (*Guest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.guests[sandboxID]
	return g, ok
}

// BrowserGuest returns the guest agent of a teammate's browser VM, if it exists.
func (r *Runtime) BrowserGuest(teammateID string) (*Guest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.vms {
		if v.name == sandboxes.BrowserName(teammateID) {
			g, ok := r.guests[v.sandboxID]
			return g, ok
		}
	}
	return nil, false
}

func (r *Runtime) Create(_ context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vms[name]; ok {
		return errors.New("a VM named " + name + " already exists")
	}
	v := &vm{sandboxID: labels["studio.sandbox-id"], name: labels["studio.sandbox-name"], socket: socket, egress: spec.Egress, size: runtime.Size{CPUs: spec.CPUs, MemoryMiB: spec.MemoryMiB}}
	r.vms[name] = v
	r.bootLocked(v)
	return nil
}

func (r *Runtime) Start(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vms[name]
	if !ok {
		return errors.New("no VM named " + name)
	}
	if v.status != runtime.StatusRunning {
		r.bootLocked(v)
	}
	return nil
}

// bootLocked starts a fresh guest, as a reboot would: terminals and sessions are gone, the
// recorded configuration and connections stay for the tests to read.
func (r *Runtime) bootLocked(v *vm) {
	v.status = runtime.StatusRunning
	g, ok := r.guests[v.sandboxID]
	if !ok {
		g = newGuest(v.sandboxID, r.Log)
		r.guests[v.sandboxID] = g
	}
	g.boot(v.socket, v.egress)
}

func (r *Runtime) Stop(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.vms[name]; ok {
		v.status = runtime.StatusStopped
		if g, ok := r.guests[v.sandboxID]; ok {
			g.shutdown()
		}
	}
	return nil
}

func (r *Runtime) Pause(_ context.Context, name string) error {
	return r.setStatus(name, runtime.StatusRunning, runtime.StatusSuspended)
}

func (r *Runtime) Resume(_ context.Context, name string) error {
	return r.setStatus(name, runtime.StatusSuspended, runtime.StatusRunning)
}

func (r *Runtime) setStatus(name string, from, to runtime.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vms[name]
	if !ok || v.status != from {
		return errors.New("VM " + name + " is not " + string(from))
	}
	v.status = to
	return nil
}

// Resize models msb's dry run: a running VM takes every change live, and any other VM saves
// its changes for the next start.
func (r *Runtime) Resize(_ context.Context, name string, size runtime.Size) (runtime.Resized, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vms[name]
	if !ok {
		return runtime.Resized{}, errors.New("no VM named " + name)
	}
	applied := runtime.AppliedNextStart
	if v.status == runtime.StatusRunning {
		applied = runtime.AppliedLive
	}
	out := runtime.Resized{CPUs: runtime.AppliedUnchanged, MemoryMiB: runtime.AppliedUnchanged}
	if v.size.CPUs != size.CPUs {
		out.CPUs = applied
	}
	if v.size.MemoryMiB != size.MemoryMiB {
		out.MemoryMiB = applied
	}
	v.size = size
	return out, nil
}

func (r *Runtime) Remove(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vms[name]
	if !ok {
		return nil
	}
	delete(r.vms, name)
	if g, ok := r.guests[v.sandboxID]; ok {
		g.shutdown()
		delete(r.guests, v.sandboxID)
	}
	return nil
}

func (r *Runtime) Status(_ context.Context, name string) (runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.vms[name]; ok {
		return v.status, nil
	}
	return runtime.StatusAbsent, nil
}

func (r *Runtime) Statuses(_ context.Context, prefix string) (map[string]runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]runtime.Status{}
	for name, v := range r.vms {
		if strings.HasPrefix(name, prefix) {
			out[name] = v.status
		}
	}
	return out, nil
}

// Checkpoints are recorded by the manager; the fake keeps no disk state to snapshot.
func (r *Runtime) CreateCheckpoint(context.Context, string, string, string) error { return nil }
func (r *Runtime) RemoveCheckpoint(context.Context, string, string) error         { return nil }
func (r *Runtime) CheckpointRestoreSupported() bool                               { return false }
func (r *Runtime) RestoreCheckpoint(context.Context, string, string, string, runtime.Egress) error {
	return ErrNoVMs
}

// The template build worker's side: jobs fail, as there is no image to build on.

func (r *Runtime) BaseReference() string  { return r.Image }
func (r *Runtime) TargetPlatform() string { return "linux/" + goruntime.GOARCH }
func (r *Runtime) PendingRun() bool       { return false }
func (r *Runtime) PrepareTemplateBaseOwned(context.Context, runtime.BaseOwner) (templateimage.Base, error) {
	return templateimage.Base{}, ErrNoVMs
}
func (r *Runtime) RemovePrewarm(context.Context, string, string) error { return nil }
func (r *Runtime) Run(context.Context, runtime.OwnedVM, runtime.RunCommand, chan<- runtime.RunOutput) (runtime.RunResult, error) {
	return runtime.RunResult{}, ErrNoVMs
}
