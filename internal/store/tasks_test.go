package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestTeammateTasks(t *testing.T) {
	ctx := context.Background()
	s, work, other := openTeammateStore(t)
	p, _ := s.CreateTeammate(ctx, Teammate{EnvironmentID: work.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if _, err := s.CreateTask(ctx, Task{EnvironmentID: work.ID, TeammateID: "missing", Title: "x", Status: TaskOpen, CreatedBy: TaskByUser}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a task of an unknown teammate: %v", err)
	}

	first, err := s.CreateTask(ctx, Task{EnvironmentID: work.ID, TeammateID: p.ID, Title: "Write the report", Status: TaskOpen, CreatedBy: TaskByUser, Due: "2026-10-20"})
	if err != nil || first.ID == "" || first.Due != "2026-10-20" || first.CreatedBy != TaskByUser {
		t.Fatalf("create: %+v (%v)", first, err)
	}
	second, _ := s.CreateTask(ctx, Task{EnvironmentID: work.ID, TeammateID: p.ID, Title: "Check the build", Status: TaskInProgress, CreatedBy: TaskByTeammate})
	done, _ := s.CreateTask(ctx, Task{EnvironmentID: work.ID, TeammateID: p.ID, Title: "Old", Status: TaskDone, CreatedBy: TaskByUser})
	if _, err := s.CreateTask(ctx, Task{EnvironmentID: other.ID, TeammateID: p.ID, Title: "Elsewhere", Status: TaskOpen, CreatedBy: TaskByUser}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a task in another environment: %v", err)
	}

	list, err := s.TeammateTasks(ctx, work.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Tasks not done come first, oldest first; done ones last.
	var ids []string
	for _, x := range list {
		ids = append(ids, x.ID)
	}
	if !slices.Equal(ids, []string{first.ID, second.ID, done.ID}) {
		t.Fatalf("order: %v", ids)
	}

	edited := first
	edited.Title, edited.Details, edited.Status, edited.Due = "Write the final report", "## Scope\n\nAll of it.", TaskDone, ""
	got, err := s.UpdateTask(ctx, edited)
	if err != nil || got.Title != "Write the final report" || got.Details == "" || got.Status != TaskDone || got.Due != "" {
		t.Fatalf("update: %+v (%v)", got, err)
	}
	if _, err := s.UpdateTask(ctx, Task{ID: first.ID, EnvironmentID: work.ID, TeammateID: "other", Title: "x", Status: TaskOpen}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update through another teammate: %v", err)
	}
}

func TestAppendTaskNote(t *testing.T) {
	ctx := context.Background()
	s, work, _ := openTeammateStore(t)
	p, _ := s.CreateTeammate(ctx, Teammate{EnvironmentID: work.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	task, _ := s.CreateTask(ctx, Task{EnvironmentID: work.ID, TeammateID: p.ID, Title: "Investigate", Status: TaskOpen, CreatedBy: TaskByUser})

	got, err := s.AppendTaskNote(ctx, work.ID, p.ID, task.ID, "Found the cause.", 64)
	if err != nil || got.Details != "Found the cause." {
		t.Fatalf("first note: %q (%v)", got.Details, err)
	}
	got, err = s.AppendTaskNote(ctx, work.ID, p.ID, task.ID, "Fixed it.", 64)
	if err != nil || got.Details != "Found the cause.\n\nFixed it." {
		t.Fatalf("second note: %q (%v)", got.Details, err)
	}
	// A note that would pass the limit is refused, and the details stay as they were.
	if _, err := s.AppendTaskNote(ctx, work.ID, p.ID, task.ID, "This note is far too long for the limit.", 64); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if after, _ := s.Task(ctx, work.ID, task.ID); after.Details != "Found the cause.\n\nFixed it." {
		t.Fatalf("a refused note changed the details: %q", after.Details)
	}
	if _, err := s.AppendTaskNote(ctx, work.ID, "someone-else", task.ID, "x", 64); !errors.Is(err, ErrNotFound) {
		t.Fatalf("note through another teammate: %v", err)
	}
}

func TestTaskSchedulesAndRuns(t *testing.T) {
	ctx := context.Background()
	s, work, _ := openTeammateStore(t)
	p, _ := s.CreateTeammate(ctx, Teammate{EnvironmentID: work.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "dev", TeammateID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	next := time.Unix(1_800_000_000, 0)
	sched := &TaskSchedule{Repeat: RepeatDays, Days: []string{"mon", "fri"}, Time: "09:00"}
	task, err := s.CreateTask(ctx, Task{EnvironmentID: work.ID, TeammateID: p.ID, Title: "CI triage", Status: TaskOpen, CreatedBy: TaskByUser,
		Schedule: sched, SandboxID: sb.ID, NextRunAt: &next})
	if err != nil || task.Schedule == nil || !slices.Equal(task.Schedule.Days, sched.Days) || task.SandboxID != sb.ID || !task.NextRunAt.Equal(next) || task.LastRun != nil {
		t.Fatalf("created: %+v (%v)", task, err)
	}
	plain, _ := s.CreateTask(ctx, Task{EnvironmentID: work.ID, TeammateID: p.ID, Title: "Unscheduled", Status: TaskOpen, CreatedBy: TaskByUser})
	if plain.Schedule != nil || plain.NextRunAt != nil {
		t.Fatalf("a task without a schedule: %+v", plain)
	}

	if due, err := s.DueTasks(ctx, next.Add(-time.Second)); err != nil || len(due) != 0 {
		t.Fatalf("due early: %+v (%v)", due, err)
	}
	if due, err := s.DueTasks(ctx, next); err != nil || len(due) != 1 || due[0].ID != task.ID {
		t.Fatalf("due: %+v (%v)", due, err)
	}
	if n, err := s.ScheduledTasks(ctx, work.ID, p.ID, ""); err != nil || n != 1 {
		t.Fatalf("scheduled: %d (%v)", n, err)
	}

	// A run's chat names its task; memory leaves it out, as it does heartbeats.
	c, err := s.CreateChat(ctx, Chat{EnvironmentID: work.ID, SandboxID: sb.ID, TeammateID: p.ID, Harness: "claude", TaskID: task.ID, Title: "CI triage · Fri"})
	if err != nil || c.TaskID != task.ID {
		t.Fatalf("run chat: %+v (%v)", c, err)
	}
	if _, err := s.SetChatState(ctx, work.ID, c.ID, ChatRunning, ChatIdle); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RunningTaskChats(ctx, work.ID, task.ID); err != nil || n != 1 {
		t.Fatalf("running run chats: %d (%v)", n, err)
	}
	missed := next.Add(-72 * time.Hour)
	for _, r := range []TaskRun{
		{TaskID: task.ID, DueAt: &missed, At: next, Outcome: RunMissed, Reason: "Studio was not running"},
		{TaskID: task.ID, DueAt: &next, At: next, Outcome: RunStarted, ChatID: c.ID},
	} {
		r.EnvironmentID, r.TeammateID = work.ID, p.ID
		if _, err := s.AddTaskRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetChatState(ctx, work.ID, c.ID, ChatIdle, ChatRunning); err != nil {
		t.Fatal(err)
	}
	if chats, _ := s.ChatsToRemember(ctx, time.Unix(0, 0)); slices.ContainsFunc(chats, func(x Chat) bool { return x.ID == c.ID }) {
		t.Fatalf("memory reads a run's chat")
	}
	if ok, err := s.FinishTaskRun(ctx, work.ID, task.ID); err != nil || !ok {
		t.Fatalf("finish: %v (%v)", ok, err)
	}
	if ok, _ := s.FinishTaskRun(ctx, work.ID, task.ID); ok {
		t.Fatalf("a run was finished twice")
	}
	got, err := s.Task(ctx, work.ID, task.ID)
	if err != nil || got.LastRun == nil || got.LastRun.Outcome != RunDone || got.LastRun.ChatID != c.ID {
		t.Fatalf("last run: %+v (%v)", got.LastRun, err)
	}
	runs, err := s.TeammateTaskRuns(ctx, work.ID, p.ID, 0)
	if err != nil || len(runs) != 2 || runs[0].Outcome != RunDone || runs[1].Outcome != RunMissed || !runs[1].DueAt.Equal(missed) {
		t.Fatalf("runs: %+v (%v)", runs, err)
	}
	list, _ := s.TeammateTasks(ctx, work.ID, p.ID)
	if len(list) != 2 || list[0].LastRun == nil || list[1].LastRun != nil {
		t.Fatalf("listed last runs: %+v", list)
	}

	// Removing the schedule leaves nothing due; StartTask moves an open task in progress.
	task.Schedule, task.NextRunAt, task.SandboxID = nil, nil, ""
	if task, err = s.UpdateTask(ctx, task); err != nil || task.Schedule != nil || task.NextRunAt != nil || task.SandboxID != "" {
		t.Fatalf("unscheduled: %+v (%v)", task, err)
	}
	if due, _ := s.DueTasks(ctx, next); len(due) != 0 {
		t.Fatalf("due without a schedule: %+v", due)
	}
	if err := s.StartTask(ctx, work.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Task(ctx, work.ID, task.ID); got.Status != TaskInProgress {
		t.Fatalf("started: %+v", got)
	}
	// The sandbox going away sends the runs to the latest one.
	task.SandboxID = sb.ID
	if _, err := s.UpdateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSandbox(ctx, work.ID, sb.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Task(ctx, work.ID, task.ID); got.SandboxID != "" {
		t.Fatalf("a deleted sandbox is kept: %+v", got)
	}
}
