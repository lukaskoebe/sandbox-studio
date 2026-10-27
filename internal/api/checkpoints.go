package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const checkpointMutationTimeout = 5 * time.Minute

type checkpointPath struct {
	Env        string `path:"env" doc:"Environment ID"`
	ID         string `path:"id" doc:"Sandbox ID"`
	Checkpoint string `path:"checkpoint" doc:"Checkpoint ID"`
}

func checkpointMutationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), checkpointMutationTimeout)
}

func (s *Server) registerCheckpoints(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listCheckpoints", Method: http.MethodGet,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoints", Tags: []string{"checkpoints"},
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []store.Checkpoint }, error) {
		checkpoints, err := s.Sandboxes.Checkpoints(ctx, in.Env, in.ID)
		if checkpoints == nil {
			checkpoints = []store.Checkpoint{}
		}
		return &struct{ Body []store.Checkpoint }{checkpoints}, apiError(err)
	})

	type createCheckpointIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"80" doc:"Human-readable checkpoint name"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "createCheckpoint", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoints", Tags: []string{"checkpoints"},
		Summary:       "Create a disk checkpoint",
		Description:   "Captures the disks of a running or stopped sandbox. A running sandbox is frozen while its filesystems are flushed; one created with systemd as its init must be stopped first.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createCheckpointIn) (*struct{ Body store.Checkpoint }, error) {
		ctx, cancel := checkpointMutationContext(ctx)
		defer cancel()
		checkpoint, err := s.Sandboxes.CreateCheckpoint(ctx, in.Env, in.ID, in.Body.Name)
		return &struct{ Body store.Checkpoint }{checkpoint}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteCheckpoint", Method: http.MethodDelete,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}", Tags: []string{"checkpoints"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *checkpointPath) (*struct{}, error) {
		ctx, cancel := checkpointMutationContext(ctx)
		defer cancel()
		return nil, apiError(s.Sandboxes.DeleteCheckpoint(ctx, in.Env, in.ID, in.Checkpoint))
	})

	type renameCheckpointIn struct {
		Env        string `path:"env" doc:"Environment ID"`
		ID         string `path:"id" doc:"Sandbox ID"`
		Checkpoint string `path:"checkpoint" doc:"Checkpoint ID"`
		Body       struct {
			Name string `json:"name" minLength:"1" maxLength:"80" doc:"Human-readable checkpoint name"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "renameCheckpoint", Method: http.MethodPut,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}", Tags: []string{"checkpoints"},
	}, func(ctx context.Context, in *renameCheckpointIn) (*struct{ Body store.Checkpoint }, error) {
		checkpoint, err := s.Sandboxes.RenameCheckpoint(ctx, in.Env, in.ID, in.Checkpoint, in.Body.Name)
		return &struct{ Body store.Checkpoint }{checkpoint}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "restoreCheckpoint", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}/restore", Tags: []string{"checkpoints"},
	}, func(ctx context.Context, in *checkpointPath) (*sandboxOut, error) {
		ctx, cancel := checkpointMutationContext(ctx)
		defer cancel()
		view, err := s.Sandboxes.RestoreCheckpoint(ctx, in.Env, in.ID, in.Checkpoint)
		return &sandboxOut{view}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getCheckpointSchedule", Method: http.MethodGet,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoint-schedule", Tags: []string{"checkpoints"},
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body store.CheckpointSchedule }, error) {
		cs, err := s.Sandboxes.CheckpointSchedule(ctx, in.Env, in.ID)
		return &struct{ Body store.CheckpointSchedule }{cs}, apiError(err)
	})

	type setCheckpointScheduleIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Body struct {
			EveryHours int  `json:"everyHours" enum:"0,1,4,12,24" doc:"Hours between scheduled checkpoints; 0 turns the schedule off, unless afterTasks"`
			AfterTasks bool `json:"afterTasks,omitempty" doc:"Take a checkpoint after every agent turn that changed files instead, with everyHours 0"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setCheckpointSchedule", Method: http.MethodPut,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoint-schedule", Tags: []string{"checkpoints"},
		Summary: "Take checkpoints on a schedule",
		Description: "Studio takes a checkpoint every everyHours while the sandbox runs, or once after it stopped, or with afterTasks after every agent turn that changed files. " +
			"It deletes the older ones it took: " +
			"it keeps the newest three and the newest of each of the seven days before. Checkpoints taken by hand, or renamed, are never deleted. " +
			"Turning the schedule off keeps the checkpoints it took.",
	}, func(ctx context.Context, in *setCheckpointScheduleIn) (*struct{ Body store.CheckpointSchedule }, error) {
		cs, err := s.Sandboxes.SetCheckpointSchedule(ctx, in.Env, in.ID, in.Body.EveryHours, in.Body.AfterTasks)
		return &struct{ Body store.CheckpointSchedule }{cs}, apiError(err)
	})
}
