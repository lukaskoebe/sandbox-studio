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
	"github.com/lukaskoebe/sandbox-studio/internal/events"
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

// HomeMiB is the size of the home disk every new sandbox gets at /home/agent. It is fixed
// and separate from the workspace's.
const HomeMiB = 4096

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
		Env(ctx context.Context, sb store.Sandbox) (map[string]string, error)
	}
	Paths paths.Paths
	Log   *slog.Logger
	// Builds builds the templates of imported sandboxes; nil disables template imports.
	Builds Builds
	// Forget removes host-side state kept for a sandbox elsewhere (git staging repos) once
	// it is deleted; nil keeps none.
	Forget func(envID, id string) error
	// Bus, if set, gets an events.TopicSandboxes notice whenever a sandbox changes: its
	// lifecycle, checkpoints, rebases, forks or history.
	Bus *events.Bus

	opsMu sync.Mutex
	ops   map[string]*operation // sandbox ID → its create or fork; see progress.go

	configuring sync.Map // sandbox ID → chan struct{}, see Configure
	autoFailed  sync.Map // sandbox ID → when its scheduled checkpoint last failed, see scheduledCheckpoint
	kickOnce    sync.Once
	kick        chan struct{} // see scheduleKick
	harness     harnessConfigs
	// sandbox ID → *sync.Mutex held by lifecycle changes, see tryMutation. Entries stay for
	// the Manager's lifetime so a deleted sandbox's lock can't be swapped under a holder.
	mutating sync.Map

	terminals  terminalWatch // see WatchTerminals
	imports    importState
	importPoll time.Duration                // build status poll interval; 0 means 1s
	diskFree   func(string) (uint64, error) // overrides the free-disk probe in tests
}

// SandboxRuntime is the runtime surface the manager needs. Keeping it narrow makes
// lifecycle transitions testable without an SDK or host runtime.
type SandboxRuntime interface {
	Create(context.Context, string, runtime.Spec, string, map[string]string) error
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Pause(context.Context, string) error
	Resume(context.Context, string) error
	Resize(context.Context, string, runtime.Size) (runtime.Resized, error)
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
	Status                     runtime.Status `json:"status" enum:"absent,created,starting,running,draining,suspended,stopped,crashed,creating,failed" doc:"The VM's runtime status; creating or failed while a create or fork runs or after it failed"`
	CheckpointRestoreSupported bool           `json:"checkpointRestoreSupported"`
	Agent                      *AgentInfo     `json:"agent,omitempty"`
	Progress                   *Progress      `json:"progress,omitempty" doc:"The create or fork that runs or failed"`
	TemplateName               string         `json:"templateName,omitempty" doc:"The display name of the template the sandbox runs, such as node-22+python-3.13@2"`
	ForkOf                     string         `json:"forkOf,omitempty" doc:"The ID of the sandbox this one was forked from, which it can be merged back into"`
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
	TeammateID   string `json:"teammateId,omitempty" doc:"The teammate that owns the sandbox; omit for an unowned sandbox"`
}

// VMName is the microsandbox name of the current generation of sb.
func VMName(sb store.Sandbox) string { return fmt.Sprintf("%s%s-g%d", VMPrefix, sb.ID, sb.Generation) }

func vmNameAtGeneration(sb store.Sandbox, generation int) string {
	sb.Generation = generation
	return VMName(sb)
}

// Reconcile re-attaches agent listeners for every sandbox after Studio starts.
// Detached VMs keep running across Studio restarts; their agents reconnect on their own.
// A create or fork that ran when Studio stopped has failed (see recoverProgress).
func (m *Manager) Reconcile(ctx context.Context) error {
	m.recoverImports(ctx)
	m.recoverMerges(ctx)
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
	failedCreates := m.recoverProgress(ctx, all)
	seen := make(map[string]bool, len(all))
	for _, sb := range all {
		seen[sb.ID] = true
		if sb.BuildJobID != "" || failedCreates[sb.ID] {
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

// Create records a new sandbox and returns it in state creating. Its VM is created and
// booted in the background; see progress.go.
func (m *Manager) Create(ctx context.Context, envID string, req CreateRequest) (View, error) {
	resolved := resolveResources(req)
	if err := resolved.Validate(); err != nil {
		return View{}, err
	}
	if err := runtime.ValidName(req.Name); err != nil {
		return View{}, err
	}
	rec, err := m.createOwnedRecord(ctx, envID, req.Name, resolved, "", req.TeammateID)
	if err != nil {
		return View{}, err
	}
	return m.bootSandbox(ctx, rec, resolved, nil, HomeMiB)
}

// CreateFromTemplateFor creates a fresh sandbox, owned by teammateID if it is set, from one
// ready, same-environment template, like Create. The catalog row pins the template before
// registry metadata is resolved or the VM is allocated. The runtime receives credentials
// only in its host-side image options.
func (m *Manager) CreateFromTemplateFor(ctx context.Context, envID, templateID, name, teammateID string) (View, error) {
	if err := runtime.ValidName(name); err != nil {
		return View{}, err
	}
	resolved, err := m.templateResources(ctx, envID, templateID)
	if err != nil {
		return View{}, err
	}
	home, err := m.homeMiBFor(ctx, envID, templateID)
	if err != nil {
		return View{}, err
	}
	rec, err := m.createOwnedRecord(ctx, envID, name, resolved, templateID, teammateID)
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
	return m.bootSandbox(ctx, rec, resolved, image, home)
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
	if !templateimage.SupportedExporterVersion(template.ExporterVersion) {
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

// homeMiBFor is the home disk a sandbox made from templateID gets, or 0 for none. A sandbox
// without a template is made from the base image, which has the home skeleton. A sandbox of
// a legacy template keeps its home on the root layer, next to the toolchains it needs: a
// home disk would hide them.
func (m *Manager) homeMiBFor(ctx context.Context, envID, templateID string) (uint32, error) {
	if templateID == "" {
		return HomeMiB, nil
	}
	template, err := m.Store.Template(ctx, envID, templateID)
	if err != nil {
		return 0, err
	}
	if !templateimage.HasHomeSkeleton(template.ExporterVersion) {
		return 0, nil
	}
	return HomeMiB, nil
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

// bootSandbox attaches a new record's network and agent socket, then creates and boots its
// VM in the background and returns the sandbox in state creating. Until the background
// create starts, a failure removes the record. After that, a failure removes the VM and
// leaves the record failed, with the step, until it is deleted.
func (m *Manager) bootSandbox(ctx context.Context, rec store.Sandbox, resolved resources.Resources, image *runtime.ImageSource, homeMiB uint32) (View, error) {
	egress, err := m.Egress.Attach(rec)
	if err != nil {
		return View{}, m.discardRecord(ctx, rec, err)
	}
	sock := m.Paths.AgentSocket(rec.ID)
	if err := m.Hub.Listen(rec.ID, sock); err != nil {
		return View{}, m.discardRecord(ctx, rec, err)
	}
	unlock, err := m.tryMutation(rec.ID)
	if err != nil {
		return View{}, m.discardRecord(ctx, rec, err)
	}
	if err := m.begin(ctx, rec, OperationCreate, createSteps); err != nil {
		unlock()
		return View{}, m.discardRecord(ctx, rec, err)
	}
	spec := runtime.Spec{
		CPUs: uint8(resolved.CPUs), MemoryMiB: uint32(resolved.MemoryMiB), MaxMemoryMiB: uint32(resolved.MaxMemoryMiB),
		WorkspaceMiB: uint32(rec.WorkspaceMiB), DockerMiB: uint32(rec.DockerMiB), HomeMiB: homeMiB,
		Image: image, Egress: egress, Progress: m.runtimeProgress(rec.ID),
	}
	labels := sandboxLabels(rec)
	m.background(rec, createTimeout, unlock, func(ctx context.Context) error {
		err := m.Runtime.Create(ctx, VMName(rec), spec, sock, labels)
		if err != nil && image != nil {
			// SDK errors can include private registry details.
			err = errors.New("sandbox creation from template failed")
		}
		if err == nil {
			err = m.awaitGuest(ctx, rec)
		}
		if err != nil {
			return m.removeFailedVM(ctx, rec, err, image != nil)
		}
		m.markStarted(ctx, rec)
		m.recordEvent(ctx, eventFor(rec, store.EventCreate))
		return nil
	})
	return m.acceptedView(ctx, rec), nil
}

// discardRecord removes the record of a create or fork that never got a VM, and returns
// cause, joined with any failure to remove the record.
func (m *Manager) discardRecord(ctx context.Context, rec store.Sandbox, cause error) error {
	m.Hub.Close(rec.ID)
	m.Egress.Detach(rec.ID)
	if err := m.Store.DeleteSandbox(context.WithoutCancel(ctx), rec.EnvironmentID, rec.ID); err != nil {
		return errors.Join(cause, fmt.Errorf("remove unstarted sandbox record: %w", err))
	}
	return cause
}

// acceptedView is the view of a create or fork the API has accepted. A status that can't
// be read now doesn't undo the acceptance; the sandbox's progress shows in the view either way.
func (m *Manager) acceptedView(ctx context.Context, rec store.Sandbox) View {
	v, err := m.view(ctx, rec)
	if err != nil {
		return m.withAgent(View{Sandbox: rec, Status: StatusCreating, ForkOf: rec.ForkedFromID})
	}
	return v
}

// removeFailedVM cleans up after a failed create or fork: the VM, its network and agent
// socket go, and the record stays for its failed state. A template's VM, which carries the
// template's private image, is removed only if its labels still match (RemoveOwned).
func (m *Manager) removeFailedVM(ctx context.Context, rec store.Sandbox, cause error, fromTemplate bool) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	m.Hub.Close(rec.ID)
	m.Egress.Detach(rec.ID)
	var err error
	if fromTemplate {
		err = m.removeOwnedVM(cleanupCtx, rec)
	} else {
		err = m.Runtime.Remove(cleanupCtx, VMName(rec))
	}
	if err != nil {
		return errors.Join(cause, fmt.Errorf("removing the VM failed: %w", err))
	}
	return cause
}

// removeOwnedVM removes a sandbox's VM with RemoveOwned, which checks the labels Studio
// gave it. A runtime without RemoveOwned removes it by name.
func (m *Manager) removeOwnedVM(ctx context.Context, rec store.Sandbox) error {
	if remover, ok := m.Runtime.(interface {
		RemoveOwned(context.Context, runtime.OwnedVM) error
	}); ok {
		return remover.RemoveOwned(ctx, ownedVM(rec))
	}
	return m.Runtime.Remove(ctx, VMName(rec))
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
		if Public(rec) {
			public = append(public, rec)
		}
	}
	recs = public
	statuses, err := m.Runtime.Statuses(ctx, VMPrefix)
	if err != nil {
		return nil, err
	}
	names, err := m.Store.TemplateNames(ctx, envID)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(recs))
	for _, rec := range recs {
		st, ok := statuses[VMName(rec)]
		if !ok {
			st = runtime.StatusAbsent
		}
		out = append(out, m.withAgent(View{Sandbox: rec, Status: st, TemplateName: names[rec.TemplateID], ForkOf: rec.ForkedFromID}))
	}
	return out, nil
}

// LostVM is why a sandbox whose VM microsandbox no longer knows can't start, and what to do.
const LostVM = "microsandbox has no record of this sandbox's VM, so it can't start; its disks are kept. " +
	"If Studio repaired microsandbox's database, quit Studio and run `studio restore-msb-db` to rebuild it from the damaged one"

// Start boots a stopped sandbox.
func (m *Manager) Start(ctx context.Context, envID, id string) (View, error) {
	defer m.publish(envID, id)
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	_, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if m.failedCreation(id) {
		return View{}, lifecycleError("the sandbox could not be created; delete it")
	}
	if status, err := m.Runtime.Status(ctx, VMName(rec)); err != nil {
		return View{}, err
	} else if status == runtime.StatusSuspended {
		return View{}, lifecycleError("the sandbox is suspended; resume it instead")
	} else if status == runtime.StatusAbsent {
		return View{}, lifecycleError(LostVM)
	}
	rec, err = m.startVM(ctx, rec)
	if err != nil {
		return View{}, err
	}
	return m.view(ctx, rec)
}

// startVM starts the VM of rec, which doesn't run, with its egress and its agent's socket.
// The caller holds the sandbox's mutation lock.
func (m *Manager) startVM(ctx context.Context, rec store.Sandbox) (store.Sandbox, error) {
	if _, err := m.Egress.Attach(rec); err != nil {
		return rec, err
	}
	if err := m.Hub.Listen(rec.ID, m.Paths.AgentSocket(rec.ID)); err != nil {
		return rec, err
	}
	if err := m.Runtime.Start(ctx, VMName(rec)); err != nil {
		return rec, err
	}
	return m.markStarted(ctx, rec), nil
}

// Stop shuts a sandbox down. Its disks and catalog record are kept.
func (m *Manager) Stop(ctx context.Context, envID, id string) (View, error) {
	defer m.publish(envID, id)
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	_, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err := m.PublicSandbox(ctx, envID, id)
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
	if errors.Is(err, ErrBusy) && m.failedCreation(id) {
		// A failed create or fork shows as failed a moment before it lets go of the sandbox.
		// Its error is the sandbox's, not the delete's.
		_ = m.Wait(ctx, id)
		unlock, err = m.tryMutation(id)
	}
	if err != nil {
		return err
	}
	defer unlock()
	_, err = m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return err
	}
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return err
	}
	rec, err := m.PublicSandbox(ctx, envID, id)
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
	failed := m.failedCreation(id)
	if err := m.Store.DeleteSandbox(ctx, envID, id); err != nil {
		return err
	}
	if !failed {
		tombstone := eventFor(rec, store.EventDelete)
		tombstone.OtherSandboxID = rec.ForkedFromID
		m.recordEvent(ctx, tombstone)
	}
	m.forgetProgress(ctx, id)
	m.forget(envID, id)
	m.closeMergesOf(ctx, envID, id)
	m.publish(envID, id)
	return nil
}

// forget removes a deleted sandbox's host-side state. Failures are logged, not returned:
// the sandbox is gone, and Studio sweeps leftovers at its next start.
func (m *Manager) forget(envID, id string) {
	if m.Forget == nil {
		return
	}
	if err := m.Forget(envID, id); err != nil {
		m.Log.Error("sandbox state cleanup", "sandbox", id, "err", err)
	}
}

// WaitReady waits until the sandbox's guest agent is connected.
func (m *Manager) WaitReady(ctx context.Context, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.Hub.WaitConnected(ctx, id)
}

// Terminals lists the tmux sessions of a sandbox.
func (m *Manager) Terminals(ctx context.Context, envID, id string) ([]Terminal, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return nil, err
	}
	return m.sessions(ctx, envID, id)
}

// RenameTerminal names the tmux session name for the UI, or removes its name for "". Its
// tmux name stays, so links to it keep working.
func (m *Manager) RenameTerminal(ctx context.Context, envID, id, name, title string) error {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if err := agentproto.CheckTitle(title); err != nil {
		return fmt.Errorf("%w: %v", ErrTerminalTitle, err)
	}
	if err := m.Hub.SetSessionTitle(ctx, id, name, title); err != nil {
		return err
	}
	_, _ = m.sessions(ctx, envID, id) // announces the new label
	return nil
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
	if !Public(sb) {
		return store.Sandbox{}, store.ErrNotFound
	}
	return sb, nil
}

// Public reports whether a sandbox is part of the user-facing sandbox API. Build VMs and
// browser VMs are not: Studio drives them, and nothing else may reach them.
func Public(sb store.Sandbox) bool { return sb.BuildJobID == "" && sb.Kind == "" }

// DialPreviewTCP connects to a public sandbox's loopback port over its guest-agent
// channel. Build and browser VMs remain private even when their IDs are supplied as
// preview hosts: a preview of a browser VM would expose its DevTools and live-view ports.
func (m *Manager) DialPreviewTCP(ctx context.Context, sandboxID string, port int) (net.Conn, error) {
	sb, err := m.Store.LookupSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if !Public(sb) {
		return nil, store.ErrNotFound
	}
	return m.Hub.DialTCP(ctx, sandboxID, port)
}

func (m *Manager) view(ctx context.Context, rec store.Sandbox) (View, error) {
	st, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		m.Log.Warn("sandbox status", "sandbox", rec.ID, "vm", VMName(rec), "err", err)
		return View{}, err
	}
	name := ""
	if rec.TemplateID != "" {
		names, err := m.Store.TemplateNames(ctx, rec.EnvironmentID)
		if err != nil {
			return View{}, err
		}
		name = names[rec.TemplateID]
	}
	return m.withAgent(View{Sandbox: rec, Status: st, TemplateName: name, ForkOf: rec.ForkedFromID}), nil
}

// markStarted records that rec's VM has just started, and returns rec with that time. A
// failed write is logged: the VM runs either way, and only its uptime is lost.
func (m *Manager) markStarted(ctx context.Context, rec store.Sandbox) store.Sandbox {
	now := time.Now()
	if err := m.Store.MarkSandboxStarted(ctx, rec.EnvironmentID, rec.ID, now); err != nil {
		m.Log.Warn("record sandbox start", "sandbox", rec.ID, "err", err)
	}
	rec.StartedAt = &now
	rec.LiveCPUs, rec.LiveMemoryMiB = 0, 0
	return rec
}

func (m *Manager) withAgent(v View) View {
	v.CheckpointRestoreSupported = m.Runtime.CheckpointRestoreSupported()
	if hello, ok := m.Hub.Connected(v.ID); ok && v.Status.IsRunning() {
		v.Agent = &AgentInfo{Version: hello.Version, Arch: hello.Arch}
	}
	return m.withProgress(v)
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
