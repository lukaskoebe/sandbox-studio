// Package sandboxes manages sandbox lifecycles: catalog records, VMs and agent channels.
package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// VMPrefix prefixes every microsandbox VM name Studio owns.
const VMPrefix = "ss-"

// Defaults for new sandboxes.
const (
	DefaultCPUs         = 2
	DefaultMemoryMiB    = 4096
	DefaultWorkspaceMiB = 20 * 1024
	DefaultDockerMiB    = 20 * 1024
)

// Manager coordinates the catalog, the runtime and the agent hub.
type Manager struct {
	Store   *store.Store
	Runtime *runtime.Runtime
	Hub     *agentchan.Hub
	Paths   paths.Paths
	Log     *slog.Logger
}

// View is a sandbox as shown to clients: catalog record plus live state.
type View struct {
	store.Sandbox
	Status runtime.Status `json:"status"`
	Agent  *AgentInfo     `json:"agent,omitempty"`
}

// AgentInfo describes a connected guest agent.
type AgentInfo struct {
	Version string `json:"version"`
	Arch    string `json:"arch"`
}

// CreateRequest describes a new sandbox; zero values take the defaults.
type CreateRequest struct {
	Name         string `json:"name"`
	CPUs         int    `json:"cpus,omitempty"`
	MemoryMiB    int    `json:"memoryMiB,omitempty"`
	WorkspaceMiB int    `json:"workspaceMiB,omitempty"`
	DockerMiB    int    `json:"dockerMiB,omitempty"`
}

// VMName is the microsandbox name of the current generation of sb.
func VMName(sb store.Sandbox) string { return fmt.Sprintf("%s%s-g%d", VMPrefix, sb.ID, sb.Generation) }

// Reconcile re-attaches agent listeners for every sandbox after Studio starts.
// Detached VMs keep running across Studio restarts; their agents reconnect on their own.
func (m *Manager) Reconcile(ctx context.Context) error {
	all, err := m.Store.AllSandboxes(ctx)
	if err != nil {
		return err
	}
	for _, sb := range all {
		if err := m.Hub.Listen(sb.ID, m.Paths.AgentSocket(sb.ID)); err != nil {
			m.Log.Warn("agent listener", "sandbox", sb.ID, "err", err)
		}
	}
	return nil
}

// Create records and boots a new sandbox.
func (m *Manager) Create(ctx context.Context, envID string, req CreateRequest) (View, error) {
	if err := runtime.ValidName(req.Name); err != nil {
		return View{}, err
	}
	rec := store.Sandbox{
		EnvironmentID: envID,
		Name:          req.Name,
		CPUs:          orDefault(req.CPUs, DefaultCPUs),
		MemoryMiB:     orDefault(req.MemoryMiB, DefaultMemoryMiB),
		WorkspaceMiB:  orDefault(req.WorkspaceMiB, DefaultWorkspaceMiB),
		DockerMiB:     orDefault(req.DockerMiB, DefaultDockerMiB),
	}
	if rec.CPUs < 1 || rec.CPUs > 64 || rec.MemoryMiB < 512 || rec.WorkspaceMiB < 1024 || rec.DockerMiB < 1024 {
		return View{}, errors.New("resources out of range (≥1 CPU, ≥512 MiB memory, ≥1 GiB disks)")
	}
	rec, err := m.Store.CreateSandbox(ctx, rec)
	if err != nil {
		return View{}, err
	}
	sock := m.Paths.AgentSocket(rec.ID)
	if err := m.Hub.Listen(rec.ID, sock); err != nil {
		m.Store.DeleteSandbox(ctx, envID, rec.ID)
		return View{}, err
	}
	spec := runtime.Spec{
		CPUs: uint8(rec.CPUs), MemoryMiB: uint32(rec.MemoryMiB),
		WorkspaceMiB: uint32(rec.WorkspaceMiB), DockerMiB: uint32(rec.DockerMiB),
	}
	labels := map[string]string{
		"studio.sandbox-id":     rec.ID,
		"studio.environment-id": envID,
		"studio.sandbox-name":   rec.Name,
	}
	if err := m.Runtime.Create(ctx, VMName(rec), spec, sock, labels); err != nil {
		m.Hub.Close(rec.ID)
		m.Runtime.Remove(context.WithoutCancel(ctx), VMName(rec))
		m.Store.DeleteSandbox(context.WithoutCancel(ctx), envID, rec.ID)
		return View{}, err
	}
	return m.view(ctx, rec)
}

// Get returns one sandbox with its live state.
func (m *Manager) Get(ctx context.Context, envID, id string) (View, error) {
	rec, err := m.Store.Sandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	return m.view(ctx, rec)
}

// List returns the sandboxes of an environment with their live state.
func (m *Manager) List(ctx context.Context, envID string) ([]View, error) {
	recs, err := m.Store.Sandboxes(ctx, envID)
	if err != nil {
		return nil, err
	}
	statuses, err := m.Runtime.Statuses(ctx, VMPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(recs))
	for _, rec := range recs {
		st, ok := statuses[VMName(rec)]
		if !ok {
			st = runtime.StatusAbsent
		}
		out = append(out, m.withAgent(View{Sandbox: rec, Status: st}))
	}
	return out, nil
}

// Start boots a stopped sandbox.
func (m *Manager) Start(ctx context.Context, envID, id string) (View, error) {
	rec, err := m.Store.Sandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if err := m.Hub.Listen(rec.ID, m.Paths.AgentSocket(rec.ID)); err != nil {
		return View{}, err
	}
	if err := m.Runtime.Start(ctx, VMName(rec)); err != nil {
		return View{}, err
	}
	return m.view(ctx, rec)
}

// Stop shuts a sandbox down. Its disks and catalog record are kept.
func (m *Manager) Stop(ctx context.Context, envID, id string) (View, error) {
	rec, err := m.Store.Sandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if err := m.Runtime.Stop(ctx, VMName(rec)); err != nil {
		return View{}, err
	}
	return m.view(ctx, rec)
}

// Delete stops a sandbox and removes its VM, disks and record.
func (m *Manager) Delete(ctx context.Context, envID, id string) error {
	rec, err := m.Store.Sandbox(ctx, envID, id)
	if err != nil {
		return err
	}
	if err := m.Runtime.Remove(ctx, VMName(rec)); err != nil {
		return err
	}
	m.Hub.Close(rec.ID)
	return m.Store.DeleteSandbox(ctx, envID, id)
}

// WaitReady waits until the sandbox's guest agent is connected.
func (m *Manager) WaitReady(ctx context.Context, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.Hub.WaitConnected(ctx, id)
}

func (m *Manager) view(ctx context.Context, rec store.Sandbox) (View, error) {
	st, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		return View{}, err
	}
	return m.withAgent(View{Sandbox: rec, Status: st}), nil
}

func (m *Manager) withAgent(v View) View {
	if hello, ok := m.Hub.Connected(v.ID); ok && v.Status.IsRunning() {
		v.Agent = &AgentInfo{Version: hello.Version, Arch: hello.Arch}
	}
	return v
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
