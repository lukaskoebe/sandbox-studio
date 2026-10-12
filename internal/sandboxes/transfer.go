package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

var (
	// ErrTransferUnsupported means the runtime cannot copy workspaces between VMs.
	ErrTransferUnsupported = errors.New("this runtime cannot copy workspaces")
	// ErrLifecycleState means the sandbox's state does not allow the operation: rebase and
	// fork need it running or stopped (so they refuse a suspended one), suspend needs it
	// running, resume needs it suspended, and start refuses a suspended one.
	ErrLifecycleState = errors.New("sandbox must be running or stopped")
	// ErrWorkspaceTooLarge means the copied workspace does not fit the target's disk.
	ErrWorkspaceTooLarge = errors.New("workspace does not fit the target disk")
	// ErrHomeTooLarge means the copied home directory does not fit the target's home disk.
	ErrHomeTooLarge = fmt.Errorf("home directory /home/agent does not fit the %d GiB home disk; delete caches in it and try again", HomeMiB>>10)
)

// transferRuntime is the runtime surface for moving a workspace and home into a new VM.
// A VM in transfer mode runs no agent or services until Boot.
type transferRuntime interface {
	CreateForTransfer(context.Context, string, runtime.Spec, string, map[string]string) error
	StartForTransfer(context.Context, string) error
	Boot(context.Context, string) error
	ExportTree(context.Context, runtime.OwnedVM, runtime.Tree, io.Writer) error
	ImportTree(context.Context, runtime.OwnedVM, runtime.Tree, io.Reader) error
}

var _ transferRuntime = (*runtime.Runtime)(nil)

func (m *Manager) transfer() (transferRuntime, error) {
	rt, ok := m.Runtime.(transferRuntime)
	if !ok {
		return nil, ErrTransferUnsupported
	}
	return rt, nil
}

// copyTrees copies the workspace and then the home of src into dst, which has a home
// disk. A source without one (created before the home disk) gives its root disk's home.
func copyTrees(ctx context.Context, rt transferRuntime, src, dst runtime.OwnedVM, workspaceMiB int) error {
	if err := copyTree(ctx, rt, src, dst, runtime.TreeWorkspace, &limitWriter{left: int64(workspaceMiB) << 20, err: ErrWorkspaceTooLarge}); err != nil {
		return err
	}
	return copyTree(ctx, rt, src, dst, runtime.TreeHome, &limitWriter{left: HomeMiB << 20, err: ErrHomeTooLarge})
}

// copyTree streams a tree of src into the empty tree of dst. Both VMs must run. The
// archive passes through the host in memory and is never stored there. It fails once the
// archive exceeds limit, or when either side fails.
func copyTree(ctx context.Context, rt transferRuntime, src, dst runtime.OwnedVM, tree runtime.Tree, limit *limitWriter) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	limit.w = pw
	exported := make(chan error, 1)
	go func() {
		err := rt.ExportTree(ctx, src, tree, limit)
		if err != nil {
			err = fmt.Errorf("export %s: %w", tree, err)
			cancel()
		}
		pw.CloseWithError(err) // nil gives the import a clean EOF
		exported <- err
	}()
	err := rt.ImportTree(ctx, dst, tree, pr)
	if err != nil {
		err = fmt.Errorf("import %s: %w", tree, err)
		cancel()
	}
	pr.CloseWithError(fmt.Errorf("%s import ended", tree))
	return errors.Join(<-exported, err)
}

// limitWriter fails with err once more than left bytes are written.
type limitWriter struct {
	w    io.Writer
	left int64
	err  error
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, l.err
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// transferSpec is the VM spec of rec, with the same network and egress as bootSandbox.
func transferSpec(rec store.Sandbox, image *runtime.ImageSource, egress runtime.Egress) runtime.Spec {
	return runtime.Spec{
		CPUs: uint8(rec.CPUs), MemoryMiB: uint32(rec.MemoryMiB), MaxMemoryMiB: uint32(rec.MaxMemoryMiB),
		WorkspaceMiB: uint32(rec.WorkspaceMiB), DockerMiB: uint32(rec.DockerMiB), HomeMiB: HomeMiB,
		Image: image, Egress: egress,
	}
}

func ownedVM(rec store.Sandbox) runtime.OwnedVM {
	return runtime.OwnedVM{Name: VMName(rec), Labels: sandboxLabels(rec)}
}

// Fork creates a new sandbox with a copy of the source's workspace and home. The fork has its
// own ID, egress identity and gateway credentials, and the source's template and resources.
// A running source is copied live, so files written during the copy may be inconsistent.
// A stopped source is started without services for the copy and stopped again. Docker
// state is not copied. A failed fork is removed.
func (m *Manager) Fork(ctx context.Context, envID, id, name string) (View, error) {
	if err := runtime.ValidName(name); err != nil {
		return View{}, err
	}
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	rt, err := m.transfer()
	if err != nil {
		return View{}, err
	}
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return View{}, err
	}
	if status != runtime.StatusRunning && status != runtime.StatusStopped {
		return View{}, ErrLifecycleState
	}
	var image *runtime.ImageSource
	if sb.TemplateID != "" {
		if _, err := m.templateResources(ctx, envID, sb.TemplateID); err != nil {
			return View{}, err
		}
		// The source pins the template, so it stays until the fork pins it too.
		if image, err = m.templateImage(ctx, envID, sb.TemplateID); err != nil {
			return View{}, err
		}
	}
	rec, err := m.Store.CreateSandbox(ctx, store.Sandbox{
		EnvironmentID: envID, TemplateID: sb.TemplateID, TeammateID: sb.TeammateID, Name: name,
		CPUs: sb.CPUs, MemoryMiB: sb.MemoryMiB, MaxMemoryMiB: sb.MaxMemoryMiB,
		WorkspaceMiB: sb.WorkspaceMiB, DockerMiB: sb.DockerMiB,
	})
	if err != nil {
		return View{}, err
	}
	unlockFork, err := m.tryMutation(rec.ID)
	if err != nil {
		return View{}, err
	}
	defer unlockFork()
	if err := m.forkInto(ctx, rt, sb, rec, image, status == runtime.StatusRunning); err != nil {
		return View{}, m.removeFailedFork(ctx, rec, err)
	}
	return m.view(ctx, rec)
}

func (m *Manager) forkInto(ctx context.Context, rt transferRuntime, sb, rec store.Sandbox, image *runtime.ImageSource, sourceRunning bool) error {
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
	if !sourceRunning {
		if _, err := m.Egress.Attach(sb); err != nil {
			return err
		}
		if err := rt.StartForTransfer(ctx, VMName(sb)); err != nil {
			return errors.Join(err, m.stopQuietly(ctx, VMName(sb)))
		}
	}
	err = copyTrees(ctx, rt, ownedVM(sb), ownedVM(rec), rec.WorkspaceMiB)
	if !sourceRunning {
		err = errors.Join(err, m.stopQuietly(ctx, VMName(sb)))
	}
	if err != nil {
		return err
	}
	return rt.Boot(ctx, VMName(rec))
}

// stopQuietly stops a VM even when ctx has ended.
func (m *Manager) stopQuietly(ctx context.Context, name string) error {
	stopCtx, cancel := cleanupContext(ctx)
	defer cancel()
	return m.Runtime.Stop(stopCtx, name)
}

func (m *Manager) removeFailedFork(ctx context.Context, rec store.Sandbox, cause error) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	if err := m.Runtime.Remove(cleanupCtx, VMName(rec)); err != nil {
		return errors.Join(cause, fmt.Errorf("fork cleanup failed; its record is kept: %w", err))
	}
	m.Hub.Close(rec.ID)
	m.Egress.Detach(rec.ID)
	if err := m.Store.DeleteSandbox(cleanupCtx, rec.EnvironmentID, rec.ID); err != nil {
		return errors.Join(cause, fmt.Errorf("fork record cleanup failed: %w", err))
	}
	return cause
}
