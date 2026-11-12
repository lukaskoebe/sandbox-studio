package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
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
	ErrWorkspaceTooLarge = errors.New("/workspace does not fit the workspace disk; delete what it no longer needs, or give it a bigger workspace disk, and try again")
	// ErrHomeTooLarge means the copied home directory does not fit the target's home disk.
	ErrHomeTooLarge = errors.New("home directory /home/agent does not fit the home disk; delete caches in it, or give it a bigger home disk, and try again")
	// ErrToolsTooLarge means the toolchains a rebase or fork would copy are more than toolsLimit.
	ErrToolsTooLarge = errors.New("the tools in /opt/mise are too large to copy; uninstall the versions you no longer use with mise uninstall, or leave the tools out")
	// ErrTemplateWithoutHome means a sandbox can't move to a template built before the home
	// directory existed: that template has no home disk to copy into.
	ErrTemplateWithoutHome = errors.New("this template was built before the home directory existed, so it cannot keep this sandbox's home; rebuild the template")
)

// transferRuntime is the runtime surface for moving a workspace and home into a new VM.
// A VM in transfer mode runs no agent or services until Boot.
type transferRuntime interface {
	CreateForTransfer(context.Context, string, runtime.Spec, string, map[string]string) error
	StartForTransfer(context.Context, string) error
	Boot(context.Context, string) error
	ExportTree(context.Context, runtime.OwnedVM, runtime.Tree, bool, io.Writer) error
	ImportTree(context.Context, runtime.OwnedVM, runtime.Tree, io.Reader, func(copied, total int64)) error
}

// copyReport is told how far the copy of a tree got: the bytes of its files copied, and their
// total, 0 while unknown.
type copyReport func(tree runtime.Tree, copied, total int64)

var _ transferRuntime = (*runtime.Runtime)(nil)

func (m *Manager) transfer() (transferRuntime, error) {
	rt, ok := m.Runtime.(transferRuntime)
	if !ok {
		return nil, ErrTransferUnsupported
	}
	if up, ok := rt.(treeUploader); ok && m.Transfers != nil && m.CA != nil {
		return uploadingRuntime{transferRuntime: rt, up: up, transfers: m.Transfers, ca: m.CA.CertPEM, log: m.Log}, nil
	}
	return rt, nil
}

// copyTrees copies the workspace of src into dst, then its home when dst has a home disk of
// homeMiB (0 for none). A source without a home disk gives its root disk's home, which is what
// a sandbox from a legacy template keeps there. The copies run one after the other: the
// runtime runs one command per VM at a time, and a second gets runtime.ErrRunPending.
func copyTrees(ctx context.Context, rt transferRuntime, src, dst runtime.OwnedVM, workspaceMiB, homeMiB int, report copyReport) error {
	if err := copyTree(ctx, rt, src, dst, runtime.TreeWorkspace, &limitWriter{left: int64(workspaceMiB) << 20, err: ErrWorkspaceTooLarge}, report); err != nil {
		return err
	}
	if homeMiB == 0 {
		return nil
	}
	return copyTree(ctx, rt, src, dst, runtime.TreeHome, &limitWriter{left: int64(homeMiB) << 20, err: ErrHomeTooLarge}, report)
}

// toolsLimit caps the toolchains a rebase or fork copies. They land on the target's
// root disk, which has no size of its own to check against.
const toolsLimit = 32 << 30

// copyTools merges the toolchains and global mise config of src into dst's.
func copyTools(ctx context.Context, rt transferRuntime, src, dst runtime.OwnedVM, report copyReport) error {
	return copyTree(ctx, rt, src, dst, runtime.TreeTools, &limitWriter{left: toolsLimit, err: ErrToolsTooLarge}, report)
}

// copyDocker copies Docker's data from src into dst's Docker disk of dockerMiB. Neither VM may
// have Docker running: both must be in transfer mode.
func copyDocker(ctx context.Context, rt transferRuntime, src, dst runtime.OwnedVM, dockerMiB int, report copyReport) error {
	return copyTree(ctx, rt, src, dst, runtime.TreeDocker, &limitWriter{left: int64(dockerMiB) << 20, err: ErrDockerTooLarge}, report)
}

// copyTree streams a tree of src into the empty tree of dst. Both VMs must run. The
// archive passes through the host in memory and is never stored there. It fails once the
// archive exceeds limit, or when either side fails: with limit's error if dst's disk fills up,
// as the archive's compressed size says little about what its files take there. report, if
// set, is told how far it got.
func copyTree(ctx context.Context, rt transferRuntime, src, dst runtime.OwnedVM, tree runtime.Tree, limit *limitWriter, report copyReport) error {
	var progress func(copied, total int64)
	if report != nil {
		report(tree, 0, 0)
		progress = func(copied, total int64) { report(tree, copied, total) }
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	limit.w = pw
	exported := make(chan error, 1)
	go func() {
		err := rt.ExportTree(ctx, src, tree, true, limit)
		if err != nil {
			err = fmt.Errorf("export %s: %w", tree, err)
			cancel()
		}
		pw.CloseWithError(err) // nil gives the import a clean EOF
		exported <- err
	}()
	err := rt.ImportTree(ctx, dst, tree, pr, progress)
	full := agentproto.NoSpace(err)
	if err != nil {
		err = fmt.Errorf("import %s: %w", tree, err)
		cancel()
	}
	pr.CloseWithError(fmt.Errorf("%s import ended", tree))
	exportErr := <-exported
	if full {
		return limit.err
	}
	return errors.Join(exportErr, err)
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
		WorkspaceMiB: uint32(rec.WorkspaceMiB), DockerMiB: uint32(rec.DockerMiB), HomeMiB: uint32(rec.HomeMiB),
		Image: image, Egress: egress,
	}
}

func ownedVM(rec store.Sandbox) runtime.OwnedVM {
	return runtime.OwnedVM{Name: VMName(rec), Labels: sandboxLabels(rec)}
}

// Fork creates a new sandbox with a copy of the source's workspace and, if the source has a
// home disk, of its home. The fork has its own ID, egress identity and gateway credentials,
// and the source's template and resources.
// A running source is copied live, so files written during the copy may be inconsistent.
// A stopped source is started without services for the copy and stopped again. opts adds
// Docker's data and the toolchains in /opt/mise. Docker's data needs a stopped source: its
// daemon has to be down for the copy, and a fork has no recovery record to start the source
// again after a crash, so a running one is refused rather than stopped. It can also run the
// fork's on-fork script, see startForkScript. The fork is returned in state creating; the copy
// runs in the background, holding the source, and a failed fork keeps its record in state failed.
func (m *Manager) Fork(ctx context.Context, envID, id, name string, opts ForkOptions) (View, error) {
	return m.fork(ctx, envID, id, name, opts.resolve, true)
}

// ForkOptions says what a fork does beyond copying the workspace and the home. An option left
// nil takes the source's fork default: a base's own, or else the tools and nothing else.
type ForkOptions struct {
	Docker, Tools, Script *bool
}

func (o ForkOptions) resolve(source store.Sandbox) store.ForkDefaults {
	// The tools come along unless asked otherwise: a copy without them surprises.
	d := store.ForkDefaults{Tools: true}
	if source.ForkDefaults != nil {
		d = *source.ForkDefaults
	}
	if o.Docker != nil {
		d.Docker = *o.Docker
	}
	if o.Tools != nil {
		d.Tools = *o.Tools
	}
	if o.Script != nil {
		d.Script = *o.Script
	}
	return d
}

// fork is Fork, with pick saying what it does for the source it finds. With recordBase, the fork
// records the base a merge back finds its changes against; a merge's scratch sandbox doesn't
// need one.
func (m *Manager) fork(ctx context.Context, envID, id, name string, pick func(store.Sandbox) store.ForkDefaults, recordBase bool) (View, error) {
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
	handedOff := false
	defer func() {
		if !handedOff {
			unlock()
		}
	}()
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
	opts := pick(sb)
	if opts.Docker && status != runtime.StatusStopped {
		return View{}, lifecycleError("stop the computer to copy its Docker images")
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
	// The fork pins the source's template, so it gets the source's home disk, if any.
	rec, err := m.Store.CreateSandbox(ctx, store.Sandbox{
		EnvironmentID: envID, TemplateID: sb.TemplateID, TeammateID: sb.TeammateID, Name: name,
		CPUs: sb.CPUs, MemoryMiB: sb.MemoryMiB, MaxMemoryMiB: sb.MaxMemoryMiB,
		WorkspaceMiB: sb.WorkspaceMiB, DockerMiB: sb.DockerMiB, HomeMiB: sb.HomeMiB,
		ForkedFromID: sb.ID, ForkedFromGeneration: sb.Generation,
		// A fork without a template is made from the default image, not its source's.
		BaseImage: m.BaseImage, BaseVersion: m.BaseVersion,
	})
	if err != nil {
		return View{}, err
	}
	unlockFork, err := m.tryMutation(rec.ID)
	if err != nil {
		return View{}, m.discardRecord(ctx, rec, err)
	}
	if err := m.begin(ctx, rec, OperationFork, forkSteps); err != nil {
		unlockFork()
		return View{}, m.discardRecord(ctx, rec, err)
	}
	handedOff = true
	sourceRunning := status == runtime.StatusRunning
	timeout := transferTimeout
	if opts.Docker {
		timeout = diskResizeTimeout
	}
	m.background(rec, timeout, func() { unlockFork(); unlock() }, func(ctx context.Context) error {
		if err := m.forkInto(ctx, rt, sb, rec, image, sourceRunning, opts); err != nil {
			return m.removeFailedVM(ctx, rec, err, false)
		}
		if recordBase {
			m.recordMergeBase(ctx, rec)
		}
		m.recordFork(ctx, sb, rec)
		return nil
	})
	return m.acceptedView(ctx, rec), nil
}

func (m *Manager) forkInto(ctx context.Context, rt transferRuntime, sb, rec store.Sandbox, image *runtime.ImageSource, sourceRunning bool, opts store.ForkDefaults) error {
	egress, err := m.Egress.Attach(rec)
	if err != nil {
		return err
	}
	sock := m.Paths.AgentSocket(rec.ID)
	if err := m.Hub.Listen(rec.ID, sock); err != nil {
		return err
	}
	spec := transferSpec(rec, image, egress)
	spec.Progress = m.runtimeProgress(rec.ID)
	if err := rt.CreateForTransfer(ctx, VMName(rec), spec, sock, sandboxLabels(rec)); err != nil {
		return err
	}
	m.advance(rec.ID, StepCopy)
	if !sourceRunning {
		if _, err := m.Egress.Attach(sb); err != nil {
			return err
		}
		if err := rt.StartForTransfer(ctx, VMName(sb)); err != nil {
			return errors.Join(err, m.stopQuietly(ctx, VMName(sb)))
		}
	}
	err = m.copyFork(ctx, rt, ownedVM(sb), rec, opts)
	if !sourceRunning {
		err = errors.Join(err, m.stopQuietly(ctx, VMName(sb)))
	}
	if err != nil {
		return err
	}
	m.advance(rec.ID, StepBoot)
	if err := rt.Boot(ctx, VMName(rec)); err != nil {
		return err
	}
	m.markStarted(ctx, rec)
	if err := m.awaitGuest(ctx, rec); err != nil {
		return err
	}
	if opts.Script {
		m.startForkScript(ctx, rec)
	}
	return nil
}

// forkScriptSession is the terminal a fork's script runs in.
const forkScriptSession = "on-fork"

// forkScript runs the guest's on-fork script, says how it ended and becomes the terminal's normal
// shell, so its output stays readable and the terminal stays usable. It runs through bash, so the
// file needn't be executable.
const forkScript = `f="$HOME/.config/studio/on-fork.sh"
if [ -f "$f" ]; then
	echo "Running ~/.config/studio/on-fork.sh"
	bash "$f"
	echo "on-fork.sh ended with status $?"
else
	echo "There is no ~/.config/studio/on-fork.sh to run."
fi
exec /opt/studio/bin/login-shell
`

// startForkScript starts the on-fork script in a terminal of the fork. It's only ever asked for,
// never because the file exists: an agent in the source could have written it. It doesn't wait
// for the script, and the fork is fine without it, so a failure to start is only logged.
func (m *Manager) startForkScript(ctx context.Context, rec store.Sandbox) {
	err := m.Hub.StartSession(ctx, rec.ID, agentproto.StartSession{
		Name: forkScriptSession, Command: []string{"bash", "-c", forkScript},
	})
	if err != nil {
		m.Log.Warn("on-fork script", "sandbox", rec.ID, "err", err)
	}
}

// copyFork copies what a fork takes from src into rec's VM.
func (m *Manager) copyFork(ctx context.Context, rt transferRuntime, src runtime.OwnedVM, rec store.Sandbox, opts store.ForkDefaults) error {
	report := m.copying(rec.ID)
	dst := ownedVM(rec)
	if err := copyTrees(ctx, rt, src, dst, rec.WorkspaceMiB, rec.HomeMiB, report); err != nil {
		return err
	}
	if opts.Tools {
		if err := copyTools(ctx, rt, src, dst, report); err != nil {
			return err
		}
	}
	if opts.Docker {
		return copyDocker(ctx, rt, src, dst, rec.DockerMiB, report)
	}
	return nil
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

// SetBase makes a sandbox a base with these fork defaults, or with nil an ordinary sandbox again.
func (m *Manager) SetBase(ctx context.Context, envID, id string, defaults *store.ForkDefaults) (View, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	if m.failedCreation(id) {
		return View{}, lifecycleError("the sandbox could not be created; delete it")
	}
	if err := m.Store.SetSandboxForkDefaults(ctx, envID, id, defaults); err != nil {
		return View{}, err
	}
	m.publish(envID, id)
	return m.Get(ctx, envID, id)
}
