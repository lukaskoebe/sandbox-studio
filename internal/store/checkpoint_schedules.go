package store

import (
	"context"
	"database/sql"
	"errors"
)

// CheckpointSchedule takes a checkpoint of a sandbox every EveryHours and deletes the older
// ones it took (sandboxes.Manager.RunCheckpointSchedules).
type CheckpointSchedule struct {
	EnvironmentID string `json:"-"`
	SandboxID     string `json:"-"`
	EveryHours    int    `json:"everyHours" doc:"Hours between scheduled checkpoints; 0 when the schedule is off"`
	Note          string `json:"note,omitempty" doc:"Why the last scheduled checkpoint was not taken, if it wasn't"`
}

// CheckpointSchedule returns a sandbox's schedule, with EveryHours 0 when it has none.
func (s *Store) CheckpointSchedule(ctx context.Context, envID, sandboxID string) (CheckpointSchedule, error) {
	if _, err := s.Sandbox(ctx, envID, sandboxID); err != nil {
		return CheckpointSchedule{}, err
	}
	cs := CheckpointSchedule{EnvironmentID: envID, SandboxID: sandboxID}
	err := s.db.QueryRowContext(ctx, "SELECT every_hours, note FROM checkpoint_schedules WHERE environment_id = ? AND sandbox_id = ?",
		envID, sandboxID).Scan(&cs.EveryHours, &cs.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return cs, nil
	}
	return cs, err
}

// SetCheckpointSchedule sets a sandbox's schedule; every 0 turns it off. The note of an
// earlier attempt goes.
func (s *Store) SetCheckpointSchedule(ctx context.Context, envID, sandboxID string, every int) error {
	if _, err := s.Sandbox(ctx, envID, sandboxID); err != nil {
		return err
	}
	if every <= 0 {
		_, err := s.db.ExecContext(ctx, "DELETE FROM checkpoint_schedules WHERE environment_id = ? AND sandbox_id = ?", envID, sandboxID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO checkpoint_schedules (sandbox_id, environment_id, every_hours) VALUES (?, ?, ?)
		ON CONFLICT (sandbox_id) DO UPDATE SET every_hours = excluded.every_hours, note = ''`, sandboxID, envID, every)
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
	rows, err := s.db.QueryContext(ctx, "SELECT environment_id, sandbox_id, every_hours, note FROM checkpoint_schedules ORDER BY sandbox_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckpointSchedule
	for rows.Next() {
		var cs CheckpointSchedule
		if err := rows.Scan(&cs.EnvironmentID, &cs.SandboxID, &cs.EveryHours, &cs.Note); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}
