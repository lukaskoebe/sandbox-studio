package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

type fixture struct {
	svc      *Service
	st       *store.Store
	bus      *events.Bus
	env      store.Environment
	iris     store.Teammate
	owned    store.Sandbox // Iris's sandbox
	unowned  store.Sandbox
	browser  store.Sandbox // Iris's browser VM
	sibling  store.Teammate
	siblingS store.Sandbox
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	iris, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Ada", Harness: "claude", GitName: "Ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{st: st, env: env, iris: iris, sibling: sibling, bus: &events.Bus{}}
	f.svc = &Service{Store: st, Bus: f.bus}
	if f.owned, err = st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "dev", TeammateID: iris.ID}); err != nil {
		t.Fatal(err)
	}
	if f.unowned, err = st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "scratch"}); err != nil {
		t.Fatal(err)
	}
	if f.browser, err = st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "browser", TeammateID: iris.ID, Kind: store.SandboxKindBrowser}); err != nil {
		t.Fatal(err)
	}
	if f.siblingS, err = st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "ada", TeammateID: sibling.ID}); err != nil {
		t.Fatal(err)
	}
	return f
}

// call runs a task tool from sandbox sb with the arguments as JSON text.
func (f fixture) call(t *testing.T, sb store.Sandbox, method, args string) (any, error) {
	t.Helper()
	return f.svc.HandleCall(context.Background(), sb.ID, method, json.RawMessage(args))
}

// asJSON round-trips a tool result, so it can be read by key.
func asJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func isUserError(err error) bool {
	var ue userError
	return errors.As(err, &ue)
}

func TestTasksFromTheTab(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ch, unsubscribe := f.bus.Subscribe()
	defer unsubscribe()

	task, err := f.svc.Create(ctx, f.env.ID, f.iris.ID, store.TaskByUser, Input{Title: "  Write the report  ", Details: "Scope first.\n", Due: "2026-10-20", Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	// A new task is open, whatever status the request sent.
	if task.Title != "Write the report" || task.Details != "Scope first." || task.Status != store.TaskOpen || task.CreatedBy != store.TaskByUser {
		t.Fatalf("created: %+v", task)
	}
	select {
	case e := <-ch:
		if e.Topic != events.TopicTasks || e.EnvironmentID != f.env.ID {
			t.Fatalf("event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("creating a task published no event")
	}

	for _, in := range []Input{
		{Title: "   "},
		{Title: "Ok", Due: "next week"},
		{Title: "Ok", Details: strings.Repeat("x", teammates.MaxTaskDetailsSize+1)},
	} {
		if _, err := f.svc.Create(ctx, f.env.ID, f.iris.ID, store.TaskByUser, in); !errors.Is(err, teammates.ErrInvalid) {
			t.Errorf("create %+v: %v", in, err)
		}
	}

	edited, err := f.svc.Update(ctx, f.env.ID, f.iris.ID, task.ID, Input{Title: "Write the final report", Details: "", Due: "", Status: store.TaskDone})
	if err != nil || edited.Title != "Write the final report" || edited.Status != store.TaskDone || edited.Details != "" || edited.Due != "" {
		t.Fatalf("update: %+v (%v)", edited, err)
	}
	if _, err := f.svc.Update(ctx, f.env.ID, f.iris.ID, task.ID, Input{Title: "x", Status: "later"}); !errors.Is(err, teammates.ErrInvalid) {
		t.Errorf("an unknown status: %v", err)
	}
	// Another teammate's task is not this one's to edit.
	if _, err := f.svc.Update(ctx, f.env.ID, f.sibling.ID, task.ID, Input{Title: "x", Status: store.TaskOpen}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another teammate's task: %v", err)
	}

	list, err := f.svc.List(ctx, f.env.ID, f.iris.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v (%v)", list, err)
	}
}

func TestTaskToolsActForTheSandboxOwnerOnly(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	created, err := f.call(t, f.owned, "task_create", `{"title":"Check the flaky test","details":"It fails on CI only.","due":"2026-10-21"}`)
	if err != nil {
		t.Fatal(err)
	}
	task := created.(store.Task)
	if task.TeammateID != f.iris.ID || task.CreatedBy != store.TaskByTeammate || task.Status != store.TaskOpen {
		t.Fatalf("task_create: %+v", task)
	}
	// The sibling's sandbox adds to the sibling's list, never to Iris's.
	if _, err := f.call(t, f.siblingS, "task_create", `{"title":"Ada's own"}`); err != nil {
		t.Fatal(err)
	}
	if list, _ := f.svc.List(ctx, f.env.ID, f.sibling.ID); len(list) != 1 || list[0].Title != "Ada's own" {
		t.Fatalf("sibling list: %+v", list)
	}

	if _, err := f.call(t, f.owned, "task_add_note", `{"task_id":"`+task.ID+`","note":"Reproduced on the runner."}`); err != nil {
		t.Fatal(err)
	}
	updated, err := f.call(t, f.owned, "task_update_status", `{"task_id":"`+task.ID+`","status":"in_progress"}`)
	if err != nil || updated.(store.Task).Status != store.TaskInProgress || !strings.Contains(updated.(store.Task).Details, "Reproduced") {
		t.Fatalf("status and note: %+v (%v)", updated, err)
	}

	// tasks_list hides done tasks unless asked for them.
	if _, err := f.call(t, f.owned, "task_update_status", `{"task_id":"`+task.ID+`","status":"done"}`); err != nil {
		t.Fatal(err)
	}
	list, err := f.call(t, f.owned, "tasks_list", `{}`)
	if err != nil || len(asJSON(t, list)["tasks"].([]any)) != 0 {
		t.Fatalf("open tasks: %v (%v)", asJSON(t, list), err)
	}
	list, err = f.call(t, f.owned, "tasks_list", `{"include_done":true}`)
	if err != nil || len(asJSON(t, list)["tasks"].([]any)) != 1 {
		t.Fatalf("all tasks: %v (%v)", asJSON(t, list), err)
	}
}

func TestTaskToolsRefuseWhatTheAgentCannotDo(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	mine, err := f.svc.Create(ctx, f.env.ID, f.sibling.ID, store.TaskByUser, Input{Title: "Ada's task"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		sb   store.Sandbox
		tool string
		args string
	}{
		{"a sandbox without a teammate", f.unowned, "tasks_list", `{}`},
		{"a browser VM", f.browser, "tasks_list", `{}`},
		{"another teammate's task", f.owned, "task_update_status", `{"task_id":"` + mine.ID + `","status":"done"}`},
		{"a missing task", f.owned, "task_add_note", `{"task_id":"nope","note":"x"}`},
		{"an unknown status", f.owned, "task_update_status", `{"task_id":"` + mine.ID + `","status":"later"}`},
		{"an empty note", f.owned, "task_add_note", `{"task_id":"` + mine.ID + `","note":"  "}`},
		{"a status on create", f.owned, "task_create", `{"title":"x","status":"done"}`},
		{"an argument the tool doesn't take", f.owned, "tasks_list", `{"owner":"ada"}`},
		{"an unknown tool", f.owned, "task_delete", `{}`},
	} {
		if _, err := f.call(t, tc.sb, tc.tool, tc.args); !isUserError(err) {
			t.Errorf("%s: want a user error, got %v", tc.name, err)
		}
	}
	// A sandbox Studio doesn't know is refused too.
	if _, err := f.svc.HandleCall(ctx, "no-such-sandbox", "tasks_list", json.RawMessage(`{}`)); !isUserError(err) {
		t.Errorf("an unknown sandbox: %v", err)
	}
	// The sibling's task is still the sibling's, untouched.
	if got, _ := f.st.Task(ctx, f.env.ID, mine.ID); got.Status != store.TaskOpen {
		t.Fatalf("a refused call changed the task: %+v", got)
	}
}

func TestNoteStaysWithinTheDetailsLimit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	task, err := f.svc.Create(ctx, f.env.ID, f.iris.ID, store.TaskByUser, Input{Title: "Long"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AddNote(ctx, f.env.ID, f.iris.ID, task.ID, strings.Repeat("n", teammates.MaxTaskDetailsSize)); err != nil {
		t.Fatalf("a note at the limit: %v", err)
	}
	if _, err := f.svc.AddNote(ctx, f.env.ID, f.iris.ID, task.ID, "one more"); !errors.Is(err, teammates.ErrInvalid) {
		t.Fatalf("a note past the limit: %v", err)
	}
	if got, _ := f.st.Task(ctx, f.env.ID, task.ID); len(got.Details) != teammates.MaxTaskDetailsSize {
		t.Fatalf("details are %d bytes", len(got.Details))
	}
}
