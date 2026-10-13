package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestTeammateTasksAndHeartbeatAPI(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	env, err := s.Store.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	iris, err := s.Store.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	base := "http://localhost:7878/api/environments/" + env.ID + "/teammates/" + iris.ID
	const missing = "http://localhost:7878/api/environments/" + "nope" + "/teammates/nope"

	// Tasks: add, list, edit, mark done.
	rec := do(h, "POST", base+"/tasks", `{"title":"Write the report","details":"Scope first.","due":"2026-10-20"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var task store.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil || task.Status != store.TaskOpen || task.CreatedBy != store.TaskByUser || task.Due != "2026-10-20" {
		t.Fatalf("created task: %s (%v)", rec.Body, err)
	}
	if rec := do(h, "POST", base+"/tasks", `{"title":"   "}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an empty title: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/tasks", `{"title":"Ok","due":"someday"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a bad due day: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", missing+"/tasks", `{"title":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("tasks of an unknown teammate: %d %s", rec.Code, rec.Body)
	}

	rec = do(h, "GET", base+"/tasks", "")
	var list []store.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &list); rec.Code != http.StatusOK || err != nil || len(list) != 1 || list[0].ID != task.ID {
		t.Fatalf("list: %d %s (%v)", rec.Code, rec.Body, err)
	}

	rec = do(h, "PUT", base+"/tasks/"+task.ID, `{"title":"Write the report","details":"Scope first.","due":"2026-10-20","status":"done"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark done: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil || task.Status != store.TaskDone {
		t.Fatalf("done task: %s (%v)", rec.Body, err)
	}
	if rec := do(h, "PUT", base+"/tasks/"+task.ID, `{"title":"x","status":"later"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown status: %d %s", rec.Code, rec.Body)
	}

	// The schedule: on with a window, refused when the window is empty, off again.
	rec = do(h, "PUT", base+"/heartbeat", `{"everyHours":4,"fromHour":9,"toHour":18}`)
	var got store.Teammate
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil || got.Heartbeat == nil || got.Heartbeat.EveryHours != 4 || got.Heartbeat.ToHour != 18 {
		t.Fatalf("set heartbeat: %d %s (%v)", rec.Code, rec.Body, err)
	}
	if rec := do(h, "PUT", base+"/heartbeat", `{"everyHours":4,"fromHour":18,"toHour":9}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an empty window: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", base+"/heartbeat", `{"everyHours":0,"fromHour":9,"toHour":18}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("zero hours: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "GET", base, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Heartbeat == nil {
		t.Fatalf("teammate after set: %s (%v)", rec.Body, err)
	}
	if rec := do(h, "DELETE", base+"/heartbeat", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("clear heartbeat: %d %s", rec.Code, rec.Body)
	}
	// A fresh value: omitted fields keep what an earlier decode left in a reused struct.
	var off store.Teammate
	rec = do(h, "GET", base, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &off); err != nil || off.Heartbeat != nil {
		t.Fatalf("teammate after clear: %s (%v)", rec.Body, err)
	}

	// No check-ins have been logged yet.
	rec = do(h, "GET", base+"/heartbeats", "")
	var beats []store.Beat
	if err := json.Unmarshal(rec.Body.Bytes(), &beats); rec.Code != http.StatusOK || err != nil || len(beats) != 0 {
		t.Fatalf("beats: %d %s (%v)", rec.Code, rec.Body, err)
	}
}
