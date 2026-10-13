package store

import (
	"context"
	"errors"
	"slices"
	"testing"
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
