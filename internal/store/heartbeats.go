package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Heartbeat is a teammate's check-in schedule: every EveryHours hours, while the local hour
// is in [FromHour, ToHour). Teammates have one, or none while it is off.
type Heartbeat struct {
	EveryHours int `json:"everyHours" minimum:"1" maximum:"24" doc:"Hours between check-ins"`
	FromHour   int `json:"fromHour" minimum:"0" maximum:"23" doc:"The first local hour a check-in may start in"`
	ToHour     int `json:"toHour" minimum:"1" maximum:"24" doc:"The local hour the check-ins stop before"`
}

// Beat outcomes.
const (
	BeatStarted = "started"
	BeatSkipped = "skipped"
)

// Beat is a check-in of a teammate's heartbeat schedule that the log keeps.
type Beat struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"-"`
	TeammateID    string    `json:"teammateId"`
	At            time.Time `json:"at"`
	Outcome       string    `json:"outcome" enum:"started,skipped"`
	Reason        string    `json:"reason,omitempty" doc:"Why the check-in was skipped"`
	ChatID        string    `json:"chatId,omitempty" doc:"The heartbeat chat it started"`
}

// SetHeartbeat turns a teammate's check-in schedule on, or off with nil. Turning it off keeps
// the window, so turning it on again restores it only when h gives it.
func (s *Store) SetHeartbeat(ctx context.Context, envID, id string, h *Heartbeat) (Teammate, error) {
	every, from, to := 0, 0, 0
	if h != nil {
		every, from, to = h.EveryHours, h.FromHour, h.ToHour
	}
	var res sql.Result
	var err error
	if h == nil {
		res, err = s.db.ExecContext(ctx, "UPDATE teammates SET heartbeat_every = 0, updated_at = ? WHERE environment_id = ? AND id = ?", now(), envID, id)
	} else {
		res, err = s.db.ExecContext(ctx, "UPDATE teammates SET heartbeat_every = ?, heartbeat_from = ?, heartbeat_to = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
			every, from, to, now(), envID, id)
	}
	if err != nil {
		return Teammate{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Teammate{}, ErrNotFound
	}
	return s.Teammate(ctx, envID, id)
}

// RecordBeat notes a check-in of b's teammate at b.At, which the schedule counts from. A
// check-in with an outcome is logged as well; one with none (nothing to do) only moves the
// schedule.
func (s *Store) RecordBeat(ctx context.Context, b Beat) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE teammates SET heartbeat_at = ? WHERE environment_id = ? AND id = ?", b.At.Unix(), b.EnvironmentID, b.TeammateID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if b.Outcome != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO teammate_heartbeats (id, environment_id, teammate_id, at, outcome, reason, chat_id)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, NewID(), b.EnvironmentID, b.TeammateID, b.At.Unix(), b.Outcome, b.Reason, nullable(b.ChatID))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Beats lists a teammate's logged check-ins, newest first. limit defaults to 20.
func (s *Store) Beats(ctx context.Context, envID, teammateID string, limit int) ([]Beat, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, environment_id, teammate_id, at, outcome, reason, IFNULL(chat_id, '')
		FROM teammate_heartbeats WHERE environment_id = ? AND teammate_id = ? ORDER BY at DESC, rowid DESC LIMIT ?`, envID, teammateID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Beat{}
	for rows.Next() {
		var b Beat
		var at int64
		if err := rows.Scan(&b.ID, &b.EnvironmentID, &b.TeammateID, &at, &b.Outcome, &b.Reason, &b.ChatID); err != nil {
			return nil, err
		}
		b.At = time.Unix(at, 0)
		out = append(out, b)
	}
	return out, rows.Err()
}

// RunningHeartbeats counts a teammate's heartbeat chats that are running a turn.
func (s *Store) RunningHeartbeats(ctx context.Context, envID, teammateID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats WHERE environment_id = ? AND teammate_id = ? AND heartbeat = 1 AND state = ?",
		envID, teammateID, ChatRunning).Scan(&n)
	return n, err
}

// PendingApprovals counts the approvals of a sandbox that wait for a decision.
func (s *Store) PendingApprovals(ctx context.Context, envID, sandboxID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM approvals WHERE environment_id = ? AND sandbox_id = ? AND status = 'pending'",
		envID, sandboxID).Scan(&n)
	return n, err
}

// TeammateSandbox returns a teammate's most recently created sandbox that users see: not a
// build VM or a browser VM. ErrNotFound if it has none.
func (s *Store) TeammateSandbox(ctx context.Context, envID, teammateID string) (Sandbox, error) {
	sb, err := scanSandbox(s.db.QueryRowContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE environment_id = ? AND teammate_id = ? AND IFNULL(build_job_id, '') = '' AND kind = '' ORDER BY created_at DESC, name LIMIT 1",
		envID, teammateID))
	if errors.Is(err, sql.ErrNoRows) {
		return sb, ErrNotFound
	}
	return sb, err
}
