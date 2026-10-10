// Package sandboxes manages sandbox lifecycles: catalog records, VMs and agent channels.
package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
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
	Store     *store.Store
	Runtime   SandboxRuntime
	Templates interface {
		Resolve(context.Context, string, string) (templateregistry.Reference, error)
	}
	Hub    *agentchan.Hub
	Egress Egress
	CA     interface {
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
	Pause(context.Context, string) error
	Resume(context.Context, string) error
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
	Status                     runtime.Status `json:"status" enum:"absent,created,starting,running,draining,suspended,stopped,crashed"`
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
	rebases, err := m.Store.Rebases(ctx)
	if err != nil {
		return err
	}
	pendingRebases := make(map[string]store.RebaseOperation, len(rebases))
	for _, op := range rebases {
		pendingRebases[op.SandboxID] = op
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
		if op, ok := pendingRebases[sb.ID]; ok {
			if err := m.recoverRebase(ctx, sb, op); err != nil {
				m.Log.Warn("rebase recovery", "sandbox", sb.ID, "err", err)
				m.Hub.Close(sb.ID)
				continue // Fail closed for this sandbox, as for restores.
			}
			delete(pendingRebases, sb.ID)
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
	for id := range pendingRebases {
		if !seen[id] {
			m.Log.Warn("rebase recovery", "sandbox", id, "err", store.ErrNotFound)
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
	rec, err := m.createSandboxRecord(ctx, envID, req.Name, resolved, "")
	if err != nil {
		return View{}, err
	}
	return m.bootSandbox(ctx, rec, resolved, nil, false)
}

// CreateFromTemplate starts a fresh sandbox from one ready, same-environment template.
// The catalog row pins the template before registry metadata is resolved or the VM is
// allocated. The runtime receives credentials only in its host-side image options.
func (m *Manager) CreateFromTemplate(ctx context.Context, envID, templateID, name string) (View, error) {
	if err := runtime.ValidName(name); err != nil {
		return View{}, err
	}
	resolved, err := m.templateResources(ctx, envID, templateID)
	if err != nil {
		return View{}, err
	}
	rec, err := m.createSandboxRecord(ctx, envID, name, resolved, templateID)
	if err != nil {
		return View{}, err
	}
	image, err := m.templateImage(ctx, envID, templateID)
	if err != nil {
		if cleanupErr := m.removeUnstartedTemplateRecord(ctx, rec, false); cleanupErr != nil {
			return View{}, cleanupErr
		}
		return View{}, err
	}
	return m.bootSandbox(ctx, rec, resolved, image, true)
}

// templateResources checks that a template can back a sandbox on this host and returns
// the resources its specification pins.
func (m *Manager) templateResources(ctx context.Context, envID, templateID string) (resources.Resources, error) {
	template, err := m.Store.Template(ctx, envID, templateID)
	if err != nil {
		return resources.Resources{}, err
	}
	if template.State != store.TemplateStateReady {
		return resources.Resources{}, store.ErrConflict
	}
	spec, err := templatespec.ParseCanonicalJSON([]byte(template.Spec))
	if err != nil {
		return resources.Resources{}, errors.New("template specification is invalid")
	}
	resolved := spec.Resources
	if err := resolved.Validate(); err != nil {
		return resources.Resources{}, errors.New("template resources are invalid")
	}
	if template.ExporterVersion != templateimage.ExporterVersion {
		return resources.Resources{}, errors.New("template exporter version is unsupported")
	}
	cacheKey, err := templateimage.CacheKey([]byte(template.Spec), template.BaseDigest, template.Platform, template.ExporterVersion)
	if err != nil || cacheKey != template.CacheKey {
		return resources.Resources{}, errors.New("template metadata is invalid")
	}
	if !templatePlatformCompatible(template.Platform, goruntime.GOARCH) {
		return resources.Resources{}, errors.New("template platform is not supported on this host")
	}
	if m.Templates == nil {
		return resources.Resources{}, errors.New("template registry is unavailable")
	}
	return resolved, nil
}

// templateImage resolves the registry image of a template. A catalog record must pin the
// template first, so it cannot be deleted in the meantime.
func (m *Manager) templateImage(ctx context.Context, envID, templateID string) (*runtime.ImageSource, error) {
	ref, err := m.Templates.Resolve(ctx, envID, templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
			return nil, err
		}
		return nil, errors.New("template image is unavailable")
	}
	if ref.Username != envID || ref.Password == "" || !templateImageReference(ref.Image, envID, templateID) {
		return nil, errors.New("template image is unavailable")
	}
	return &runtime.ImageSource{Reference: ref.Image, Username: ref.Username, Password: ref.Password}, nil
}

func (m *Manager) createSandboxRecord(ctx context.Context, envID, name string, resolved resources.Resources, templateID string) (store.Sandbox, error) {
	return m.Store.CreateSandbox(ctx, store.Sandbox{
		EnvironmentID: envID,
		TemplateID:    templateID,
		Name:          name,
		CPUs:          int(resolved.CPUs),
		MemoryMiB:     int(resolved.MemoryMiB),
		MaxMemoryMiB:  int(resolved.MaxMemoryMiB),
		WorkspaceMiB:  int(resolved.WorkspaceMiB),
		DockerMiB:     int(resolved.DockerMiB),
	})
}

func (m *Manager) bootSandbox(ctx context.Context, rec store.Sandbox, resolved resources.Resources, image *runtime.ImageSource, retainOnUncertainBoot bool) (View, error) {
	egress, err := m.Egress.Attach(rec)
	if err != nil {
		if retainOnUncertainBoot {
			if cleanupErr := m.removeUnstartedTemplateRecord(ctx, rec, true); cleanupErr != nil {
				return View{}, cleanupErr
			}
		} else {
			m.Store.DeleteSandbox(ctx, rec.EnvironmentID, rec.ID)
		}
		return View{}, err
	}
	sock := m.Paths.AgentSocket(rec.ID)
	if err := m.Hub.Listen(rec.ID, sock); err != nil {
		m.Egress.Detach(rec.ID)
		if retainOnUncertainBoot {
			if cleanupErr := m.removeUnstartedTemplateRecord(ctx, rec, false); cleanupErr != nil {
				return View{}, cleanupErr
			}
		} else {
			m.Store.DeleteSandbox(ctx, rec.EnvironmentID, rec.ID)
		}
		return View{}, err
	}
	spec := runtime.Spec{
		CPUs: uint8(resolved.CPUs), MemoryMiB: uint32(resolved.MemoryMiB), MaxMemoryMiB: uint32(resolved.MaxMemoryMiB),
		WorkspaceMiB: uint32(rec.WorkspaceMiB), DockerMiB: uint32(rec.DockerMiB),
		Image: image, Egress: egress,
	}
	labels := sandboxLabels(rec)
	if err := m.Runtime.Create(ctx, VMName(rec), spec, sock, labels); err != nil {
		if retainOnUncertainBoot {
			m.cleanupFailedTemplateBoot(ctx, rec, labels)
			// SDK errors can include private registry details. The catalog row remains
			// available if exact VM absence could not be established.
			return View{}, errors.New("sandbox creation from template failed")
		}
		m.Hub.Close(rec.ID)
		m.Egress.Detach(rec.ID)
		m.Runtime.Remove(context.WithoutCancel(ctx), VMName(rec))
		m.Store.DeleteSandbox(context.WithoutCancel(ctx), rec.EnvironmentID, rec.ID)
		return View{}, err
	}
	return m.view(ctx, rec)
}

func sandboxLabels(rec store.Sandbox) map[string]string {
	labels := map[string]string{
		"studio.sandbox-id":     rec.ID,
		"studio.environment-id": rec.EnvironmentID,
		"studio.sandbox-name":   rec.Name,
	}
	if rec.TemplateID != "" {
		labels["studio.template-id"] = rec.TemplateID
	}
	return labels
}

func (m *Manager) removeUnstartedTemplateRecord(ctx context.Context, rec store.Sandbox, detach bool) error {
	if detach {
		m.Hub.Close(rec.ID)
		m.Egress.Detach(rec.ID)
	}
	if err := m.Store.DeleteSandbox(context.WithoutCancel(ctx), rec.EnvironmentID, rec.ID); err != nil {
		return fmt.Errorf("remove unstarted sandbox record: %w", err)
	}
	return nil
}

func (m *Manager) cleanupFailedTemplateBoot(ctx context.Context, rec store.Sandbox, labels map[string]string) {
	remover, ok := m.Runtime.(interface {
		RemoveOwned(context.Context, runtime.OwnedVM) error
	})
	if !ok {
		return
	}
	owned := runtime.OwnedVM{Name: VMName(rec), Labels: labels}
	if err := remover.RemoveOwned(context.WithoutCancel(ctx), owned); err != nil {
		return
	}
	// RemoveOwned verifies absence. Only then is it safe to drop the template pin.
	m.Hub.Close(rec.ID)
	m.Egress.Detach(rec.ID)
	_ = m.Store.DeleteSandbox(context.WithoutCancel(ctx), rec.EnvironmentID, rec.ID)
}

func templatePlatformCompatible(platform, arch string) bool {
	parts := strings.Split(platform, "/")
	return (len(parts) == 2 || len(parts) == 3) && parts[0] == "linux" && parts[1] == arch &&
		(arch == "amd64" || arch == "arm64")
}

func templateImageReference(image, envID, templateID string) bool {
	parts := strings.Split(image, "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] != "studio" || parts[2] != envID {
		return false
	}
	repository, digest, ok := strings.Cut(parts[3], "@")
	return ok && repository == templateID && !strings.Contains(digest, "@") && templateimage.ValidDigest(digest)
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
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if status, err := m.Runtime.Status(ctx, VMName(rec)); err != nil {
		return View{}, err
	} else if status == runtime.StatusSuspended {
		return View{}, lifecycleError("the sandbox is suspended; resume it instead")
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
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
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
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
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
