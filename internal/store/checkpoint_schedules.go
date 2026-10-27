package store

import (
	"context"
	"database/sql"
	"errors"
)

// CheckpointSchedule takes a checkpoint of a sandbox every EveryHours, or after every task
// when AfterTasks, and deletes the older ones it took (sandboxes.Manager.RunCheckpointSchedules).
type CheckpointSchedule struct {
	EnvironmentID string `json:"-"`
	SandboxID     string `json:"-"`
	EveryHours    int    `json:"everyHours" doc:"Hours between scheduled checkpoints; 0 when the schedule is off or runs after every task"`
	AfterTasks    bool   `json:"afterTasks" doc:"A checkpoint is taken after every agent turn that changed files, instead of every few hours"`
	Note          string `json:"note,omitempty" doc:"Why the last scheduled checkpoint was not taken, if it wasn't"`
}

// CheckpointSchedule returns a sandbox's schedule, with EveryHours 0 and not AfterTasks when it
// has none.
func (s *Store) CheckpointSchedule(ctx context.Context, envID, sandboxID string) (CheckpointSchedule, error) {
	if _, err := s.Sandbox(ctx, envID, sandboxID); err != nil {
		return CheckpointSchedule{}, err
	}
	cs := CheckpointSchedule{EnvironmentID: envID, SandboxID: sandboxID}
	err := s.db.QueryRowContext(ctx, "SELECT every_hours, after_tasks, note FROM checkpoint_schedules WHERE environment_id = ? AND sandbox_id = ?",
		envID, sandboxID).Scan(&cs.EveryHours, &cs.AfterTasks, &cs.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return cs, nil
	}
	return cs, err
}

// SetCheckpointSchedule sets a sandbox's schedule: every few hours, or after every task when
// afterTasks, which takes every 0. Neither turns it off. The note of an earlier attempt goes.
func (s *Store) SetCheckpointSchedule(ctx context.Context, envID, sandboxID string, every int, afterTasks bool) error {
	if _, err := s.Sandbox(ctx, envID, sandboxID); err != nil {
		return err
	}
	if afterTasks {
		every = 0
	}
	if every <= 0 && !afterTasks {
		_, err := s.db.ExecContext(ctx, "DELETE FROM checkpoint_schedules WHERE environment_id = ? AND sandbox_id = ?", envID, sandboxID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO checkpoint_schedules (sandbox_id, environment_id, every_hours, after_tasks) VALUES (?, ?, ?, ?)
		ON CONFLICT (sandbox_id) DO UPDATE SET every_hours = excluded.every_hours, after_tasks = excluded.after_tasks, note = ''`,
		sandboxID, envID, every, afterTasks)
	return err
}

// SetCheckpointScheduleNote records why the last scheduled checkpoint was not taken, or ""
// after one was.
func (s *Store) SetCheckpointScheduleNote(ctx context.Context, envID, sandboxID, note string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE checkpoint_schedules SET note = ? WHERE environment_id = ? AND sandbox_id = ?", note, envID, sandboxID)
	return err
}

// CheckpointSchedules lists every schedule, in all environments.
func (s *Store) CheckpointSchedules(ctx context.Context) ([]CheckpointSchedule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT environment_id, sandbox_id, every_hours, after_tasks, note FROM checkpoint_schedules ORDER BY sandbox_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckpointSchedule
	for rows.Next() {
		var cs CheckpointSchedule
		if err := rows.Scan(&cs.EnvironmentID, &cs.SandboxID, &cs.EveryHours, &cs.AfterTasks, &cs.Note); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}
