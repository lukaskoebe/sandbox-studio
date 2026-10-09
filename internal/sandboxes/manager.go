// Package sandboxes manages sandbox lifecycles: catalog records, VMs and agent channels.
package sandboxes

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ErrInvalidSpec remains the manager-facing alias used by API error mapping.
var ErrInvalidSpec = resources.ErrInvalid

// VMPrefix prefixes every microsandbox VM name Studio owns.
const VMPrefix = "ss-"

// Defaults for new sandboxes.
const (
	DefaultCPUs         = resources.DefaultCPUs
	DefaultMemoryMiB    = resources.DefaultMemoryMiB
	DefaultWorkspaceMiB = resources.DefaultWorkspaceMiB
	DefaultDockerMiB    = resources.DefaultDockerMiB
)

// Manager coordinates the catalog, the runtime, the agent hub and the network gateway.
type Manager struct {
	Store   *store.Store
	Runtime SandboxRuntime
	Hub     *agentchan.Hub
	Egress  Egress
	CA      interface {
		CertPEM(ctx context.Context, envID string) ([]byte, error)
	}
	Secrets interface {
		Env(ctx context.Context, envID string) (map[string]string, error)
	}
	Paths paths.Paths
	Log   *slog.Logger

	configuring sync.Map // sandbox ID → chan struct{}, see Configure
	// sandbox ID → *sync.Mutex held by lifecycle changes, see tryMutation. Entries stay for
	// the Manager's lifetime so a deleted sandbox's lock can't be swapped under a holder.
	mutating sync.Map
}

// SandboxRuntime is the runtime surface the manager needs. Keeping it narrow makes
// lifecycle transitions testable without an SDK or host runtime.
type SandboxRuntime interface {
	Create(context.Context, string, runtime.Spec, string, map[string]string) error
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Remove(context.Context, string) error
	Status(context.Context, string) (runtime.Status, error)
	Statuses(context.Context, string) (map[string]runtime.Status, error)
	CreateCheckpoint(context.Context, string, string, string) error
	RemoveCheckpoint(context.Context, string, string) error
	CheckpointRestoreSupported() bool
	RestoreCheckpoint(context.Context, string, string, string, runtime.Egress) error
}

// Egress is the host side of sandbox networking (internal/gateway).
type Egress interface {
	// Attach prepares a sandbox's resolver and gateway credentials. It must run before its
	// VM is created or started.
	Attach(sb store.Sandbox) (runtime.Egress, error)
	// Detach releases them when the sandbox is deleted.
	Detach(sandboxID string)
}

// View is a sandbox as shown to clients: catalog record plus live state.
type View struct {
	store.Sandbox
	Status                     runtime.Status `json:"status" enum:"absent,created,starting,running,draining,paused,stopped,crashed"`
	CheckpointRestoreSupported bool           `json:"checkpointRestoreSupported"`
	Agent                      *AgentInfo     `json:"agent,omitempty"`
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
	MaxMemoryMiB int    `json:"maxMemoryMiB,omitempty"`
	WorkspaceMiB int    `json:"workspaceMiB,omitempty"`
	DockerMiB    int    `json:"dockerMiB,omitempty"`
}

// VMName is the microsandbox name of the current generation of sb.
func VMName(sb store.Sandbox) string { return fmt.Sprintf("%s%s-g%d", VMPrefix, sb.ID, sb.Generation) }

func vmNameAtGeneration(sb store.Sandbox, generation int) string {
	sb.Generation = generation
	return VMName(sb)
}

// Reconcile re-attaches agent listeners for every sandbox after Studio starts.
// Detached VMs keep running across Studio restarts; their agents reconnect on their own.
func (m *Manager) Reconcile(ctx context.Context) error {
	all, err := m.Store.AllSandboxes(ctx)
	if err != nil {
		return err
	}
	ops, err := m.Store.Restores(ctx)
	if err != nil {
		return err
	}
	pending := make(map[string]store.RestoreOperation, len(ops))
	for _, op := range ops {
		pending[op.SandboxID] = op
	}
	seen := make(map[string]bool, len(all))
	for _, sb := range all {
		seen[sb.ID] = true
		if sb.BuildJobID != "" {
			continue
		}
		if op, ok := pending[sb.ID]; ok {
			if op.EnvironmentID != sb.EnvironmentID {
				m.Log.Warn("restore recovery", "sandbox", sb.ID, "err", store.ErrNotFound)
				m.Hub.Close(sb.ID)
				continue
			}
			if err := m.recoverRestore(ctx, sb, op); err != nil {
				m.Log.Warn("restore recovery", "sandbox", sb.ID, "err", err)
				continue // Fail closed for this sandbox; other sandboxes can still reconcile.
			}
			delete(pending, sb.ID)
		}
		if err := m.Hub.Listen(sb.ID, m.Paths.AgentSocket(sb.ID)); err != nil {
			m.Log.Warn("agent listener", "sandbox", sb.ID, "err", err)
		}
		if _, err := m.Egress.Attach(sb); err != nil {
			m.Log.Warn("sandbox network", "sandbox", sb.ID, "err", err)
		}
	}
	for id := range pending {
		if !seen[id] {
			m.Log.Warn("restore recovery", "sandbox", id, "err", store.ErrNotFound)
		}
	}
	return nil
}

// Create records and boots a new sandbox.
func (m *Manager) Create(ctx context.Context, envID string, req CreateRequest) (View, error) {
	resolved := resolveResources(req)
	if err := resolved.Validate(); err != nil {
		return View{}, err
	}
	if err := runtime.ValidName(req.Name); err != nil {
		return View{}, err
	}
	rec := store.Sandbox{
		EnvironmentID: envID,
		Name:          req.Name,
		CPUs:          int(resolved.CPUs),
		MemoryMiB:     int(resolved.MemoryMiB),
		MaxMemoryMiB:  int(resolved.MaxMemoryMiB),
		WorkspaceMiB:  int(resolved.WorkspaceMiB),
		DockerMiB:     int(resolved.DockerMiB),
	}
	rec, err := m.Store.CreateSandbox(ctx, rec)
	if err != nil {
		return View{}, err
	}
	egress, err := m.Egress.Attach(rec)
	if err != nil {
		m.Store.DeleteSandbox(ctx, envID, rec.ID)
		return View{}, err
	}
	sock := m.Paths.AgentSocket(rec.ID)
	if err := m.Hub.Listen(rec.ID, sock); err != nil {
		m.Egress.Detach(rec.ID)
		m.Store.DeleteSandbox(ctx, envID, rec.ID)
		return View{}, err
	}
	spec := runtime.Spec{
		CPUs: uint8(resolved.CPUs), MemoryMiB: uint32(resolved.MemoryMiB), MaxMemoryMiB: uint32(resolved.MaxMemoryMiB),
		WorkspaceMiB: uint32(rec.WorkspaceMiB), DockerMiB: uint32(rec.DockerMiB),
		Egress: egress,
	}
	labels := map[string]string{
		"studio.sandbox-id":     rec.ID,
		"studio.environment-id": envID,
		"studio.sandbox-name":   rec.Name,
	}
	if err := m.Runtime.Create(ctx, VMName(rec), spec, sock, labels); err != nil {
		m.Hub.Close(rec.ID)
		m.Egress.Detach(rec.ID)
		m.Runtime.Remove(context.WithoutCancel(ctx), VMName(rec))
		m.Store.DeleteSandbox(context.WithoutCancel(ctx), envID, rec.ID)
		return View{}, err
	}
	return m.view(ctx, rec)
}

// Get returns one sandbox with its live state.
func (m *Manager) Get(ctx context.Context, envID, id string) (View, error) {
	rec, err := m.PublicSandbox(ctx, envID, id)
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
	public := recs[:0]
	for _, rec := range recs {
		if rec.BuildJobID == "" {
			public = append(public, rec)
		}
	}
	recs = public
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
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if err := m.ensureNoRestore(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if _, err := m.Egress.Attach(rec); err != nil {
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
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if err := m.ensureNoRestore(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err = m.PublicSandbox(ctx, envID, id)
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
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return err
	}
	defer unlock()
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return err
	}
	if err := m.ensureNoRestore(ctx, envID, id, ""); err != nil {
		return err
	}
	rec, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return err
	}
	checkpoints, err := m.Store.Checkpoints(ctx, envID, id)
	if err != nil {
		return err
	}
	if err := m.deleteCheckpointsLocked(ctx, envID, id, checkpoints); err != nil {
		return err
	}
	if err := m.Runtime.Remove(ctx, VMName(rec)); err != nil {
		return err
	}
	m.Hub.Close(rec.ID)
	m.Egress.Detach(rec.ID)
	return m.Store.DeleteSandbox(ctx, envID, id)
}

// WaitReady waits until the sandbox's guest agent is connected.
func (m *Manager) WaitReady(ctx context.Context, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.Hub.WaitConnected(ctx, id)
}

// Terminals lists the tmux sessions of a sandbox.
func (m *Manager) Terminals(ctx context.Context, envID, id string) ([]agentproto.Session, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return nil, err
	}
	return m.Hub.Sessions(ctx, id)
}

// OpenTerminal attaches to the tmux session name, creating it if needed.
func (m *Manager) OpenTerminal(ctx context.Context, envID, id, name string, cols, rows uint16) (*agentchan.PTY, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return nil, err
	}
	return m.Hub.OpenPTY(ctx, id, name, cols, rows)
}

// CloseTerminal ends a tmux session and every terminal attached to it.
func (m *Manager) CloseTerminal(ctx context.Context, envID, id, name string) error {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	return m.Hub.KillSession(ctx, id, name)
}

// Ports lists the TCP ports listening inside a sandbox.
func (m *Manager) Ports(ctx context.Context, envID, id string) ([]agentproto.Port, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return nil, err
	}
	return m.Hub.Ports(ctx, id)
}

// PublicSandbox returns one sandbox that belongs to envID unless it is reserved for a
// private build job. Builder sandboxes are catalogued for the worker and gateway, but are
// not part of the user-facing sandbox API.
func (m *Manager) PublicSandbox(ctx context.Context, envID, id string) (store.Sandbox, error) {
	sb, err := m.Store.Sandbox(ctx, envID, id)
	if err != nil {
		return store.Sandbox{}, err
	}
	if sb.BuildJobID != "" {
		return store.Sandbox{}, store.ErrNotFound
	}
	return sb, nil
}

// DialPreviewTCP connects to a public sandbox's loopback port over its guest-agent
// channel. Build VMs remain private even when their IDs are supplied as preview hosts.
func (m *Manager) DialPreviewTCP(ctx context.Context, sandboxID string, port int) (net.Conn, error) {
	sb, err := m.Store.LookupSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if sb.BuildJobID != "" {
		return nil, store.ErrNotFound
	}
	return m.Hub.DialTCP(ctx, sandboxID, port)
}

func (m *Manager) view(ctx context.Context, rec store.Sandbox) (View, error) {
	st, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		return View{}, err
	}
	return m.withAgent(View{Sandbox: rec, Status: st}), nil
}

func (m *Manager) withAgent(v View) View {
	v.CheckpointRestoreSupported = m.Runtime.CheckpointRestoreSupported()
	if hello, ok := m.Hub.Connected(v.ID); ok && v.Status.IsRunning() {
		v.Agent = &AgentInfo{Version: hello.Version, Arch: hello.Arch}
	}
	return v
}

func resolveResources(req CreateRequest) resources.Resources {
	defaults := resources.Defaults()
	memory := resourceOrDefault(req.MemoryMiB, defaults.MemoryMiB)
	maxMemory := int64(req.MaxMemoryMiB)
	if maxMemory == 0 {
		maxMemory = memory
	}
	return resources.Resources{
		CPUs:         resourceOrDefault(req.CPUs, defaults.CPUs),
		MemoryMiB:    memory,
		MaxMemoryMiB: maxMemory,
		WorkspaceMiB: resourceOrDefault(req.WorkspaceMiB, defaults.WorkspaceMiB),
		DockerMiB:    resourceOrDefault(req.DockerMiB, defaults.DockerMiB),
	}
}

func resourceOrDefault(v int, def int64) int64 {
	if v == 0 {
		return def
	}
	return int64(v)
}
