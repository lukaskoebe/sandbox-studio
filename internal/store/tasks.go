package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	// Schedule runs the task in a chat of its own at its times; nil for none.
	Schedule  *TaskSchedule `json:"schedule,omitempty"`
	SandboxID string        `json:"sandboxId,omitempty" doc:"The sandbox the runs go to; empty for the teammate's latest"`
	NextRunAt *time.Time    `json:"nextRunAt,omitempty" doc:"When the schedule runs the task next; absent while it is paused or has no time left"`
	LastRun   *TaskRun      `json:"lastRun,omitempty" doc:"The latest logged run"`
}

// Schedule repeats.
const (
	RepeatOnce  = "once"
	RepeatDays  = "days"
	RepeatHours = "hours"
)

// TaskSchedule is when a task runs, in the Studio host's local time: once at Date and Time, on
// Days at Time, or every EveryHours hours from FromHour while the hour is before ToHour.
type TaskSchedule struct {
	Repeat     string   `json:"repeat" enum:"once,days,hours"`
	Date       string   `json:"date,omitempty" doc:"once: the day, YYYY-MM-DD"`
	Time       string   `json:"time,omitempty" doc:"once and days: the local time, HH:MM"`
	Days       []string `json:"days,omitempty" enum:"mon,tue,wed,thu,fri,sat,sun" doc:"days: the weekdays it runs on"`
	EveryHours int      `json:"everyHours,omitempty" minimum:"1" maximum:"24" doc:"hours: the hours between runs"`
	FromHour   int      `json:"fromHour,omitempty" minimum:"0" maximum:"23" doc:"hours: the local hour of the first run"`
	ToHour     int      `json:"toHour,omitempty" minimum:"1" maximum:"24" doc:"hours: the local hour the runs stop before"`
	Paused     bool     `json:"paused,omitempty" doc:"Keeps the schedule and stops the runs"`
}

// Repeats reports whether the schedule runs more than once.
func (s *TaskSchedule) Repeats() bool { return s != nil && s.Repeat != RepeatOnce }

// Task run outcomes.
const (
	RunStarted = "started"
	RunDone    = "done"
	RunSkipped = "skipped"
	RunMissed  = "missed"
)

// TaskRun is a logged run of a scheduled task.
type TaskRun struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"-"`
	TeammateID    string     `json:"teammateId"`
	TaskID        string     `json:"taskId"`
	DueAt         *time.Time `json:"dueAt,omitempty" doc:"The scheduled time; absent for a run started with Run now"`
	At            time.Time  `json:"at"`
	Outcome       string     `json:"outcome" enum:"started,done,skipped,missed" doc:"done: the teammate marked the run's work done"`
	Reason        string     `json:"reason,omitempty" doc:"Why the run was skipped or missed"`
	ChatID        string     `json:"chatId,omitempty" doc:"The chat the run started"`
}

const taskCols = "id, environment_id, teammate_id, title, details, status, due, created_by, created_at, updated_at, IFNULL(schedule, ''), IFNULL(sandbox_id, ''), next_run_at"

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var created, updated int64
	var schedule string
	var next sql.NullInt64
	err := row.Scan(&t.ID, &t.EnvironmentID, &t.TeammateID, &t.Title, &t.Details, &t.Status, &t.Due, &t.CreatedBy, &created, &updated,
		&schedule, &t.SandboxID, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.CreatedAt, t.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	if schedule != "" {
		t.Schedule = &TaskSchedule{}
		if err := json.Unmarshal([]byte(schedule), t.Schedule); err != nil {
			return t, err
		}
	}
	if next.Valid {
		at := time.Unix(next.Int64, 0)
		t.NextRunAt = &at
	}
	return t, nil
}

// scheduleArgs are a task's schedule, sandbox and next run as the columns keep them.
func scheduleArgs(t Task) (schedule, sandbox, next any, err error) {
	if t.Schedule != nil {
		b, err := json.Marshal(t.Schedule)
		if err != nil {
			return nil, nil, nil, err
		}
		schedule = string(b)
	}
	if t.NextRunAt != nil {
		next = t.NextRunAt.Unix()
	}
	return schedule, nullable(t.SandboxID), next, nil
}

// CreateTask inserts t, a task of its teammate, with a new ID unless it has one.
func (s *Store) CreateTask(ctx context.Context, t Task) (Task, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	at := now()
	schedule, sandbox, next, err := scheduleArgs(t)
	if err != nil {
		return t, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO teammate_tasks (id, environment_id, teammate_id, title, details, status, due, created_by,
		created_at, updated_at, schedule, sandbox_id, next_run_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.EnvironmentID, t.TeammateID, t.Title, t.Details, t.Status, t.Due, t.CreatedBy, at, at, schedule, sandbox, next)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	return s.Task(ctx, t.EnvironmentID, t.ID)
}

// Task returns one task of an environment, with its last run.
func (s *Store) Task(ctx context.Context, envID, id string) (Task, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, "SELECT "+taskCols+" FROM teammate_tasks WHERE environment_id = ? AND id = ?", envID, id))
	if err != nil {
		return t, err
	}
	last, err := s.lastRuns(ctx, "r.environment_id = ? AND r.task_id = ?", envID, id)
	if run, ok := last[t.ID]; ok {
		t.LastRun = &run
	}
	return t, err
}

// TeammateTasks lists a teammate's tasks: the ones not done first, then oldest first.
func (s *Store) TeammateTasks(ctx context.Context, envID, teammateID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskCols+` FROM teammate_tasks
		WHERE environment_id = ? AND teammate_id = ? ORDER BY status = 'done', created_at, rowid`, envID, teammateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := collectTasks(rows)
	if err != nil {
		return nil, err
	}
	last, err := s.lastRuns(ctx, "r.environment_id = ? AND r.teammate_id = ?", envID, teammateID)
	for i := range out {
		if run, ok := last[out[i].ID]; ok {
			out[i].LastRun = &run
		}
	}
	return out, err
}

func collectTasks(rows *sql.Rows) ([]Task, error) {
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

// DueTasks lists the tasks of every environment whose next run is due at now: the ones with a
// schedule that are not done, earliest first.
func (s *Store) DueTasks(ctx context.Context, at time.Time) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskCols+` FROM teammate_tasks
		WHERE next_run_at IS NOT NULL AND next_run_at <= ? AND schedule IS NOT NULL AND status != 'done' ORDER BY next_run_at, rowid`, at.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

// ScheduledTasks counts a teammate's tasks that are not done and have a schedule, leaving out
// the task except.
func (s *Store) ScheduledTasks(ctx context.Context, envID, teammateID, except string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teammate_tasks WHERE environment_id = ? AND teammate_id = ?
		AND schedule IS NOT NULL AND status != 'done' AND id != ?`, envID, teammateID, except).Scan(&n)
	return n, err
}

// UpdateTask replaces the title, details, due, status, schedule, sandbox and next run of a
// task of its teammate.
func (s *Store) UpdateTask(ctx context.Context, t Task) (Task, error) {
	schedule, sandbox, next, err := scheduleArgs(t)
	if err != nil {
		return t, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE teammate_tasks SET title = ?, details = ?, due = ?, status = ?, schedule = ?, sandbox_id = ?,
		next_run_at = ?, updated_at = ? WHERE environment_id = ? AND teammate_id = ? AND id = ?`,
		t.Title, t.Details, t.Due, t.Status, schedule, sandbox, next, now(), t.EnvironmentID, t.TeammateID, t.ID)
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

// SetTaskNextRun moves a task's schedule to its next run, or to none with nil.
func (s *Store) SetTaskNextRun(ctx context.Context, envID, id string, next *time.Time) error {
	var at any
	if next != nil {
		at = next.Unix()
	}
	_, err := s.db.ExecContext(ctx, "UPDATE teammate_tasks SET next_run_at = ? WHERE environment_id = ? AND id = ?", at, envID, id)
	return err
}

// StartTask marks an open task in progress, as its run starts. Other states stay.
func (s *Store) StartTask(ctx context.Context, envID, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE teammate_tasks SET status = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND status = ?",
		TaskInProgress, now(), envID, id, TaskOpen)
	return err
}

// AddTaskRun logs a run of a task.
func (s *Store) AddTaskRun(ctx context.Context, r TaskRun) (TaskRun, error) {
	if r.ID == "" {
		r.ID = NewID()
	}
	var due int64
	if r.DueAt != nil {
		due = r.DueAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO task_runs (id, environment_id, teammate_id, task_id, due_at, at, outcome, reason, chat_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.EnvironmentID, r.TeammateID, r.TaskID, due, r.At.Unix(), r.Outcome, r.Reason, nullable(r.ChatID))
	return r, err
}

// FinishTaskRun logs a task's latest started run done, when the teammate marked its work done.
// It reports whether there was one.
func (s *Store) FinishTaskRun(ctx context.Context, envID, taskID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE task_runs SET outcome = ? WHERE id = (SELECT id FROM task_runs
		WHERE environment_id = ? AND task_id = ? ORDER BY at DESC, rowid DESC LIMIT 1) AND outcome = ?`, RunDone, envID, taskID, RunStarted)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TaskRuns lists a task's logged runs, newest first, at most limit (default 100).
func (s *Store) TaskRuns(ctx context.Context, envID, taskID string, limit int) ([]TaskRun, error) {
	return s.taskRuns(ctx, "environment_id = ? AND task_id = ?", limit, envID, taskID)
}

// TeammateTaskRuns lists the logged runs of all of a teammate's tasks, newest first, at most
// limit (default 100).
func (s *Store) TeammateTaskRuns(ctx context.Context, envID, teammateID string, limit int) ([]TaskRun, error) {
	return s.taskRuns(ctx, "environment_id = ? AND teammate_id = ?", limit, envID, teammateID)
}

// RunningTaskChats counts the chats of a task's runs that are running a turn.
func (s *Store) RunningTaskChats(ctx context.Context, envID, taskID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats WHERE environment_id = ? AND task_id = ? AND state = ?",
		envID, taskID, ChatRunning).Scan(&n)
	return n, err
}

const taskRunCols = "r.id, r.environment_id, r.teammate_id, r.task_id, r.due_at, r.at, r.outcome, r.reason, IFNULL(r.chat_id, '')"

func scanTaskRun(row interface{ Scan(...any) error }) (TaskRun, error) {
	var r TaskRun
	var due, at int64
	if err := row.Scan(&r.ID, &r.EnvironmentID, &r.TeammateID, &r.TaskID, &due, &at, &r.Outcome, &r.Reason, &r.ChatID); err != nil {
		return r, err
	}
	if due != 0 {
		d := time.Unix(due, 0)
		r.DueAt = &d
	}
	r.At = time.Unix(at, 0)
	return r, nil
}

func (s *Store) taskRuns(ctx context.Context, where string, limit int, args ...any) ([]TaskRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskRunCols+" FROM task_runs r WHERE "+where+" ORDER BY r.at DESC, r.rowid DESC LIMIT ?",
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskRun{}
	for rows.Next() {
		r, err := scanTaskRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// lastRuns returns the latest run of each task that where selects, by task ID.
func (s *Store) lastRuns(ctx context.Context, where string, args ...any) (map[string]TaskRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskRunCols+" FROM task_runs r WHERE "+where+` AND r.rowid = (SELECT l.rowid FROM task_runs l
		WHERE l.task_id = r.task_id ORDER BY l.at DESC, l.rowid DESC LIMIT 1)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TaskRun{}
	for rows.Next() {
		r, err := scanTaskRun(rows)
		if err != nil {
			return nil, err
		}
		out[r.TaskID] = r
	}
	return out, rows.Err()
}
