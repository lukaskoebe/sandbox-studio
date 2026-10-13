package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Task states.
const (
	TaskOpen       = "open"
	TaskInProgress = "in_progress"
	TaskDone       = "done"
)

// Task creators: the user, or the teammate through its task_create tool.
const (
	TaskByUser     = "user"
	TaskByTeammate = "teammate"
)

// ErrTooLarge refuses a write that would make a task's details larger than its limit.
var ErrTooLarge = errors.New("too large")

// Task is an item of a teammate's task list (docs/teammates.md). Details are markdown.
type Task struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"-"`
	TeammateID    string    `json:"teammateId"`
	Title         string    `json:"title"`
	Details       string    `json:"details,omitempty"`
	Status        string    `json:"status" enum:"open,in_progress,done"`
	Due           string    `json:"due,omitempty" doc:"A day, YYYY-MM-DD"`
	CreatedBy     string    `json:"createdBy" enum:"user,teammate" doc:"teammate: the teammate created it for itself"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const taskCols = "id, environment_id, teammate_id, title, details, status, due, created_by, created_at, updated_at"

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var created, updated int64
	err := row.Scan(&t.ID, &t.EnvironmentID, &t.TeammateID, &t.Title, &t.Details, &t.Status, &t.Due, &t.CreatedBy, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.CreatedAt, t.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return t, err
}

// CreateTask inserts t, a task of its teammate, with a new ID unless it has one.
func (s *Store) CreateTask(ctx context.Context, t Task) (Task, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	at := now()
	_, err := s.db.ExecContext(ctx, "INSERT INTO teammate_tasks ("+taskCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		t.ID, t.EnvironmentID, t.TeammateID, t.Title, t.Details, t.Status, t.Due, t.CreatedBy, at, at)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	return s.Task(ctx, t.EnvironmentID, t.ID)
}

// Task returns one task of an environment.
func (s *Store) Task(ctx context.Context, envID, id string) (Task, error) {
	return scanTask(s.db.QueryRowContext(ctx, "SELECT "+taskCols+" FROM teammate_tasks WHERE environment_id = ? AND id = ?", envID, id))
}

// TeammateTasks lists a teammate's tasks: the ones not done first, then oldest first.
func (s *Store) TeammateTasks(ctx context.Context, envID, teammateID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskCols+` FROM teammate_tasks
		WHERE environment_id = ? AND teammate_id = ? ORDER BY status = 'done', created_at, rowid`, envID, teammateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTask replaces the title, details, due and status of a task of its teammate.
func (s *Store) UpdateTask(ctx context.Context, t Task) (Task, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE teammate_tasks SET title = ?, details = ?, due = ?, status = ?, updated_at = ?
		WHERE environment_id = ? AND teammate_id = ? AND id = ?`, t.Title, t.Details, t.Due, t.Status, now(), t.EnvironmentID, t.TeammateID, t.ID)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return t, ErrNotFound
	}
	return s.Task(ctx, t.EnvironmentID, t.ID)
}

// AppendTaskNote adds note to the details of a task of its teammate, after a blank line. The
// details may not grow past limit bytes: ErrTooLarge.
func (s *Store) AppendTaskNote(ctx context.Context, envID, teammateID, id, note string, limit int) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	var details string
	err = tx.QueryRowContext(ctx, "SELECT details FROM teammate_tasks WHERE environment_id = ? AND teammate_id = ? AND id = ?",
		envID, teammateID, id).Scan(&details)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	next := note
	if details != "" {
		next = details + "\n\n" + note
	}
	if len(next) > limit {
		return Task{}, ErrTooLarge
	}
	if _, err := tx.ExecContext(ctx, "UPDATE teammate_tasks SET details = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		next, now(), envID, id); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.Task(ctx, envID, id)
}
