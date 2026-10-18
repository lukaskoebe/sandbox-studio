package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/tasks"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// tasks is the task service the API uses. It holds no state of its own, so each request gets one.
func (s *Server) tasks() *tasks.Service {
	return &tasks.Service{Store: s.Store, Bus: s.Bus, Log: s.Log}
}

// TaskRunner starts runs of scheduled tasks: the heartbeat service.
type TaskRunner interface {
	RunNow(ctx context.Context, envID, teammateID, taskID string) (store.TaskRun, error)
}

// registerTasks adds a teammate's task list, its tasks' schedules and runs, and its heartbeat
// schedule and log (docs/teammates.md).
func (s *Server) registerTasks(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listTeammateTasks", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{id}/tasks", Tags: []string{"teammates"},
		Description: "The teammate's tasks: the ones not done first, then oldest first.",
	}, func(ctx context.Context, in *teammatePath) (*struct{ Body []store.Task }, error) {
		if _, err := s.Store.Teammate(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		list, err := s.tasks().List(ctx, in.Env, in.ID)
		return &struct{ Body []store.Task }{list}, apiError(err)
	})

	type createTaskIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Teammate ID"`
		Body tasks.TaskInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createTeammateTask", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{id}/tasks", Tags: []string{"teammates"},
		DefaultStatus: http.StatusCreated,
		Description: "Adds a task to the teammate's list, created by the user. A new task is open; its status is not read. " +
			"A schedule runs it in a chat of its own at its times, in the sandbox sandboxId or the teammate's latest.",
	}, func(ctx context.Context, in *createTaskIn) (*struct{ Body store.Task }, error) {
		if _, err := s.Store.Teammate(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		t, err := s.tasks().Create(ctx, in.Env, in.ID, store.TaskByUser, in.Body)
		return &struct{ Body store.Task }{t}, apiError(err)
	})

	type updateTaskIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Teammate ID"`
		Task string `path:"task" doc:"Task ID"`
		Body tasks.TaskInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateTeammateTask", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{id}/tasks/{task}", Tags: []string{"teammates"},
		Description: "Replaces the task's title, details, due day, status, schedule and sandbox: a request without a schedule removes it. " +
			"Status done marks the task finished, and ends a repeating schedule.",
	}, func(ctx context.Context, in *updateTaskIn) (*struct{ Body store.Task }, error) {
		t, err := s.tasks().Update(ctx, in.Env, in.ID, in.Task, in.Body)
		return &struct{ Body store.Task }{t}, apiError(err)
	})

	type taskPath struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Teammate ID"`
		Task string `path:"task" doc:"Task ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "runTeammateTask", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{id}/tasks/{task}/run", Tags: []string{"teammates"},
		DefaultStatus: http.StatusCreated,
		Description: "Starts a run of the task now, in a chat of its own, as its schedule would. It makes the same checks but doesn't wait: " +
			"a run that can't start now, such as while a turn runs in the sandbox, is a 409 that says why. The schedule's next run stays.",
	}, func(ctx context.Context, in *taskPath) (*struct{ Body store.TaskRun }, error) {
		if s.TaskRuns == nil {
			return nil, huma.Error503ServiceUnavailable("scheduled runs are not available")
		}
		// The run goes on if the request ends while its sandbox starts.
		run, err := s.TaskRuns.RunNow(context.WithoutCancel(ctx), in.Env, in.ID, in.Task)
		return &struct{ Body store.TaskRun }{run}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "listTaskRuns", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{id}/tasks/{task}/runs", Tags: []string{"teammates"},
		Description: "The task's logged runs, newest first, at most 100: started or done with their chat, skipped with why, or missed.",
	}, func(ctx context.Context, in *taskPath) (*struct{ Body []store.TaskRun }, error) {
		t, err := s.Store.Task(ctx, in.Env, in.Task)
		if err == nil && t.TeammateID != in.ID {
			err = store.ErrNotFound
		}
		if err != nil {
			return nil, apiError(err)
		}
		list, err := s.Store.TaskRuns(ctx, in.Env, in.Task, 100)
		return &struct{ Body []store.TaskRun }{list}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "listTeammateTaskRuns", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{id}/task-runs", Tags: []string{"teammates"},
		Description: "The logged runs of all the teammate's scheduled tasks, newest first, at most 20, for its activity.",
	}, func(ctx context.Context, in *teammatePath) (*struct{ Body []store.TaskRun }, error) {
		list, err := s.Store.TeammateTaskRuns(ctx, in.Env, in.ID, 20)
		return &struct{ Body []store.TaskRun }{list}, apiError(err)
	})

	type heartbeatIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Teammate ID"`
		Body store.Heartbeat
	}
	huma.Register(api, huma.Operation{
		OperationID: "setTeammateHeartbeat", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{id}/heartbeat", Tags: []string{"teammates"},
		Description: "Turns the teammate's check-ins on, or changes their schedule: every everyHours hours while the local hour is in " +
			"[fromHour, toHour). A check-in gives the teammate a chat turn in its latest sandbox when it has open tasks.",
	}, func(ctx context.Context, in *heartbeatIn) (*struct{ Body store.Teammate }, error) {
		if err := teammates.CheckHeartbeat(in.Body); err != nil {
			return nil, apiError(err)
		}
		p, err := s.Store.SetHeartbeat(ctx, in.Env, in.ID, &in.Body)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body store.Teammate }{p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "clearTeammateHeartbeat", Method: http.MethodDelete, Path: "/api/environments/{env}/teammates/{id}/heartbeat", Tags: []string{"teammates"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Turns the teammate's check-ins off. The schedule's window is kept for the next time.",
	}, func(ctx context.Context, in *teammatePath) (*struct{}, error) {
		if _, err := s.Store.SetHeartbeat(ctx, in.Env, in.ID, nil); err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listHeartbeats", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{id}/heartbeats", Tags: []string{"teammates"},
		Description: "The teammate's logged check-ins, newest first: the heartbeat chats they started, and the ones skipped with why.",
	}, func(ctx context.Context, in *teammatePath) (*struct{ Body []store.Beat }, error) {
		list, err := s.Store.Beats(ctx, in.Env, in.ID, 20)
		return &struct{ Body []store.Beat }{list}, apiError(err)
	})
}
