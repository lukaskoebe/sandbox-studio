package sandboxes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/forkmerge"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Merging a fork back into its source (docs/design/fork-merge.md): the merge is done in a
// scratch sandbox, a fresh fork of the source, and Accept replays the scratch sandbox's
// changes into the source.

var (
	// ErrMergeUnsupported means the runtime can't run commands in a VM's guest.
	ErrMergeUnsupported = errors.New("this runtime cannot merge forks")
	// ErrNotAFork means the sandbox wasn't forked from a sandbox that still exists.
	ErrNotAFork = errors.New("this sandbox wasn't forked from a sandbox that still exists")
	// ErrMergeClosed means the merge was accepted, aborted or failed.
	ErrMergeClosed = errors.New("this merge is over")
	// ErrMergeOpen means another merge into the same sandbox is open.
	ErrMergeOpen = errors.New("another merge into this sandbox is open: accept or abort it first")
	// ErrMergeNotReady means the merge is still being prepared, or has conflicts left.
	ErrMergeNotReady = errors.New("resolve the merge's conflicts first")
)

// MergeNotRunningError means a sandbox the merge needs isn't running.
type MergeNotRunningError struct{ Name string }

func (e *MergeNotRunningError) Error() string { return "start " + e.Name + " first" }

// MergeReplayError lists what changed in the source since the merge started, which Accept
// won't overwrite.
type MergeReplayError struct{ Paths []string }

func (e *MergeReplayError) Error() string {
	return "changed in the sandbox since the merge started, so nothing was merged: " + strings.Join(e.Paths, ", ")
}

// agentRunner runs studio-agent commands in a running VM as the agent user.
type agentRunner interface {
	RunAgentCommand(ctx context.Context, owned runtime.OwnedVM, args []string, stdin io.Reader, stdout io.Writer) error
}

var _ agentRunner = (*runtime.Runtime)(nil)

const (
	mergeTimeout     = 2 * time.Hour
	mergeBaseTimeout = 20 * time.Minute
	acceptTimeout    = time.Hour
)

// MergeView is a merge with the names of its sandboxes and its report.
type MergeView struct {
	store.Merge
	SourceName  string            `json:"sourceName"`
	ForkName    string            `json:"forkName"`
	ScratchName string            `json:"scratchName,omitempty"`
	Report      *forkmerge.Report `json:"report,omitempty"`
}

func (m *Manager) agentRunner() (agentRunner, error) {
	r, ok := m.Runtime.(agentRunner)
	if !ok {
		return nil, ErrMergeUnsupported
	}
	return r, nil
}

// recordMergeBase records a new fork's base, which its changes are found against later. A
// fork without one still merges, with every difference a conflict, so a failure is logged.
func (m *Manager) recordMergeBase(ctx context.Context, rec store.Sandbox) {
	r, err := m.agentRunner()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, mergeBaseTimeout)
	defer cancel()
	if err := r.RunAgentCommand(ctx, ownedVM(rec), []string{"merge-base", "--fork-id", rec.ID}, nil, io.Discard); err != nil {
		m.Log.Warn("fork merge base", "sandbox", rec.ID, "err", err)
	}
}

// StartMerge starts merging the fork forkID into the sandbox it was forked from. The fork must
// run. It answers at once; the scratch sandbox is made and the merge done in the background.
func (m *Manager) StartMerge(ctx context.Context, envID, forkID string) (MergeView, error) {
	r, err := m.agentRunner()
	if err != nil {
		return MergeView{}, err
	}
	if _, err := m.transfer(); err != nil {
		return MergeView{}, err
	}
	fork, err := m.PublicSandbox(ctx, envID, forkID)
	if err != nil {
		return MergeView{}, err
	}
	if fork.ForkedFromID == "" {
		return MergeView{}, ErrNotAFork
	}
	source, err := m.PublicSandbox(ctx, envID, fork.ForkedFromID)
	if errors.Is(err, store.ErrNotFound) {
		return MergeView{}, ErrNotAFork
	}
	if err != nil {
		return MergeView{}, err
	}
	if err := m.requireRunning(ctx, fork); err != nil {
		return MergeView{}, err
	}
	mg, err := m.Store.CreateMerge(ctx, store.Merge{EnvironmentID: envID, SourceID: source.ID, ForkID: fork.ID})
	if errors.Is(err, store.ErrConflict) {
		return MergeView{}, ErrMergeOpen
	}
	if err != nil {
		return MergeView{}, err
	}
	m.publish(envID, fork.ID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), mergeTimeout)
		defer cancel()
		m.prepareMerge(ctx, r, mg, source, fork)
	}()
	return m.mergeView(ctx, mg), nil
}

// prepareMerge makes the scratch sandbox and merges the fork into it.
func (m *Manager) prepareMerge(ctx context.Context, r agentRunner, mg store.Merge, source, fork store.Sandbox) {
	fail := func(err error) {
		m.Log.Warn("fork merge failed", "merge", mg.ID, "err", err)
		mg.State, mg.Error = store.MergeFailed, err.Error()
		if !m.saveMerge(ctx, mg) && mg.ScratchID != "" {
			m.discardScratch(ctx, mg)
		}
	}
	name, err := m.scratchName(ctx, source)
	if err != nil {
		fail(err)
		return
	}
	scratch, err := m.fork(ctx, source.EnvironmentID, source.ID, name, false)
	if err != nil {
		fail(fmt.Errorf("make the scratch sandbox: %w", err))
		return
	}
	mg.ScratchID = scratch.ID
	if !m.saveMerge(ctx, mg) {
		m.discardScratch(ctx, mg)
		return
	}
	if err := m.Wait(ctx, scratch.ID); err != nil {
		fail(fmt.Errorf("make the scratch sandbox: %w", err))
		return
	}
	scratchRec, err := m.PublicSandbox(ctx, source.EnvironmentID, scratch.ID)
	if err != nil {
		fail(err)
		return
	}
	if err := r.RunAgentCommand(ctx, ownedVM(scratchRec), []string{"merge-base", "--start", "--fork-id", fork.ID}, nil, io.Discard); err != nil {
		fail(fmt.Errorf("record the scratch sandbox: %w", err))
		return
	}
	var out bytes.Buffer
	err = pipeAgent(ctx, r,
		ownedVM(fork), []string{"merge-export", "--fork-id", fork.ID},
		ownedVM(scratchRec), []string{"merge-apply", "--fork", fork.Name, "--ours", source.Name}, &out)
	if err != nil {
		fail(err)
		return
	}
	var rep forkmerge.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		fail(fmt.Errorf("read the merge report: %w", err))
		return
	}
	mg.Report, mg.State = out.String(), mergeState(&rep)
	if !m.saveMerge(ctx, mg) {
		m.discardScratch(ctx, mg)
	}
}

// pipeAgent streams the output of one studio-agent command into another, through memory,
// and collects the second one's output.
func pipeAgent(ctx context.Context, r agentRunner, from runtime.OwnedVM, fromArgs []string, to runtime.OwnedVM, toArgs []string, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	exported := make(chan error, 1)
	go func() {
		err := r.RunAgentCommand(ctx, from, fromArgs, nil, pw)
		if err != nil {
			err = fmt.Errorf("%s in %s: %w", fromArgs[0], from.Name, err)
			cancel()
		}
		pw.CloseWithError(err)
		exported <- err
	}()
	err := r.RunAgentCommand(ctx, to, toArgs, pr, out)
	if err != nil {
		err = fmt.Errorf("%s: %w", toArgs[0], err)
		cancel()
	}
	pr.CloseWithError(errors.New("ended"))
	if exportErr := <-exported; exportErr != nil {
		return exportErr // the import fails because the export did
	}
	return err
}

func mergeState(rep *forkmerge.Report) string {
	if rep.Conflicts > 0 {
		return store.MergeConflicts
	}
	return store.MergeReady
}

// saveMerge saves an open merge, and reports false if it was closed meanwhile.
func (m *Manager) saveMerge(ctx context.Context, mg store.Merge) bool {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	err := m.Store.UpdateMerge(ctx, mg, nil)
	m.publishMerge(mg)
	if errors.Is(err, store.ErrConflict) {
		return false
	}
	if err != nil {
		m.Log.Warn("fork merge record", "merge", mg.ID, "err", err)
	}
	return true
}

// discardScratch deletes the scratch sandbox of a merge that was aborted while it was made.
func (m *Manager) discardScratch(ctx context.Context, mg store.Merge) {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	_ = m.Wait(ctx, mg.ScratchID)
	if err := m.Delete(ctx, mg.EnvironmentID, mg.ScratchID); err != nil && !errors.Is(err, store.ErrNotFound) {
		m.Log.Warn("fork merge scratch sandbox", "merge", mg.ID, "err", err)
	}
}

func (m *Manager) publishMerge(mg store.Merge) {
	for _, id := range []string{mg.SourceID, mg.ForkID, mg.ScratchID} {
		if id != "" {
			m.publish(mg.EnvironmentID, id)
		}
	}
}

// scratchName is a free name <source>-merge-<n>.
func (m *Manager) scratchName(ctx context.Context, source store.Sandbox) (string, error) {
	list, err := m.Store.Sandboxes(ctx, source.EnvironmentID)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, sb := range list {
		taken[sb.Name] = true
	}
	for n := 1; ; n++ {
		suffix := fmt.Sprintf("-merge-%d", n)
		base := source.Name
		if len(base)+len(suffix) > 40 {
			base = strings.TrimRight(base[:40-len(suffix)], "-")
		}
		if name := base + suffix; !taken[name] {
			return name, nil
		}
	}
}

func (m *Manager) requireRunning(ctx context.Context, sb store.Sandbox) error {
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return err
	}
	if status != runtime.StatusRunning {
		return &MergeNotRunningError{Name: sb.Name}
	}
	return nil
}

// Merges lists the merges sandbox id takes part in, newest first.
func (m *Manager) Merges(ctx context.Context, envID, id string) ([]MergeView, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return nil, err
	}
	list, err := m.Store.MergesOf(ctx, envID, id)
	if err != nil {
		return nil, err
	}
	out := make([]MergeView, 0, len(list))
	for _, mg := range list {
		out = append(out, m.mergeView(ctx, mg))
	}
	return out, nil
}

// GetMerge returns one merge.
func (m *Manager) GetMerge(ctx context.Context, envID, mergeID string) (MergeView, error) {
	mg, err := m.Store.Merge(ctx, envID, mergeID)
	if err != nil {
		return MergeView{}, err
	}
	return m.mergeView(ctx, mg), nil
}

func (m *Manager) mergeView(ctx context.Context, mg store.Merge) MergeView {
	v := MergeView{Merge: mg}
	name := func(id string) string {
		if id == "" {
			return ""
		}
		if sb, err := m.Store.Sandbox(ctx, mg.EnvironmentID, id); err == nil {
			return sb.Name
		}
		return ""
	}
	v.SourceName, v.ForkName, v.ScratchName = name(mg.SourceID), name(mg.ForkID), name(mg.ScratchID)
	if mg.Report != "" {
		var rep forkmerge.Report
		if json.Unmarshal([]byte(mg.Report), &rep) == nil {
			v.Report = &rep
		}
	}
	return v
}

// openMerge returns a merge that is ready or has conflicts, with its scratch sandbox.
func (m *Manager) openMerge(ctx context.Context, envID, mergeID string) (store.Merge, store.Sandbox, error) {
	mg, err := m.Store.Merge(ctx, envID, mergeID)
	if err != nil {
		return store.Merge{}, store.Sandbox{}, err
	}
	if !mg.Open() {
		return store.Merge{}, store.Sandbox{}, ErrMergeClosed
	}
	if mg.State == store.MergePreparing {
		return store.Merge{}, store.Sandbox{}, ErrMergeNotReady
	}
	scratch, err := m.PublicSandbox(ctx, envID, mg.ScratchID)
	if err != nil {
		return store.Merge{}, store.Sandbox{}, err
	}
	return mg, scratch, nil
}

// RecheckMerge counts the conflicts left in the scratch sandbox.
func (m *Manager) RecheckMerge(ctx context.Context, envID, mergeID string) (MergeView, error) {
	r, err := m.agentRunner()
	if err != nil {
		return MergeView{}, err
	}
	mg, scratch, err := m.openMerge(ctx, envID, mergeID)
	if err != nil {
		return MergeView{}, err
	}
	if err := m.requireRunning(ctx, scratch); err != nil {
		return MergeView{}, err
	}
	var out bytes.Buffer
	if err := r.RunAgentCommand(ctx, ownedVM(scratch), []string{"merge-status"}, nil, &out); err != nil {
		return MergeView{}, err
	}
	var rep forkmerge.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		return MergeView{}, fmt.Errorf("read the merge report: %w", err)
	}
	mg.Report, mg.State = out.String(), mergeState(&rep)
	if err := m.Store.UpdateMerge(ctx, mg, nil); err != nil {
		return MergeView{}, err
	}
	m.publishMerge(mg)
	return m.mergeView(ctx, mg), nil
}

// AcceptMerge checkpoints the source, replays the scratch sandbox's changes into it and
// deletes the scratch sandbox. Both must run, and no conflicts may be left. When the source
// changed a path since the merge started, nothing is replayed: a *MergeReplayError.
func (m *Manager) AcceptMerge(ctx context.Context, envID, mergeID string) (MergeView, error) {
	r, err := m.agentRunner()
	if err != nil {
		return MergeView{}, err
	}
	if _, err := m.RecheckMerge(ctx, envID, mergeID); err != nil {
		return MergeView{}, err
	}
	mg, scratch, err := m.openMerge(ctx, envID, mergeID)
	if err != nil {
		return MergeView{}, err
	}
	if mg.State != store.MergeReady {
		return MergeView{}, ErrMergeNotReady
	}
	source, err := m.PublicSandbox(ctx, envID, mg.SourceID)
	if err != nil {
		return MergeView{}, err
	}
	if err := m.requireRunning(ctx, source); err != nil {
		return MergeView{}, err
	}
	// The replay goes on if the request ends: stopping halfway would leave the source half
	// merged.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), acceptTimeout)
	defer cancel()
	forkName := ""
	if fork, err := m.Store.Sandbox(ctx, envID, mg.ForkID); err == nil {
		forkName = fork.Name
	}
	cpName, err := m.freeCheckpointName(ctx, envID, source.ID, checkpointNameBeforeMerge(forkName))
	if err != nil {
		return MergeView{}, err
	}
	cp, err := m.CreateCheckpoint(ctx, envID, source.ID, cpName)
	if err != nil {
		return MergeView{}, fmt.Errorf("checkpoint %s before the merge: %w", source.Name, err)
	}
	mg.CheckpointID = cp.ID
	unlock, err := m.tryMutation(source.ID)
	if err != nil {
		return MergeView{}, err
	}
	var out bytes.Buffer
	err = pipeAgent(ctx, r, ownedVM(scratch), []string{"merge-replay-export"}, ownedVM(source), []string{"merge-replay"}, &out)
	unlock()
	if err != nil {
		return MergeView{}, err
	}
	var result forkmerge.ReplayResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return MergeView{}, fmt.Errorf("read the replay's answer: %w", err)
	}
	if len(result.Conflicts) > 0 {
		_ = m.Store.UpdateMerge(ctx, mg, nil)
		return MergeView{}, &MergeReplayError{Paths: result.Conflicts}
	}
	cur, err := m.PublicSandbox(ctx, envID, source.ID)
	if err != nil {
		cur = source
	}
	ev := eventFor(cur, store.EventMerge)
	ev.OtherSandboxID = mg.ForkID
	mg.State = store.MergeAccepted
	if err := m.Store.UpdateMerge(ctx, mg, &ev); err != nil {
		return MergeView{}, err
	}
	if err := m.Delete(ctx, envID, scratch.ID); err != nil {
		m.Log.Warn("fork merge scratch sandbox", "merge", mg.ID, "err", err)
	}
	m.publishMerge(mg)
	return m.mergeView(ctx, mg), nil
}

func checkpointNameBeforeMerge(fork string) string {
	name := "before merging " + fork
	if fork == "" {
		name = "before a merge"
	}
	if len(name) > 56 {
		name = strings.TrimSpace(name[:56])
	}
	return name
}

// freeCheckpointName is name, or name (n) if the sandbox has a checkpoint called that.
func (m *Manager) freeCheckpointName(ctx context.Context, envID, id, name string) (string, error) {
	list, err := m.Store.Checkpoints(ctx, envID, id)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, cp := range list {
		taken[cp.Name] = true
	}
	free := name
	for n := 2; taken[free]; n++ {
		free = fmt.Sprintf("%s (%d)", name, n)
	}
	return free, nil
}

// AbortMerge ends a merge without touching the source and deletes the scratch sandbox. A
// scratch sandbox still being made is deleted once it is.
func (m *Manager) AbortMerge(ctx context.Context, envID, mergeID string) error {
	mg, err := m.Store.Merge(ctx, envID, mergeID)
	if err != nil {
		return err
	}
	if !mg.Open() && mg.State != store.MergeFailed {
		return ErrMergeClosed
	}
	if mg.Open() {
		mg.State = store.MergeAborted
		if err := m.Store.UpdateMerge(ctx, mg, nil); err != nil {
			return err
		}
	}
	m.publishMerge(mg)
	if mg.ScratchID == "" {
		return nil
	}
	err = m.Delete(ctx, envID, mg.ScratchID)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrBusy) {
		return nil // gone, or still being made: prepareMerge deletes it
	}
	return err
}

// closeMergesOf aborts the open merges a deleted sandbox took part in. The scratch sandbox of
// one whose source or fork went stays, for what was done in it.
func (m *Manager) closeMergesOf(ctx context.Context, envID, id string) {
	list, err := m.Store.MergesOf(ctx, envID, id)
	if err != nil {
		m.Log.Warn("fork merges", "sandbox", id, "err", err)
		return
	}
	for _, mg := range list {
		if !mg.Open() {
			continue
		}
		mg.State = store.MergeAborted
		if err := m.Store.UpdateMerge(ctx, mg, nil); err != nil && !errors.Is(err, store.ErrConflict) {
			m.Log.Warn("fork merges", "merge", mg.ID, "err", err)
		}
		m.publishMerge(mg)
	}
}

// recoverMerges fails the merges Studio stopped while preparing.
func (m *Manager) recoverMerges(ctx context.Context) {
	list, err := m.Store.OpenMerges(ctx)
	if err != nil {
		m.Log.Warn("fork merges", "err", err)
		return
	}
	for _, mg := range list {
		if mg.State != store.MergePreparing {
			continue
		}
		mg.State, mg.Error = store.MergeFailed, "Studio stopped while the merge was being prepared"
		if err := m.Store.UpdateMerge(ctx, mg, nil); err != nil {
			m.Log.Warn("fork merges", "merge", mg.ID, "err", err)
		}
	}
}
