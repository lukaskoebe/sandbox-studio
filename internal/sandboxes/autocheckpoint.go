package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// CheckpointScheduleHours are the intervals a checkpoint schedule can have; 0 turns it off.
var CheckpointScheduleHours = []int{0, 1, 4, 12, 24}

// ErrCheckpointSchedule means a schedule interval is not one of CheckpointScheduleHours.
var ErrCheckpointSchedule = errors.New("a checkpoint schedule runs every 1, 4, 12 or 24 hours or after every task, or 0 to turn it off")

// Scheduled checkpoints the schedule keeps: the newest few, and the newest of each day
// before today for a week. Every checkpoint holds a full copy of the sandbox's disks, so
// this bounds a sandbox at 3 + 7 of them.
const (
	autoCheckpointsKept = 3
	autoCheckpointDays  = 7
)

// autoCheckpointRetry is how long the schedule waits after a checkpoint failed to be taken.
const autoCheckpointRetry = 15 * time.Minute

// CheckpointSchedule returns a sandbox's checkpoint schedule.
func (m *Manager) CheckpointSchedule(ctx context.Context, envID, id string) (store.CheckpointSchedule, error) {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return store.CheckpointSchedule{}, err
	}
	return m.Store.CheckpointSchedule(ctx, envID, id)
}

// SetCheckpointSchedule sets a sandbox's checkpoint schedule: every few hours, or after every
// task when afterTasks, with everyHours 0. Turning it off keeps the checkpoints it took.
func (m *Manager) SetCheckpointSchedule(ctx context.Context, envID, id string, everyHours int, afterTasks bool) (store.CheckpointSchedule, error) {
	if !slices.Contains(CheckpointScheduleHours, everyHours) || afterTasks && everyHours != 0 {
		return store.CheckpointSchedule{}, ErrCheckpointSchedule
	}
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return store.CheckpointSchedule{}, err
	}
	defer m.publish(envID, id)
	if err := m.Store.SetCheckpointSchedule(ctx, envID, id, everyHours, afterTasks); err != nil {
		return store.CheckpointSchedule{}, err
	}
	m.autoFailed.Delete(id)
	if everyHours > 0 {
		select {
		case m.scheduleKick() <- struct{}{}: // The first one is taken now, not on the next tick.
		default:
		}
	}
	return m.Store.CheckpointSchedule(ctx, envID, id)
}

// TaskDone tells the checkpoint schedules that an agent finished a task in the sandbox that
// changed its files: a schedule that runs after every task takes a checkpoint now, or, while
// another operation runs, on the next minute. Studio forgets it when it stops.
func (m *Manager) TaskDone(sandboxID string) {
	m.tasksDone.Store(sandboxID, time.Now())
	select {
	case m.scheduleKick() <- struct{}{}:
	default:
	}
}

// scheduleKick wakes RunCheckpointSchedules before its next tick.
func (m *Manager) scheduleKick() chan struct{} {
	m.kickOnce.Do(func() { m.kick = make(chan struct{}, 1) })
	return m.kick
}

// RunCheckpointSchedules takes the scheduled checkpoints until ctx ends, checking every minute.
func (m *Manager) RunCheckpointSchedules(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	kick := m.scheduleKick()
	for {
		m.runCheckpointSchedules(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-kick:
		}
	}
}

func (m *Manager) runCheckpointSchedules(ctx context.Context, now time.Time) {
	schedules, err := m.Store.CheckpointSchedules(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.Log.Warn("checkpoint schedules", "err", err)
		}
		return
	}
	for _, cs := range schedules {
		if ctx.Err() != nil {
			return
		}
		m.scheduledCheckpoint(ctx, cs, now)
	}
}

// scheduledCheckpoint takes a sandbox's checkpoint if one is due and deletes the scheduled
// ones the schedule no longer keeps. A stopped sandbox gets one only if it ran since the
// last, so its disks may have changed; a suspended one waits until it resumes. After every
// task, one is due when a task ended since the last (TaskDone), running or stopped.
func (m *Manager) scheduledCheckpoint(ctx context.Context, cs store.CheckpointSchedule, now time.Time) {
	if failed, ok := m.autoFailed.Load(cs.SandboxID); ok && now.Sub(failed.(time.Time)) < autoCheckpointRetry {
		return
	}
	sb, err := m.PublicSandbox(ctx, cs.EnvironmentID, cs.SandboxID)
	if err != nil {
		return // Deleted, or still being created.
	}
	checkpoints, err := m.Store.Checkpoints(ctx, cs.EnvironmentID, cs.SandboxID)
	if err != nil {
		return
	}
	var last *store.Checkpoint
	for i, cp := range checkpoints {
		if cp.Auto && (last == nil || cp.CreatedAt.After(last.CreatedAt)) {
			last = &checkpoints[i]
		}
	}
	done, _ := m.tasksDone.Load(cs.SandboxID)
	if cs.AfterTasks {
		if done == nil || last != nil && !done.(time.Time).After(last.CreatedAt) {
			return
		}
	} else if last != nil && now.Sub(last.CreatedAt) < time.Duration(cs.EveryHours)*time.Hour {
		return
	}
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return
	}
	switch status {
	case runtime.StatusRunning:
	case runtime.StatusStopped, runtime.StatusCrashed:
		if !cs.AfterTasks && last != nil && (sb.StartedAt == nil || !sb.StartedAt.After(last.CreatedAt)) {
			return
		}
	default:
		return
	}

	if _, err := m.takeAutoCheckpoint(ctx, cs, now); err != nil {
		if errors.Is(err, ErrBusy) || ctx.Err() != nil {
			return // Another operation is running; the next minute tries again.
		}
		m.autoFailed.Store(cs.SandboxID, now)
		m.Log.Warn("scheduled checkpoint", "sandbox", cs.SandboxID, "err", err)
		m.setScheduleNote(ctx, cs, err.Error())
		return
	}
	m.autoFailed.Delete(cs.SandboxID)
	if done != nil {
		m.tasksDone.CompareAndDelete(cs.SandboxID, done) // A task that ended since gets one of its own.
	}
	m.setScheduleNote(ctx, cs, "")
	m.pruneAutoCheckpoints(ctx, cs, now)
}

// takeAutoCheckpoint names the checkpoint after the time it is taken, in local time.
func (m *Manager) takeAutoCheckpoint(ctx context.Context, cs store.CheckpointSchedule, now time.Time) (store.Checkpoint, error) {
	name := "Auto " + now.Format("2006-01-02 15:04")
	for n := 2; ; n++ {
		cp, err := m.createCheckpoint(ctx, cs.EnvironmentID, cs.SandboxID, name, true)
		if !errors.Is(err, store.ErrExists) || n > 9 {
			return cp, err
		}
		name = fmt.Sprintf("Auto %s (%d)", now.Format("2006-01-02 15:04"), n)
	}
}

func (m *Manager) setScheduleNote(ctx context.Context, cs store.CheckpointSchedule, note string) {
	if note == cs.Note {
		return
	}
	if err := m.Store.SetCheckpointScheduleNote(ctx, cs.EnvironmentID, cs.SandboxID, note); err != nil {
		m.Log.Warn("scheduled checkpoint note", "sandbox", cs.SandboxID, "err", err)
		return
	}
	m.publish(cs.EnvironmentID, cs.SandboxID)
}

func (m *Manager) pruneAutoCheckpoints(ctx context.Context, cs store.CheckpointSchedule, now time.Time) {
	checkpoints, err := m.Store.Checkpoints(ctx, cs.EnvironmentID, cs.SandboxID)
	if err != nil {
		return
	}
	for _, cp := range prunedAutoCheckpoints(checkpoints, now) {
		if err := m.DeleteCheckpoint(ctx, cs.EnvironmentID, cs.SandboxID, cp.ID); err != nil {
			m.Log.Warn("delete a scheduled checkpoint", "sandbox", cs.SandboxID, "checkpoint", cp.Name, "err", err)
		}
	}
}

// prunedAutoCheckpoints are the ready scheduled checkpoints the schedule no longer keeps:
// all but the newest autoCheckpointsKept and the newest of each of the autoCheckpointDays
// days before now's, in now's time zone. The user's own checkpoints never go.
func prunedAutoCheckpoints(checkpoints []store.Checkpoint, now time.Time) []store.Checkpoint {
	var auto []store.Checkpoint
	for _, cp := range checkpoints {
		if cp.Auto && cp.State == store.CheckpointStateReady {
			auto = append(auto, cp)
		}
	}
	slices.SortFunc(auto, func(a, b store.Checkpoint) int { return b.CreatedAt.Compare(a.CreatedAt) })
	today := day(now, now)
	keptDays := map[time.Time]bool{}
	var pruned []store.Checkpoint
	for i, cp := range auto {
		d := day(cp.CreatedAt, now)
		daysAgo := int(today.Sub(d).Hours()/24 + 0.5) // DST days have 23 or 25 hours
		switch {
		case i < autoCheckpointsKept:
			keptDays[d] = true
		case daysAgo >= 1 && daysAgo <= autoCheckpointDays && !keptDays[d]:
			keptDays[d] = true
		default:
			pruned = append(pruned, cp)
		}
	}
	return pruned
}

// day is the midnight starting t's day in now's time zone.
func day(t, now time.Time) time.Time {
	t = t.In(now.Location())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
}
