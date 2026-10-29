package sandboxes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A create, fork or rebase runs in the background: the API answers at once with the sandbox
// in state creating, and its view shows the steps (docs/sandboxes.md#creation-progress).

// Steps, in the order they run.
const (
	StepPrepare   = "prepare"   // stopping the sandbox and starting it for the copy (rebase)
	StepPull      = "pull"      // pulling the image
	StepVM        = "vm"        // starting the VM
	StepCopy      = "copy"      // copying the workspace (fork)
	StepBoot      = "boot"      // running `studio-agent boot`
	StepServices  = "services"  // the supervisor starting containerd, Docker and the agent
	StepAgent     = "agent"     // the agent answering requests
	StepConfigure = "configure" // the guest acknowledging the CA and secret placeholders
)

// Operations.
const (
	OperationCreate = "create" // also a create from a template
	OperationFork   = "fork"
	// A rebase isn't saved: its own record recovers it (rebase.go). A failed one leaves the
	// sandbox as it was, so its status stays and only the progress tells of the failure.
	OperationRebase = "rebase"
	// A disk resize is a rebase onto the sandbox's own template or base (diskresize.go).
	OperationDisks = "disks"
)

// rebasing reports whether operation is a rebase or a disk resize, which leave the sandbox
// as it was when they fail.
func rebasing(operation string) bool {
	return operation == OperationRebase || operation == OperationDisks
}

// Progress states.
const (
	ProgressRunning = "running"
	ProgressFailed  = "failed"
)

// StatusCreating and StatusFailed stand in for the runtime status of a sandbox whose create
// or fork runs, or failed. A failed sandbox has no VM until it is deleted.
const (
	StatusCreating runtime.Status = "creating"
	StatusFailed   runtime.Status = "failed"
)

var (
	createSteps = []string{StepPull, StepVM, StepBoot, StepServices, StepAgent, StepConfigure}
	forkSteps   = []string{StepPull, StepVM, StepCopy, StepBoot, StepServices, StepAgent, StepConfigure}
	// A rebased sandbox's agent connects and is configured as after any start.
	rebaseRunningSteps = []string{StepPrepare, StepPull, StepVM, StepCopy, StepBoot}
	rebaseStoppedSteps = []string{StepPrepare, StepPull, StepVM, StepCopy}
)

const (
	createTimeout = 10 * time.Minute
	// A workspace copy can take a while.
	transferTimeout = 30 * time.Minute
	// A disk resize copies Docker's data too, which can be much bigger.
	diskResizeTimeout = 2 * time.Hour
	// guestTimeout bounds the wait for the agent once `studio-agent boot` ran.
	guestTimeout = 2 * time.Minute
	// Pull updates are published at most this often; step changes always are.
	pullPublishInterval = 250 * time.Millisecond
	// A create or fork is saved under this prefix plus the sandbox ID until it ends, so a
	// restart can't leave it creating forever.
	progressKeyPrefix = "sandbox.progress."
)

// Progress is where a sandbox's create, fork or rebase stands.
type Progress struct {
	Operation string        `json:"operation" enum:"create,fork,rebase,disks" doc:"disks is a disk resize"`
	State     string        `json:"state" enum:"running,failed" doc:"A failed create or fork leaves the sandbox failed; a failed rebase or disk resize leaves it as it was, in its status"`
	Steps     []string      `json:"steps" enum:"prepare,pull,vm,copy,boot,services,agent,configure" doc:"The operation's steps, in order"`
	Step      string        `json:"step" enum:"prepare,pull,vm,copy,boot,services,agent,configure" doc:"The step that runs, or the one that failed"`
	Error     string        `json:"error,omitempty" doc:"Why the step failed"`
	Pull      *PullProgress `json:"pull,omitempty" doc:"The image pull, once it reports sizes"`
}

// PullProgress counts an image pull. Totals are 0 while unknown.
type PullProgress struct {
	Layers     int   `json:"layers"`
	LayersDone int   `json:"layersDone"`
	Bytes      int64 `json:"bytes"`
	TotalBytes int64 `json:"totalBytes"`
}

// operation is a create or fork that runs, or one that failed and stays until the sandbox
// is deleted.
type operation struct {
	envID     string
	progress  Progress
	done      chan struct{} // closed once it ended
	err       error
	published time.Time // when the pull was last published
}

type savedProgress struct {
	EnvironmentID string   `json:"environmentId"`
	Progress      Progress `json:"progress"`
}

// saved reports whether an operation's progress is saved, so a restart can tell that it
// was running. A rebase has its own durable record instead.
func saved(p Progress) bool { return !rebasing(p.Operation) }

// begin records that a create, fork or rebase of rec started, at its first step.
func (m *Manager) begin(ctx context.Context, rec store.Sandbox, kind string, steps []string) error {
	p := Progress{Operation: kind, State: ProgressRunning, Steps: slices.Clone(steps), Step: steps[0]}
	if saved(p) {
		if err := m.saveProgress(ctx, rec.ID, rec.EnvironmentID, p); err != nil {
			return err
		}
	}
	m.opsMu.Lock()
	if m.ops == nil {
		m.ops = map[string]*operation{}
	}
	m.ops[rec.ID] = &operation{envID: rec.EnvironmentID, progress: p, done: make(chan struct{})}
	m.opsMu.Unlock()
	m.publish(rec.EnvironmentID, rec.ID)
	return nil
}

// advance moves the operation on sandbox id on to step. Steps only move forward.
func (m *Manager) advance(id, step string) {
	m.opsMu.Lock()
	op := m.ops[id]
	if op == nil || op.progress.State != ProgressRunning ||
		slices.Index(op.progress.Steps, step) <= slices.Index(op.progress.Steps, op.progress.Step) {
		m.opsMu.Unlock()
		return
	}
	op.progress.Step = step
	p, envID := op.progress, op.envID
	m.opsMu.Unlock()
	if saved(p) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.saveProgress(ctx, id, envID, p); err != nil {
			m.Log.Warn("sandbox progress", "sandbox", id, "err", err)
		}
	}
	m.publish(envID, id)
}

// pulled records the image pull's progress.
func (m *Manager) pulled(id string, pull runtime.PullProgress) {
	m.opsMu.Lock()
	op := m.ops[id]
	if op == nil || op.progress.State != ProgressRunning || op.progress.Step != StepPull {
		m.opsMu.Unlock()
		return
	}
	op.progress.Pull = &PullProgress{Layers: pull.Layers, LayersDone: pull.LayersDone,
		Bytes: int64(pull.Bytes), TotalBytes: int64(pull.TotalBytes)}
	publish := time.Since(op.published) >= pullPublishInterval
	if publish {
		op.published = time.Now()
	}
	envID := op.envID
	m.opsMu.Unlock()
	if publish {
		m.publish(envID, id)
	}
}

// runtimeProgress is the Spec.Progress of sandbox id's VM.
func (m *Manager) runtimeProgress(id string) func(runtime.Progress) {
	return func(p runtime.Progress) {
		switch p.Phase {
		case runtime.PhasePull:
			if p.Pull != (runtime.PullProgress{}) {
				m.pulled(id, p.Pull)
			}
		case runtime.PhaseVM:
			m.advance(id, StepVM)
		case runtime.PhaseBoot:
			m.advance(id, StepBoot)
		}
	}
}

// background runs the create or fork that begin recorded, after the request has answered.
// run removes what it made when it fails. unlock releases the sandboxes the operation holds,
// before waiters see the end.
func (m *Manager) background(rec store.Sandbox, timeout time.Duration, unlock func(), run func(context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := run(ctx)
		if err != nil {
			m.Log.Warn("sandbox operation failed", "sandbox", rec.ID, "err", err)
		}
		op := m.settle(rec, err)
		unlock()
		if op != nil {
			close(op.done)
		}
		m.publish(rec.EnvironmentID, rec.ID)
	}()
}

// settle records how the create or fork of rec ended. A successful one is forgotten; a
// failed one stays until the sandbox is deleted.
func (m *Manager) settle(rec store.Sandbox, err error) *operation {
	m.opsMu.Lock()
	op := m.ops[rec.ID]
	if op == nil {
		m.opsMu.Unlock()
		return nil
	}
	op.err = err
	if err == nil {
		delete(m.ops, rec.ID)
	} else {
		op.progress.State, op.progress.Error = ProgressFailed, err.Error()
	}
	p := op.progress
	m.opsMu.Unlock()
	if !saved(p) {
		return op
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var saveErr error
	if err == nil {
		saveErr = m.Store.DeleteSetting(ctx, progressKeyPrefix+rec.ID)
	} else {
		saveErr = m.saveProgress(ctx, rec.ID, rec.EnvironmentID, p)
	}
	if saveErr != nil {
		m.Log.Warn("sandbox progress", "sandbox", rec.ID, "err", saveErr)
	}
	return op
}

// Wait waits until the create or fork of sandbox id ends, and returns its error.
// It returns nil at once if none runs.
func (m *Manager) Wait(ctx context.Context, id string) error {
	m.opsMu.Lock()
	op := m.ops[id]
	m.opsMu.Unlock()
	if op == nil {
		return nil
	}
	select {
	case <-op.done:
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// failedCreation reports whether the create or fork of sandbox id failed, so it has no VM.
func (m *Manager) failedCreation(id string) bool {
	m.opsMu.Lock()
	defer m.opsMu.Unlock()
	op := m.ops[id]
	return op != nil && op.progress.State == ProgressFailed && !rebasing(op.progress.Operation)
}

// ClearProgress dismisses the report of a sandbox's failed rebase. A running operation and
// a failed create or fork stay: those end only with the operation or the sandbox.
func (m *Manager) ClearProgress(ctx context.Context, envID, id string) error {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	m.opsMu.Lock()
	op := m.ops[id]
	if op == nil {
		m.opsMu.Unlock()
		return nil
	}
	if op.progress.State != ProgressFailed || !rebasing(op.progress.Operation) {
		m.opsMu.Unlock()
		return lifecycleError("only a failed rebase or disk resize can be dismissed")
	}
	delete(m.ops, id)
	m.opsMu.Unlock()
	m.publish(envID, id)
	return nil
}

// forgetProgress drops what is known of a deleted sandbox's create or fork.
func (m *Manager) forgetProgress(ctx context.Context, id string) {
	m.opsMu.Lock()
	delete(m.ops, id)
	m.opsMu.Unlock()
	if err := m.Store.DeleteSetting(ctx, progressKeyPrefix+id); err != nil {
		m.Log.Warn("sandbox progress", "sandbox", id, "err", err)
	}
}

// withProgress adds a sandbox's create or fork to its view. While it runs, or after it
// failed, the sandbox's status is creating or failed.
func (m *Manager) withProgress(v View) View {
	m.opsMu.Lock()
	op := m.ops[v.ID]
	if op == nil {
		m.opsMu.Unlock()
		return v
	}
	p := op.progress
	if p.Pull != nil {
		pull := *p.Pull
		p.Pull = &pull
	}
	m.opsMu.Unlock()
	v.Progress = &p
	if rebasing(p.Operation) && p.State == ProgressFailed {
		return v // rolled back: the sandbox is as it was
	}
	v.Status, v.Agent = StatusCreating, nil
	if p.State == ProgressFailed {
		v.Status = StatusFailed
	}
	return v
}

func (m *Manager) saveProgress(ctx context.Context, id, envID string, p Progress) error {
	value, err := json.Marshal(savedProgress{EnvironmentID: envID, Progress: p})
	if err != nil {
		return err
	}
	return m.Store.SetSetting(ctx, progressKeyPrefix+id, value)
}

// recoverProgress loads the creates and forks saved at startup. One that was running when
// Studio stopped has failed: its VM is removed, as after any failed create. It returns the
// IDs of the sandboxes whose creation failed; those have no VM to attach.
func (m *Manager) recoverProgress(ctx context.Context, all []store.Sandbox) map[string]bool {
	saved, err := m.Store.SettingsWithPrefix(ctx, progressKeyPrefix)
	if err != nil {
		m.Log.Warn("sandbox progress recovery", "err", err)
		return nil
	}
	byID := make(map[string]store.Sandbox, len(all))
	for _, sb := range all {
		byID[sb.ID] = sb
	}
	failed := map[string]bool{}
	for key, value := range saved {
		id := strings.TrimPrefix(key, progressKeyPrefix)
		var rec savedProgress
		sb, ok := byID[id]
		if json.Unmarshal(value, &rec) != nil || !ok || sb.EnvironmentID != rec.EnvironmentID || len(rec.Progress.Steps) == 0 {
			if err := m.Store.DeleteSetting(ctx, key); err != nil {
				m.Log.Warn("sandbox progress recovery", "sandbox", id, "err", err)
			}
			continue
		}
		p := rec.Progress
		if p.State != ProgressFailed {
			p.State, p.Error = ProgressFailed, "Studio stopped before this step finished"
			if err := m.Runtime.Remove(ctx, VMName(sb)); err != nil {
				p.Error += fmt.Sprintf("; removing the VM failed: %v", err)
			}
			if err := m.saveProgress(ctx, id, sb.EnvironmentID, p); err != nil {
				m.Log.Warn("sandbox progress recovery", "sandbox", id, "err", err)
			}
		}
		done := make(chan struct{})
		close(done)
		m.opsMu.Lock()
		if m.ops == nil {
			m.ops = map[string]*operation{}
		}
		m.ops[id] = &operation{envID: sb.EnvironmentID, progress: p, done: done, err: errors.New(p.Error)}
		m.opsMu.Unlock()
		failed[id] = true
	}
	return failed
}

// awaitGuest follows a booted VM until its guest is ready: the supervisor started the
// agent, the agent answers, and the guest acknowledged its configuration.
func (m *Manager) awaitGuest(ctx context.Context, rec store.Sandbox) error {
	m.advance(rec.ID, StepServices)
	wait, cancel := context.WithTimeout(ctx, guestTimeout)
	defer cancel()
	if err := m.Hub.WaitConnected(wait, rec.ID); err != nil {
		return fmt.Errorf("the agent did not connect within %s: %w", guestTimeout, err)
	}
	m.advance(rec.ID, StepAgent)
	if _, err := m.Hub.Sessions(wait, rec.ID); err != nil {
		return fmt.Errorf("the agent does not answer: %w", err)
	}
	m.advance(rec.ID, StepConfigure)
	return m.ConfigureGuest(ctx, rec.EnvironmentID, rec.ID)
}

// publish tells open tabs that a sandbox changed.
func (m *Manager) publish(envID, id string) {
	if m.Bus != nil {
		m.Bus.Publish(events.Event{Topic: events.TopicSandboxes, EnvironmentID: envID, ID: id})
	}
}
