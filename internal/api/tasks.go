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

// registerTasks adds a teammate's task list and its heartbeat schedule and log (docs/teammates.md).
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
		Body tasks.Input
	}
	huma.Register(api, huma.Operation{
		OperationID: "createTeammateTask", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{id}/tasks", Tags: []string{"teammates"},
		DefaultStatus: http.StatusCreated,
		Description:   "Adds a task to the teammate's list, created by the user. A new task is open; its status is not read.",
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
		Body tasks.Input
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateTeammateTask", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{id}/tasks/{task}", Tags: []string{"teammates"},
		Description: "Replaces the task's title, details, due day and status. Status done marks it finished.",
	}, func(ctx context.Context, in *updateTaskIn) (*struct{ Body store.Task }, error) {
		t, err := s.tasks().Update(ctx, in.Env, in.ID, in.Task, in.Body)
		return &struct{ Body store.Task }{t}, apiError(err)
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
