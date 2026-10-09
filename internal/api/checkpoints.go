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
		Description:   "Stop the sandbox first; checkpoint capture requires a stopped sandbox.",
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

	huma.Register(api, huma.Operation{
		OperationID: "restoreCheckpoint", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}/restore", Tags: []string{"checkpoints"},
	}, func(ctx context.Context, in *checkpointPath) (*sandboxOut, error) {
		ctx, cancel := checkpointMutationContext(ctx)
		defer cancel()
		view, err := s.Sandboxes.RestoreCheckpoint(ctx, in.Env, in.ID, in.Checkpoint)
		return &sandboxOut{view}, apiError(err)
	})
}
