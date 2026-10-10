package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/diskspace"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const (
	// importTimeout bounds one import after its upload, including a template build.
	importTimeout = 3 * time.Hour
	// importDiskReserve is free space an upload must leave on the data disk.
	importDiskReserve = 1 << 30
	// importRetention is how long finished import statuses stay readable.
	importRetention    = 7 * 24 * time.Hour
	maxImportNameTries = 100
)

var (
	// ErrInsufficientDisk means the data disk cannot hold an uploaded workspace.
	ErrInsufficientDisk = errors.New("not enough free disk space for this import")
	// ErrBuildsUnavailable means template builds are not configured.
	ErrBuildsUnavailable = errors.New("template builds are unavailable")
	errImportCancelled   = errors.New("import was cancelled")
)

// Builds submits template builds for imports (internal/templatebuild).
type Builds interface {
	Submit(ctx context.Context, envID, source string) (store.BuildJob, bool, error)
	Job(ctx context.Context, envID, id string) (store.BuildJob, error)
}

// importState is the in-memory side of imports: running imports and disk reservations.
type importState struct {
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	reserved uint64
}

func (s *importState) reserve(n uint64, free uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserved+n+importDiskReserve > free {
		return false
	}
	s.reserved += n
	return true
}

func (s *importState) release(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserved -= n
}

func (s *importState) track(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancels == nil {
		s.cancels = map[string]context.CancelFunc{}
	}
	s.cancels[id] = cancel
}

func (s *importState) untrack(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
}

func (s *importState) cancel(id string) {
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) importBlob(id string) string {
	return filepath.Join(m.Paths.Imports(), id+".workspace")
}

// Import reads an export file from body, stores its workspace archive opaquely under the
// data directory and finishes the import in the background. size is the body length, or
// -1 when unknown. The archive is never extracted or parsed on the host; only the guest
// reads it. The returned status is in the building state.
func (m *Manager) Import(ctx context.Context, envID string, body io.Reader, size int64) (store.SandboxImport, error) {
	if _, err := m.Store.Environment(ctx, envID); err != nil {
		return store.SandboxImport{}, err
	}
	manifest, err := ReadExportHeader(body)
	if err != nil {
		return store.SandboxImport{}, err
	}
	if _, err := m.transfer(); err != nil {
		return store.SandboxImport{}, err
	}
	if manifest.TemplateSpecYAML != nil && m.Builds == nil {
		return store.SandboxImport{}, ErrBuildsUnavailable
	}
	limit := manifest.WorkspaceLimit()
	need := uint64(limit)
	if size >= 0 && uint64(size) < need {
		need = uint64(size) // the body also holds the header, so this over-reserves slightly
	}
	if err := os.MkdirAll(m.Paths.Imports(), 0o700); err != nil {
		return store.SandboxImport{}, errors.New("import storage is unavailable")
	}
	free, err := m.freeDisk(m.Paths.Imports())
	if err == nil && !m.imports.reserve(need, free) {
		return store.SandboxImport{}, ErrInsufficientDisk
	}
	if err != nil {
		m.Log.Warn("free disk space unknown", "err", err)
		need = 0
	}
	defer m.imports.release(need)

	imp, err := m.Store.CreateSandboxImport(ctx, envID, manifest.Name)
	if err != nil {
		return store.SandboxImport{}, err
	}
	blob := m.importBlob(imp.ID)
	fail := func(cause error) (store.SandboxImport, error) {
		_ = os.Remove(blob)
		m.finishFailed(ctx, imp, cause)
		return store.SandboxImport{}, cause
	}
	if err := storeArchive(blob, body, limit, manifest.WorkspaceBytes); err != nil {
		return fail(err)
	}
	if err := m.Store.AdvanceSandboxImport(ctx, envID, imp.ID, store.ImportUploading, store.ImportBuilding, ""); err != nil {
		return fail(errImportCancelled)
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), importTimeout)
	m.imports.track(imp.ID, cancel)
	go func() {
		defer cancel()
		defer m.imports.untrack(imp.ID)
		defer os.Remove(blob)
		if err := m.finishImport(runCtx, imp, manifest, blob); err != nil {
			m.finishFailed(runCtx, imp, err)
		}
	}()
	return m.Store.SandboxImport(ctx, envID, imp.ID)
}

// storeArchive copies the rest of body to a new file, failing once it exceeds limit
// bytes. expected is the declared archive length, or 0 when unknown.
func storeArchive(blob string, body io.Reader, limit, expected int64) error {
	f, err := os.OpenFile(blob, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("import storage is unavailable")
	}
	n, err := io.Copy(f, io.LimitReader(body, limit+1))
	if closeErr := f.Close(); err == nil && closeErr != nil {
		return errors.New("import storage is unavailable")
	}
	switch {
	case err != nil:
		return fmt.Errorf("upload ended early: %w", err)
	case n > limit:
		return ErrWorkspaceTooLarge
	case n == 0:
		return invalidExport("workspace archive is missing")
	case expected != 0 && n != expected:
		return invalidExport("workspace archive length does not match workspaceBytes")
	}
	return nil
}

// finishImport resolves the template, creates the sandbox in transfer mode, streams the
// archive into its workspace and boots it. A half-made sandbox is removed on failure.
func (m *Manager) finishImport(ctx context.Context, imp store.SandboxImport, manifest ExportManifest, blob string) error {
	envID := imp.EnvironmentID
	rt, err := m.transfer()
	if err != nil {
		return err
	}
	resolved, templateID := manifest.Resources, ""
	if manifest.TemplateSpecYAML != nil {
		job, _, err := m.Builds.Submit(ctx, envID, *manifest.TemplateSpecYAML)
		if err != nil {
			return err
		}
		if err := m.Store.AdvanceSandboxImport(ctx, envID, imp.ID, store.ImportBuilding, store.ImportBuilding, job.ID); err != nil {
			return errImportCancelled
		}
		if templateID, err = m.waitForBuild(ctx, envID, job); err != nil {
			return err
		}
		if resolved, err = m.templateResources(ctx, envID, templateID); err != nil {
			return err
		}
		if resolved != manifest.Resources {
			return importError("the built template's resources differ from the export")
		}
	}
	if err := m.Store.AdvanceSandboxImport(ctx, envID, imp.ID, store.ImportBuilding, store.ImportCreating, ""); err != nil {
		return errImportCancelled
	}
	rec, err := m.createImportRecord(ctx, imp, manifest.Name, resolved, templateID)
	if err != nil {
		return err
	}
	unlock, err := m.tryMutation(rec.ID)
	if err != nil {
		return m.removeFailedFork(ctx, rec, err)
	}
	defer unlock()
	if err := m.importInto(ctx, rt, imp, rec, blob); err != nil {
		return m.removeFailedFork(ctx, rec, err)
	}
	return nil
}

func (m *Manager) importInto(ctx context.Context, rt transferRuntime, imp store.SandboxImport, rec store.Sandbox, blob string) error {
	var image *runtime.ImageSource
	if rec.TemplateID != "" {
		var err error
		if image, err = m.templateImage(ctx, rec.EnvironmentID, rec.TemplateID); err != nil {
			return err
		}
	}
	egress, err := m.Egress.Attach(rec)
	if err != nil {
		return err
	}
	sock := m.Paths.AgentSocket(rec.ID)
	if err := m.Hub.Listen(rec.ID, sock); err != nil {
		return err
	}
	if err := rt.CreateForTransfer(ctx, VMName(rec), transferSpec(rec, image, egress), sock, sandboxLabels(rec)); err != nil {
		return err
	}
	if err := m.Store.AdvanceSandboxImport(ctx, rec.EnvironmentID, imp.ID, store.ImportCreating, store.ImportImporting, ""); err != nil {
		return errImportCancelled
	}
	f, err := os.Open(blob)
	if err != nil {
		return importError("uploaded workspace is unavailable")
	}
	err = rt.ImportWorkspace(ctx, ownedVM(rec), f)
	f.Close()
	if err != nil {
		return fmt.Errorf("import workspace: %w", err)
	}
	if err := rt.Boot(ctx, VMName(rec)); err != nil {
		return err
	}
	if err := m.Store.FinishSandboxImport(ctx, rec.EnvironmentID, imp.ID, store.ImportReady, ""); err != nil {
		return errImportCancelled
	}
	return nil
}

// createImportRecord records the sandbox under the exported name, or the first free
// name-N, linked to the import in the same transaction.
func (m *Manager) createImportRecord(ctx context.Context, imp store.SandboxImport, name string, res resources.Resources, templateID string) (store.Sandbox, error) {
	for i := 1; i <= maxImportNameTries; i++ {
		candidate := importName(name, i)
		rec, err := m.Store.CreateImportedSandbox(ctx, imp.ID, store.Sandbox{
			EnvironmentID: imp.EnvironmentID, TemplateID: templateID, Name: candidate,
			CPUs: int(res.CPUs), MemoryMiB: int(res.MemoryMiB), MaxMemoryMiB: int(res.MaxMemoryMiB),
			WorkspaceMiB: int(res.WorkspaceMiB), DockerMiB: int(res.DockerMiB),
		})
		if errors.Is(err, store.ErrExists) {
			continue
		}
		if errors.Is(err, store.ErrConflict) {
			return rec, errImportCancelled
		}
		return rec, err
	}
	return store.Sandbox{}, fmt.Errorf("no free name for %q: %w", name, store.ErrExists)
}

// importName is name for the first try and name-N after that, shortened to stay valid.
func importName(name string, try int) string {
	if try == 1 {
		return name
	}
	suffix := "-" + strconv.Itoa(try)
	base := name
	if len(base)+len(suffix) > 40 {
		base = base[:40-len(suffix)]
	}
	for len(base) > 0 && base[len(base)-1] == '-' {
		base = base[:len(base)-1]
	}
	return base + suffix
}

func (m *Manager) waitForBuild(ctx context.Context, envID string, job store.BuildJob) (string, error) {
	poll := m.importPoll
	if poll == 0 {
		poll = time.Second
	}
	for {
		switch job.Status {
		case store.BuildReady:
			if job.TemplateID == "" {
				return "", importError("template build finished without a template")
			}
			return job.TemplateID, nil
		case store.BuildFailed, store.BuildCancelled:
			return "", importError("template build " + job.Status)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
		next, err := m.Builds.Job(ctx, envID, job.ID)
		if err != nil {
			return "", err
		}
		job = next
	}
}

// finishFailed records a failed import with a message safe to show; details only go to
// the log.
func (m *Manager) finishFailed(ctx context.Context, imp store.SandboxImport, cause error) {
	m.Log.Warn("sandbox import failed", "import", imp.ID, "err", cause)
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	err := m.Store.FinishSandboxImport(ctx, imp.EnvironmentID, imp.ID, store.ImportFailed, importErrorMessage(cause))
	if err != nil && !errors.Is(err, store.ErrConflict) {
		m.Log.Warn("recording failed sandbox import", "import", imp.ID, "err", err)
	}
}

func importErrorMessage(err error) string {
	var public importError
	switch {
	case errors.As(err, &public):
		return string(public)
	case errors.Is(err, errImportCancelled), errors.Is(err, context.Canceled):
		return "import was cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "import timed out"
	case errors.Is(err, store.ErrNotFound):
		return "environment or template no longer exists"
	}
	for _, known := range []error{ErrWorkspaceTooLarge, ErrInvalidExport, store.ErrExists, ErrTransferUnsupported, ErrBuildsUnavailable} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "import failed; see the Studio log for details"
}

// importError is an import failure whose text is safe to show as is.
type importError string

func (e importError) Error() string { return string(e) }

// SandboxImport returns the status of an import.
func (m *Manager) SandboxImport(ctx context.Context, envID, id string) (store.SandboxImport, error) {
	return m.Store.SandboxImport(ctx, envID, id)
}

// CancelImport marks an import failed and stops its work. A template build it started
// keeps running, since other imports or users may share it.
func (m *Manager) CancelImport(ctx context.Context, envID, id string) (store.SandboxImport, error) {
	if err := m.Store.FinishSandboxImport(ctx, envID, id, store.ImportFailed, "import was cancelled"); err != nil {
		if errors.Is(err, store.ErrConflict) {
			if _, getErr := m.Store.SandboxImport(ctx, envID, id); getErr != nil {
				return store.SandboxImport{}, getErr
			}
		}
		return store.SandboxImport{}, err
	}
	m.imports.cancel(id)
	return m.Store.SandboxImport(ctx, envID, id)
}

// recoverImports fails imports a previous Studio process left unfinished, removes their
// half-made sandboxes and deletes every staged archive.
func (m *Manager) recoverImports(ctx context.Context) {
	active, err := m.Store.ActiveSandboxImports(ctx)
	if err != nil {
		m.Log.Warn("import recovery", "err", err)
		return
	}
	for _, imp := range active {
		if err := m.Store.FinishSandboxImport(ctx, imp.EnvironmentID, imp.ID, store.ImportFailed, "Studio restarted during this import"); err != nil {
			m.Log.Warn("import recovery", "import", imp.ID, "err", err)
		}
		if imp.SandboxID == "" {
			continue
		}
		sb, err := m.Store.Sandbox(ctx, imp.EnvironmentID, imp.SandboxID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err == nil {
			err = m.Runtime.Remove(ctx, VMName(sb))
		}
		if err == nil {
			err = m.Store.DeleteSandbox(ctx, imp.EnvironmentID, imp.SandboxID)
		}
		if err != nil {
			m.Log.Warn("import recovery kept a half-made sandbox", "import", imp.ID, "sandbox", imp.SandboxID, "err", err)
		}
	}
	entries, err := os.ReadDir(m.Paths.Imports())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.Log.Warn("import recovery", "err", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(m.Paths.Imports(), e.Name())); err != nil {
			m.Log.Warn("import recovery", "err", err)
		}
	}
	if err := m.Store.PruneSandboxImports(ctx, time.Now().Add(-importRetention)); err != nil {
		m.Log.Warn("import recovery", "err", err)
	}
}

func (m *Manager) freeDisk(dir string) (uint64, error) {
	if m.diskFree != nil {
		return m.diskFree(dir)
	}
	return diskspace.Free(dir)
}
