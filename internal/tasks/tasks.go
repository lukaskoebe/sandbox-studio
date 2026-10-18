// Package tasks keeps the task lists of teammates. The Tasks tab edits them, and a teammate
// reads and changes its own through the task tools of its sandboxes (docs/teammates.md).
package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// TaskInput is the editable part of a task. Due is a day as YYYY-MM-DD, or empty.
type TaskInput struct {
	Title    string              `json:"title" minLength:"1" maxLength:"200"`
	Details  string              `json:"details,omitempty" maxLength:"8192" doc:"Markdown, at most 8 KiB"`
	Due      string              `json:"due,omitempty" maxLength:"10" doc:"A day, YYYY-MM-DD"`
	Status   string              `json:"status,omitempty" enum:"open,in_progress,done"`
	Schedule *store.TaskSchedule `json:"schedule,omitempty" doc:"When the task runs in a chat of its own; absent for none, so a PUT without it removes it"`
	// SandboxID is where the runs go; empty for the teammate's latest sandbox.
	SandboxID string `json:"sandboxId,omitempty" doc:"The sandbox the runs go to; empty for the teammate's latest"`
}

// Service keeps the task lists of an environment's teammates.
type Service struct {
	Store *store.Store
	Bus   *events.Bus
	Log   *slog.Logger
	// Now is the clock that schedules count from; nil means time.Now.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// List returns a teammate's tasks.
func (s *Service) List(ctx context.Context, envID, teammateID string) ([]store.Task, error) {
	return s.Store.TeammateTasks(ctx, envID, teammateID)
}

// Create adds a task to a teammate's list, created by the user or by the teammate (by is
// store.TaskByUser or store.TaskByTeammate). A new task is open. Only the user's may have a
// schedule: the teammate's tools take none.
func (s *Service) Create(ctx context.Context, envID, teammateID, by string, in TaskInput) (store.Task, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return store.Task{}, err
	}
	t := store.Task{
		EnvironmentID: envID, TeammateID: teammateID, Title: in.Title, Details: in.Details,
		Due: in.Due, Status: store.TaskOpen, CreatedBy: by,
	}
	if err := s.schedule(ctx, &t, in, nil); err != nil {
		return store.Task{}, err
	}
	t, err := s.Store.CreateTask(ctx, t)
	if err == nil {
		s.changed(envID)
	}
	return t, err
}

// Update replaces the title, details, due, status, schedule and sandbox of a task of a
// teammate, as the user does. A repeating task the user marks done loses its schedule.
func (s *Service) Update(ctx context.Context, envID, teammateID, id string, in TaskInput) (store.Task, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return store.Task{}, err
	}
	if err := checkStatus(in.Status); err != nil {
		return store.Task{}, err
	}
	t, err := s.task(ctx, envID, teammateID, id)
	if err != nil {
		return t, err
	}
	old := t
	t.Title, t.Details, t.Due, t.Status = in.Title, in.Details, in.Due, in.Status
	if in.Status == store.TaskDone && in.Schedule.Repeats() {
		in.Schedule, in.SandboxID = nil, ""
	}
	if err := s.schedule(ctx, &t, in, &old); err != nil {
		return store.Task{}, err
	}
	if t, err = s.Store.UpdateTask(ctx, t); err != nil {
		return t, err
	}
	if t.Status == store.TaskDone && old.Status != store.TaskDone {
		if _, err := s.Store.FinishTaskRun(ctx, envID, id); err != nil {
			return t, err
		}
	}
	s.changed(envID)
	return s.Store.Task(ctx, envID, id)
}

// StatusResult is a task after task_update_status, with a note when the status stayed.
type StatusResult struct {
	store.Task
	Note string `json:"note,omitempty"`
}

// SetStatus changes the status of a task of a teammate, as its task_update_status does. Done
// on a repeating task ends only its current run: the run is logged done, and the task keeps
// its status and waits for its next run.
func (s *Service) SetStatus(ctx context.Context, envID, teammateID, id, status string) (StatusResult, error) {
	if err := checkStatus(status); err != nil {
		return StatusResult{}, err
	}
	t, err := s.task(ctx, envID, teammateID, id)
	if err != nil {
		return StatusResult{}, err
	}
	if status == store.TaskDone && t.Schedule.Repeats() {
		if _, err := s.Store.FinishTaskRun(ctx, envID, id); err != nil {
			return StatusResult{}, err
		}
		s.changed(envID)
		if t, err = s.Store.Task(ctx, envID, id); err != nil {
			return StatusResult{}, err
		}
		note := "This task repeats on a schedule, so it stays " + t.Status + ": this run is done."
		if t.NextRunAt != nil {
			note += " The next run is at " + t.NextRunAt.In(time.Local).Format("Mon 2 Jan 15:04") + "."
		}
		return StatusResult{Task: t, Note: note}, nil
	}
	wasDone := t.Status == store.TaskDone
	t.Status = status
	if t, err = s.Store.UpdateTask(ctx, t); err != nil {
		return StatusResult{}, err
	}
	if status == store.TaskDone && !wasDone {
		if _, err := s.Store.FinishTaskRun(ctx, envID, id); err != nil {
			return StatusResult{}, err
		}
	}
	s.changed(envID)
	t, err = s.Store.Task(ctx, envID, id)
	return StatusResult{Task: t}, err
}

// schedule sets t's schedule, sandbox and next run from in. old is the task before, nil for a
// new one: an unchanged schedule keeps its next run, so a run that is due or waiting stays.
func (s *Service) schedule(ctx context.Context, t *store.Task, in TaskInput, old *store.Task) error {
	t.Schedule, t.SandboxID, t.NextRunAt = in.Schedule, in.SandboxID, nil
	if in.SandboxID != "" {
		sb, err := s.Store.Sandbox(ctx, t.EnvironmentID, in.SandboxID)
		if errors.Is(err, store.ErrNotFound) || err == nil && (sb.TeammateID != t.TeammateID || sb.BuildJobID != "" || sb.Kind != "") {
			return fmt.Errorf("%w: the runs can only go to a sandbox of the teammate's", teammates.ErrInvalid)
		}
		if err != nil {
			return err
		}
	}
	if t.Schedule == nil || t.Status == store.TaskDone {
		return nil
	}
	if err := CheckSchedule(*t.Schedule); err != nil {
		return err
	}
	if old == nil || old.Schedule == nil || old.Status == store.TaskDone {
		id := ""
		if old != nil {
			id = old.ID
		}
		if n, err := s.Store.ScheduledTasks(ctx, t.EnvironmentID, t.TeammateID, id); err != nil {
			return err
		} else if n >= MaxScheduled {
			return fmt.Errorf("%w: a teammate has at most %d scheduled tasks", teammates.ErrInvalid, MaxScheduled)
		}
	}
	if old != nil && old.Status != store.TaskDone && reflect.DeepEqual(old.Schedule, t.Schedule) {
		t.NextRunAt = old.NextRunAt
		return nil
	}
	t.NextRunAt = NextRun(t.Schedule, s.now().In(time.Local))
	return nil
}

// AddNote appends a note to the details of a task of a teammate, after a blank line. The
// details stay within the size limit.
func (s *Service) AddNote(ctx context.Context, envID, teammateID, id, note string) (store.Task, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return store.Task{}, fmt.Errorf("%w: the note is empty", teammates.ErrInvalid)
	}
	if err := teammates.CheckTaskDetails(note); err != nil {
		return store.Task{}, err
	}
	t, err := s.Store.AppendTaskNote(ctx, envID, teammateID, id, note, teammates.MaxTaskDetailsSize)
	if errors.Is(err, store.ErrTooLarge) {
		return store.Task{}, fmt.Errorf("%w: the details would be larger than %d KiB", teammates.ErrInvalid, teammates.MaxTaskDetailsSize>>10)
	}
	if err == nil {
		s.changed(envID)
	}
	return t, err
}

// task returns a task of a teammate; another teammate's task is not found.
func (s *Service) task(ctx context.Context, envID, teammateID, id string) (store.Task, error) {
	t, err := s.Store.Task(ctx, envID, id)
	if err == nil && t.TeammateID != teammateID {
		err = store.ErrNotFound
	}
	return t, err
}

func (s *Service) changed(envID string) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicTasks, EnvironmentID: envID})
	}
}

func normalize(in TaskInput) TaskInput {
	in.Title = strings.TrimSpace(in.Title)
	in.Details = strings.TrimSpace(in.Details)
	in.Due = strings.TrimSpace(in.Due)
	in.Status = strings.TrimSpace(in.Status)
	in.SandboxID = strings.TrimSpace(in.SandboxID)
	if in.Schedule != nil {
		sched := normalizeSchedule(*in.Schedule)
		in.Schedule = &sched
	}
	return in
}

func validate(in TaskInput) error {
	if err := teammates.CheckTaskTitle(in.Title); err != nil {
		return err
	}
	if err := teammates.CheckTaskDetails(in.Details); err != nil {
		return err
	}
	return teammates.CheckTaskDue(in.Due)
}

func checkStatus(status string) error {
	switch status {
	case store.TaskOpen, store.TaskInProgress, store.TaskDone:
		return nil
	}
	return fmt.Errorf("%w: the status must be open, in_progress or done", teammates.ErrInvalid)
}

// HandleCall answers a task tool of a sandbox (agentproto.TaskMethodPrefix). The sandbox's
// owner teammate is the one whose tasks the call reads and writes, whatever the arguments
// name. Mistakes the agent can fix are returned as user errors, which it sees verbatim.
func (s *Service) HandleCall(ctx context.Context, sandboxID, method string, params json.RawMessage) (any, error) {
	sb, err := s.Store.LookupSandbox(ctx, sandboxID)
	if err != nil {
		return nil, userErrorf("this sandbox is not known to Studio")
	}
	if sb.TeammateID == "" || sb.BuildJobID != "" || sb.Kind != "" {
		return nil, userErrorf("this sandbox has no teammate, so it has no tasks")
	}
	res, err := s.call(ctx, sb, method, params)
	if err == nil {
		return res, nil
	}
	var ue userError
	if errors.As(err, &ue) {
		return nil, err
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, userErrorf("no task with that ID in your list")
	}
	if errors.Is(err, teammates.ErrInvalid) || errors.Is(err, store.ErrTooLarge) {
		return nil, userErrorf("%v", err)
	}
	s.log().Warn("task tool failed", "tool", method, "sandbox", sandboxID, "err", err)
	return nil, errors.New(method + " failed")
}

func (s *Service) call(ctx context.Context, sb store.Sandbox, method string, params json.RawMessage) (any, error) {
	switch method {
	case agentproto.MethodTasksList:
		var in struct {
			IncludeDone bool `json:"include_done"`
		}
		if err := decode(params, &in); err != nil {
			return nil, err
		}
		list, err := s.List(ctx, sb.EnvironmentID, sb.TeammateID)
		if err != nil {
			return nil, err
		}
		out := []store.Task{}
		for _, t := range list {
			if in.IncludeDone || t.Status != store.TaskDone {
				out = append(out, t)
			}
		}
		return map[string]any{"tasks": out}, nil
	case agentproto.MethodTaskCreate:
		var in struct {
			Title   string `json:"title"`
			Details string `json:"details"`
			Due     string `json:"due"`
		}
		if err := decode(params, &in); err != nil {
			return nil, err
		}
		return s.Create(ctx, sb.EnvironmentID, sb.TeammateID, store.TaskByTeammate, TaskInput{Title: in.Title, Details: in.Details, Due: in.Due})
	case agentproto.MethodTaskUpdateStatus:
		var in struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}
		if err := decode(params, &in); err != nil {
			return nil, err
		}
		return s.SetStatus(ctx, sb.EnvironmentID, sb.TeammateID, in.TaskID, in.Status)
	case agentproto.MethodTaskAddNote:
		var in struct {
			TaskID string `json:"task_id"`
			Note   string `json:"note"`
		}
		if err := decode(params, &in); err != nil {
			return nil, err
		}
		return s.AddNote(ctx, sb.EnvironmentID, sb.TeammateID, in.TaskID, in.Note)
	}
	return nil, userErrorf("unknown task tool %q", method)
}

// decode reads tool arguments strictly: an argument the tool doesn't take is refused.
func decode(params json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return userErrorf("invalid arguments: %v", err)
	}
	return nil
}

// userError is a tool error the agent should see verbatim.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func userErrorf(format string, args ...any) error { return userError{fmt.Sprintf(format, args...)} }
