package heartbeat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/tasks"
)

var daily = store.TaskSchedule{Repeat: store.RepeatDays, Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, Time: "10:00"}

// scheduled adds a task of the fixture's teammate with schedule s, whose next run is at next.
func (f *fixture) scheduled(title string, s store.TaskSchedule, next time.Time) store.Task {
	f.t.Helper()
	task, err := f.st.CreateTask(context.Background(), store.Task{EnvironmentID: f.env.ID, TeammateID: f.teammate.ID,
		Title: title, Status: store.TaskOpen, CreatedBy: store.TaskByUser, Schedule: &s, NextRunAt: &next})
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}

// due runs the due task again as the scheduler would, at the fixture's time.
func (f *fixture) due(task store.Task) store.Task {
	f.t.Helper()
	ctx := context.Background()
	task, err := f.st.Task(ctx, f.env.ID, task.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.svc.Due(ctx, task, f.now); err != nil {
		f.t.Fatalf("due: %v", err)
	}
	if task, err = f.st.Task(ctx, f.env.ID, task.ID); err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f *fixture) runs(task store.Task) []store.TaskRun {
	f.t.Helper()
	runs, err := f.st.TaskRuns(context.Background(), f.env.ID, task.ID, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return runs
}

// idle ends the running turns of every chat in the store.
func (f *fixture) idle() {
	f.t.Helper()
	ctx := context.Background()
	chats, err := f.st.Chats(ctx, f.env.ID, "")
	if err != nil {
		f.t.Fatal(err)
	}
	for _, c := range chats {
		if c.State == store.ChatRunning {
			if _, err := f.st.SetChatState(ctx, f.env.ID, c.ID, store.ChatIdle, store.ChatRunning); err != nil {
				f.t.Fatal(err)
			}
		}
	}
}

func TestADueRunStartsAChatOfItsOwn(t *testing.T) {
	f := newFixture(t)
	f.sandbox(runtime.StatusStopped, false)
	once := store.TaskSchedule{Repeat: store.RepeatOnce, Date: "2026-10-13", Time: "10:00"}
	task := f.scheduled("Check the release", once, f.now)

	task = f.due(task)
	runs := f.runs(task)
	if len(runs) != 1 || runs[0].Outcome != store.RunStarted || runs[0].ChatID == "" || runs[0].DueAt == nil || !runs[0].DueAt.Equal(f.now) {
		t.Fatalf("runs: %+v", runs)
	}
	// A one-off task goes in progress, and has no time left.
	if task.Status != store.TaskInProgress || task.NextRunAt != nil || task.LastRun == nil || task.LastRun.ID != runs[0].ID {
		t.Fatalf("task after its run: %+v", task)
	}
	c, err := f.st.Chat(context.Background(), f.env.ID, runs[0].ChatID)
	if err != nil || c.TaskID != task.ID || c.Heartbeat || c.Title != "Check the release · Tue 13 Oct 10:00" {
		t.Fatalf("run chat: %+v (%v)", c, err)
	}
	f.chats.mu.Lock()
	prompts := f.chats.prompts
	f.chats.mu.Unlock()
	if len(prompts) != 1 || !strings.HasPrefix(prompts[0], RunMessage) || !strings.Contains(prompts[0], "<studio-tasks>\n- Check the release [id "+task.ID+"]") ||
		strings.Contains(prompts[0], "Write the report") {
		t.Fatalf("prompt: %q", prompts)
	}
	if f.vms.calls[0] != "start" {
		t.Errorf("the stopped sandbox was not started: %v", f.vms.calls)
	}
}

func TestABusyRunWaitsThenRuns(t *testing.T) {
	f := newFixture(t)
	sb := f.sandbox(runtime.StatusRunning, true)
	f.chatIn(sb, false) // the user's turn
	task := f.scheduled("CI triage", daily, f.now)

	task = f.due(task)
	if runs := f.runs(task); len(runs) != 0 || !task.NextRunAt.Equal(f.now) {
		t.Fatalf("a busy run was not left waiting: %+v, next %v", runs, task.NextRunAt)
	}
	f.idle()
	f.now = f.now.Add(5 * time.Minute)
	task = f.due(task)
	runs := f.runs(task)
	if len(runs) != 1 || runs[0].Outcome != store.RunStarted || !runs[0].DueAt.Equal(f.now.Add(-5*time.Minute)) {
		t.Fatalf("runs after the wait: %+v", runs)
	}
	// A repeating task stays open and moves to its next time.
	if task.Status != store.TaskOpen || !task.NextRunAt.Equal(time.Date(2026, 10, 14, 10, 0, 0, 0, time.Local)) {
		t.Fatalf("task: %+v next %v", task, task.NextRunAt)
	}
}

func TestABusyRunIsSkippedAfterTheWait(t *testing.T) {
	f := newFixture(t)
	sb := f.sandbox(runtime.StatusRunning, true)
	if _, _, err := f.st.RequestApproval(context.Background(), store.Approval{EnvironmentID: f.env.ID, SandboxID: sb.ID,
		Kind: "network.request", Subject: "example.com:443", Payload: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	task := f.scheduled("CI triage", daily, f.now)
	start := f.now
	f.due(task)
	f.now = start.Add(RunWait - time.Minute)
	if task = f.due(task); len(f.runs(task)) != 0 {
		t.Fatalf("skipped before the wait was over: %+v", f.runs(task))
	}
	f.now = start.Add(RunWait)
	task = f.due(task)
	runs := f.runs(task)
	if len(runs) != 1 || runs[0].Outcome != store.RunSkipped || !strings.Contains(runs[0].Reason, "approvals in sandbox dev wait") {
		t.Fatalf("runs: %+v", runs)
	}
	if !task.NextRunAt.Equal(time.Date(2026, 10, 14, 10, 0, 0, 0, time.Local)) {
		t.Fatalf("next %v", task.NextRunAt)
	}
}

func TestARunThatCannotStartIsSkippedAtOnce(t *testing.T) {
	f := newFixture(t)
	f.sandbox(runtime.StatusAbsent, false)
	task := f.due(f.scheduled("CI triage", daily, f.now))
	runs := f.runs(task)
	if len(runs) != 1 || runs[0].Outcome != store.RunSkipped || !strings.Contains(runs[0].Reason, "needs to be created") || task.NextRunAt == nil || !task.NextRunAt.After(f.now) {
		t.Fatalf("runs: %+v, next %v", runs, task.NextRunAt)
	}
}

func TestMissedRunsAndCatchUp(t *testing.T) {
	f := newFixture(t)
	f.sandbox(runtime.StatusRunning, true)
	day := func(d, hour, minute int) time.Time { return time.Date(2026, 10, d, hour, minute, 0, 0, time.Local) }

	// Studio was off for three days and starts two hours after today's time: every run is missed.
	f.now = day(13, 12, 0)
	off := f.due(f.scheduled("Off for days", daily, day(10, 10, 0)))
	runs := f.runs(off)
	if len(runs) != 4 {
		t.Fatalf("missed runs: %+v", runs)
	}
	for _, r := range runs {
		if r.Outcome != store.RunMissed || r.Reason != "Studio was not running" {
			t.Fatalf("missed run: %+v", r)
		}
	}
	if !runs[0].DueAt.Equal(day(13, 10, 0)) || !off.NextRunAt.Equal(day(14, 10, 0)) {
		t.Fatalf("newest miss %v, next %v", runs[0].DueAt, off.NextRunAt)
	}

	// Less than an hour late, the latest time still runs; the one before is missed.
	f.now = day(13, 10, 40)
	late := f.due(f.scheduled("Forty minutes late", daily, day(12, 10, 0)))
	runs = f.runs(late)
	if len(runs) != 2 || runs[0].Outcome != store.RunStarted || !runs[0].DueAt.Equal(day(13, 10, 0)) ||
		runs[1].Outcome != store.RunMissed || !runs[1].DueAt.Equal(day(12, 10, 0)) {
		t.Fatalf("catch-up runs: %+v", runs)
	}
	if !late.NextRunAt.Equal(day(14, 10, 0)) {
		t.Fatalf("next %v", late.NextRunAt)
	}
}

func TestTickRunsOnlyTheDueTasks(t *testing.T) {
	f := newFixture(t)
	f.sandbox(runtime.StatusRunning, true)
	ctx := context.Background()
	// No check-in competes for the sandbox.
	if _, err := f.st.SetHeartbeat(ctx, f.env.ID, f.teammate.ID, nil); err != nil {
		t.Fatal(err)
	}
	due := f.scheduled("Due", daily, f.now)
	later := f.scheduled("Later", daily, f.now.Add(time.Hour))
	paused := daily
	paused.Paused = true
	stopped, err := f.st.CreateTask(ctx, store.Task{EnvironmentID: f.env.ID, TeammateID: f.teammate.ID, Title: "Paused",
		Status: store.TaskOpen, CreatedBy: store.TaskByUser, Schedule: &paused})
	if err != nil {
		t.Fatal(err)
	}

	f.svc.Tick(ctx)
	if n := len(f.chats.started()); n != 1 || f.chats.started()[0].TaskID != due.ID {
		t.Fatalf("chats started: %+v", f.chats.started())
	}
	for _, task := range []store.Task{later, stopped} {
		if runs := f.runs(task); len(runs) != 0 {
			t.Errorf("%s ran: %+v", task.Title, runs)
		}
	}
}

func TestACheckInLeavesScheduledTasksOut(t *testing.T) {
	f := newFixture(t)
	f.sandbox(runtime.StatusRunning, true)
	f.scheduled("CI triage", daily, f.now.Add(24*time.Hour))
	if err := f.beat(); err != nil {
		t.Fatal(err)
	}
	f.chats.mu.Lock()
	prompts := f.chats.prompts
	f.chats.mu.Unlock()
	if len(prompts) != 1 || strings.Contains(prompts[0], "CI triage") || !strings.Contains(prompts[0], "Write the report") ||
		!strings.Contains(prompts[0], "1 more task runs on a schedule of its own") {
		t.Fatalf("check-in prompt: %q", prompts)
	}

	// With only scheduled tasks open, a check-in has nothing to do.
	g := newFixture(t)
	g.sandbox(runtime.StatusRunning, true)
	ctx := context.Background()
	list, _ := g.st.TeammateTasks(ctx, g.env.ID, g.teammate.ID)
	list[0].Schedule, list[0].NextRunAt = &daily, &g.now
	if _, err := g.st.UpdateTask(ctx, list[0]); err != nil {
		t.Fatal(err)
	}
	if err := g.beat(); err != nil {
		t.Fatal(err)
	}
	if n := len(g.chats.started()); n != 0 || len(g.logged()) != 0 {
		t.Fatalf("a check-in took a scheduled task: %d chats, %+v", n, g.logged())
	}
}

func TestRunNow(t *testing.T) {
	f := newFixture(t)
	sb := f.sandbox(runtime.StatusRunning, true)
	ctx := context.Background()
	next := f.now.Add(24 * time.Hour)
	task := f.scheduled("CI triage", daily, next)

	f.chatIn(sb, false)
	if _, err := f.svc.RunNow(ctx, f.env.ID, f.teammate.ID, task.ID); !errors.Is(err, tasks.ErrCannotRun) || !strings.Contains(err.Error(), "a turn is running in sandbox dev") {
		t.Fatalf("run now while busy: %v", err)
	}
	if runs := f.runs(task); len(runs) != 0 {
		t.Fatalf("a refused run was logged: %+v", runs)
	}
	f.idle()
	run, err := f.svc.RunNow(ctx, f.env.ID, f.teammate.ID, task.ID)
	if err != nil || run.Outcome != store.RunStarted || run.ChatID == "" || run.DueAt != nil {
		t.Fatalf("run now: %+v (%v)", run, err)
	}
	// The previous run still runs, so another is refused; the schedule's next run stays.
	if _, err := f.svc.RunNow(ctx, f.env.ID, f.teammate.ID, task.ID); !errors.Is(err, tasks.ErrCannotRun) || !strings.Contains(err.Error(), "previous run is still running") {
		t.Fatalf("run now during a run: %v", err)
	}
	if got, _ := f.st.Task(ctx, f.env.ID, task.ID); !got.NextRunAt.Equal(next) || got.Status != store.TaskOpen {
		t.Fatalf("task after run now: %+v", got)
	}
	// Another teammate's task is not found.
	f.teammateWith("p-anthropic")
	if _, err := f.svc.RunNow(ctx, f.env.ID, f.teammate.ID, task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another teammate's task: %v", err)
	}
}
