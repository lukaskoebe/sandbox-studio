package sandboxes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// microsandbox 0.7.12 and later keep a VM's disks in a new layout. On Linux and Windows msb
// moves them as the VM starts. On macOS it moves them only when told that nothing from before
// the upgrade still writes to them, and until then it won't start or copy the VM: Studio's
// Move disks says so for the person, who confirms it, and holds the sandboxes meanwhile.

// diskMover is the runtime surface for moving the disks msb keeps in its old layout.
type diskMover interface {
	StorageStates(ctx context.Context, prefix string) ([]runtime.StorageState, error)
	MoveDisks(ctx context.Context, name string) error
}

var _ diskMover = (*runtime.Runtime)(nil)

// storageStatesTTL is the default Manager.StorageEvery.
const storageStatesTTL = 30 * time.Second

// storageStates caches, for views, the VMs whose disks msb has yet to move.
type storageStates struct {
	mu     sync.Mutex
	at     time.Time
	states map[string]runtime.StorageState // VM name → its state; ready ones aren't kept
}

// oldDiskLayout reports whether msb won't start or copy the VM vm until its disks are moved.
func (m *Manager) oldDiskLayout(vm string) bool {
	st, ok := m.storageStates()[vm]
	return ok && runtime.NeedsMove(st.Status)
}

// storageStates returns what msb says of the disks of Studio's VMs, asking at most every
// Manager.StorageEvery. A failed question is logged and counts as none to move.
func (m *Manager) storageStates() map[string]runtime.StorageState {
	mover, ok := m.Runtime.(diskMover)
	if !ok {
		return nil
	}
	c := &m.storage
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.states != nil && time.Since(c.at) < cmp.Or(m.StorageEvery, storageStatesTTL) {
		return c.states
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	states, err := mover.StorageStates(ctx, VMPrefix)
	if err != nil {
		m.Log.Warn("ask microsandbox how it keeps the sandboxes' disks", "err", err)
		states = nil
	}
	c.states = map[string]runtime.StorageState{}
	for _, st := range states {
		c.states[st.VM] = st
	}
	c.at = time.Now()
	return c.states
}

// forgetStorageStates makes the next view ask msb again.
func (m *Manager) forgetStorageStates() {
	m.storage.mu.Lock()
	m.storage.states = nil
	m.storage.mu.Unlock()
}

// checkDisksMoved returns runtime.ErrMoveDisks if msb won't start or copy rec's VM until its
// disks are moved, so a start, rebase, resize or fork stops before it changes anything. If msb
// can't say, the operation goes ahead and msb refuses it itself.
func (m *Manager) checkDisksMoved(ctx context.Context, rec store.Sandbox) error {
	mover, ok := m.Runtime.(diskMover)
	if !ok {
		return nil
	}
	vm := VMName(rec)
	states, err := mover.StorageStates(ctx, vm)
	if err != nil {
		m.Log.Warn("ask microsandbox how it keeps a sandbox's disks", "sandbox", rec.ID, "vm", vm, "err", err)
		return nil
	}
	for _, st := range states {
		if st.VM == vm && runtime.NeedsMove(st.Status) {
			m.forgetStorageStates()
			return runtime.ErrMoveDisks
		}
	}
	return nil
}

// moveDisksTimeout bounds a move of every sandbox's disks: msb copies what it can't clone.
const moveDisksTimeout = time.Hour

// DiskMove is a sandbox whose disks msb keeps in its old layout, as Move disks lists it.
type DiskMove struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environmentId"`
	Name          string `json:"name" doc:"The sandbox's name, or what Studio uses the VM for, such as a teammate's browser"`
	Status        string `json:"status" doc:"Whether its VM runs: running, suspended, stopped and so on"`
	Own           bool   `json:"own,omitempty" doc:"A VM Studio drives itself, such as a teammate's browser: it isn't started again after the move, but when Studio next needs it"`
}

// MovedDisks is what MoveDisks did with one sandbox.
type MovedDisks struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environmentId"`
	Name          string `json:"name"`
	Error         string `json:"error,omitempty" doc:"Why its disks didn't move, or why it didn't start again after they did"`
}

// ErrWritersRunning is a move asked for before the person confirmed that nothing from before
// the upgrade runs.
var ErrWritersRunning = errors.New("confirm that no Studio or msb from before the update still runs, so nothing else writes to the disks while they move")

// diskLabel is how Move disks names rec: a sandbox by its name, a VM Studio drives by what for.
func (m *Manager) diskLabel(ctx context.Context, rec store.Sandbox) string {
	switch {
	case rec.Kind == store.SandboxKindBrowser:
		if tm, err := m.Store.Teammate(ctx, rec.EnvironmentID, rec.TeammateID); err == nil && tm.Name != "" {
			return tm.Name + "'s browser"
		}
		return "a teammate's browser"
	case store.IsSystemKind(rec.Kind):
		return "Studio's " + strings.ReplaceAll(rec.Kind, "-", " ") + " VM"
	case rec.BuildJobID != "":
		return "a template build"
	}
	return rec.Name
}

// toMove returns, sorted by name, every sandbox whose disks msb won't start or copy until
// they're moved, as states says, and why a move can't start now: msb is moving some already,
// or a VM no sandbox has still runs from before the upgrade.
func (m *Manager) toMove(ctx context.Context, states []runtime.StorageState) (todo []store.Sandbox, blocked, err error) {
	all, err := m.Store.AllSandboxes(ctx)
	if err != nil {
		return nil, nil, err
	}
	byVM := map[string]store.Sandbox{}
	for _, sb := range all {
		byVM[VMName(sb)] = sb
	}
	var moving, strangers []string
	for _, st := range states {
		sb, ours := byVM[st.VM]
		switch {
		case st.Status == "in_progress" && ours:
			moving = append(moving, m.diskLabel(ctx, sb))
		case !runtime.NeedsMove(st.Status):
		case ours:
			todo = append(todo, sb)
		case st.Running:
			// A VM of Studio's that no sandbox has, such as one a rebase left, still writes.
			strangers = append(strangers, st.VM)
		}
	}
	slices.SortFunc(todo, func(a, b store.Sandbox) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), strings.Compare(a.Name, b.Name))
	})
	switch {
	case len(moving) > 0:
		slices.Sort(moving)
		return todo, lifecycleError(fmt.Sprintf("microsandbox is moving the disks of %s already; wait for it, or restart Studio", strings.Join(moving, ", "))), nil
	case len(strangers) > 0:
		slices.Sort(strangers)
		return todo, lifecycleError(fmt.Sprintf("microsandbox VMs no sandbox has still run as the older microsandbox started them: %s. "+
			"They are left over from creates, forks or rebases that failed: remove them with `msb remove --force %s` "+
			"(`studio doctor` lists them under Leftover VMs), then try again", strings.Join(strangers, ", "), strings.Join(strangers, " "))), nil
	}
	return todo, nil, nil
}

// DisksToMove lists every sandbox, in every environment, and every VM Studio drives itself,
// whose disks msb keeps in its old layout, as msb last said: what MoveDisks would move.
func (m *Manager) DisksToMove(ctx context.Context) ([]DiskMove, error) {
	cached := m.storageStates()
	states := make([]runtime.StorageState, 0, len(cached))
	for _, st := range cached {
		states = append(states, st)
	}
	// Why a move can't start now is the move's to say.
	todo, _, err := m.toMove(ctx, states)
	if err != nil || len(todo) == 0 {
		return []DiskMove{}, err
	}
	statuses, err := m.Runtime.Statuses(ctx, VMPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]DiskMove, 0, len(todo))
	for _, sb := range todo {
		status, ok := statuses[VMName(sb)]
		if !ok {
			status = runtime.StatusAbsent
		}
		out = append(out, DiskMove{ID: sb.ID, EnvironmentID: sb.EnvironmentID, Name: m.diskLabel(ctx, sb),
			Status: string(status), Own: !Public(sb)})
	}
	return out, nil
}

// MoveDisks has msb move the disks of every sandbox it won't start or copy until they're moved,
// across environments, since msb's home is shared, and of the VMs Studio drives itself, such
// as teammates' browsers. writersStopped is the person's word that no Studio or msb from before
// the update runs. Studio holds every such sandbox, stops the ones that still run as the older
// msb started them, moves each one's disks, keeping the originals, and starts again the
// sandboxes that ran; its own VMs start when it next needs them. A sandbox that fails is
// reported and the rest go on.
func (m *Manager) MoveDisks(ctx context.Context, writersStopped bool) ([]MovedDisks, error) {
	if !writersStopped {
		return nil, ErrWritersRunning
	}
	mover, ok := m.Runtime.(diskMover)
	if !ok {
		return nil, lifecycleError("this runtime can't move a sandbox's disks")
	}
	defer m.forgetStorageStates()
	states, err := mover.StorageStates(ctx, VMPrefix)
	if err != nil {
		return nil, err
	}
	todo, blocked, err := m.toMove(ctx, states)
	if err != nil {
		return nil, err
	}
	if blocked != nil {
		return nil, blocked
	}
	if len(todo) == 0 {
		return nil, nil
	}
	// Every sandbox is held before any stops, so nothing starts one of them halfway.
	var unlocks []func()
	defer func() {
		for _, unlock := range unlocks {
			unlock()
		}
	}()
	for _, sb := range todo {
		unlock, err := m.tryMutation(sb.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.diskLabel(ctx, sb), err)
		}
		unlocks = append(unlocks, unlock)
		if err := m.ensureSettled(ctx, sb.EnvironmentID, sb.ID, ""); err != nil {
			return nil, fmt.Errorf("%s: %w", m.diskLabel(ctx, sb), err)
		}
		if m.failedCreation(sb.ID) {
			return nil, lifecycleError(fmt.Sprintf("%s could not be created; delete it first", sb.Name))
		}
	}
	defer func() {
		for _, sb := range todo {
			if Public(sb) {
				m.publish(sb.EnvironmentID, sb.ID)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), moveDisksTimeout)
	defer cancel()
	out := make([]MovedDisks, len(todo))
	for n, sb := range todo {
		out[n] = MovedDisks{ID: sb.ID, EnvironmentID: sb.EnvironmentID, Name: m.diskLabel(ctx, sb)}
	}
	// Every sandbox stops before any disk moves: on macOS msb moves disks only while no VM
	// from before the upgrade runs. One that doesn't stop is reported; msb then refuses the
	// moves it would write beside, and says so for each.
	ran := map[string]bool{}
	for n, sb := range todo {
		status, err := m.Runtime.Status(ctx, VMName(sb))
		if err != nil {
			out[n].Error = "Studio could not tell whether it runs: " + err.Error()
			continue
		}
		if status == runtime.StatusStopped || status == runtime.StatusAbsent || status == runtime.StatusCrashed {
			continue
		}
		ran[sb.ID] = status.IsRunning() && Public(sb)
		m.Log.Info("stopping a sandbox to move its disks", "sandbox", sb.ID, "vm", VMName(sb))
		if err := m.Runtime.Stop(ctx, VMName(sb)); err != nil {
			out[n].Error = "it did not stop: " + err.Error()
		}
	}
	for n, sb := range todo {
		if out[n].Error != "" {
			continue
		}
		m.Log.Warn("moving a sandbox's disks to microsandbox's new layout", "sandbox", sb.ID, "vm", VMName(sb))
		if err := mover.MoveDisks(ctx, VMName(sb)); err != nil {
			m.Log.Error("move a sandbox's disks", "sandbox", sb.ID, "vm", VMName(sb), "err", err)
			out[n].Error = err.Error()
			continue
		}
		m.Log.Info("moved a sandbox's disks", "sandbox", sb.ID, "vm", VMName(sb))
	}
	m.forgetStorageStates()
	m.vmsChanged()
	for n, sb := range todo {
		if out[n].Error != "" || !ran[sb.ID] {
			continue
		}
		if _, err := m.startVM(ctx, sb); err != nil {
			m.Log.Error("start a sandbox after moving its disks", "sandbox", sb.ID, "err", err)
			out[n].Error = "its disks moved, but it did not start again: " + err.Error()
		}
	}
	return out, nil
}
