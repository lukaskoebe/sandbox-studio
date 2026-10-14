package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// A workspace copy can take a while; it continues if the client disconnects.
const transferTimeout = 30 * time.Minute

func (s *Server) registerTransfers(api huma.API) {
	type rebaseIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Wait bool   `query:"wait" doc:"Answer once the rebase has ended, with the rebased sandbox or why it failed, instead of at once"`
		Body struct {
			TemplateID string `json:"templateId" minLength:"1" doc:"A ready template in the same environment"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "rebaseSandbox", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/rebase", Tags: []string{"sandboxes"},
		Summary: "Move a sandbox onto another template",
		Description: "Copies /workspace and /home/agent into a new VM built from the template, with the template's resources. " +
			"Docker images, containers and volumes are lost. Tmux sessions end. " +
			"A running sandbox is stopped for the copy and started again; a stopped one stays stopped. " +
			"It returns at once in state creating, with the steps in progress; the copy runs in the background. " +
			"If the rebase fails, the sandbox stays on its old template in the state it was in, and its progress names the step that failed " +
			"until the next rebase or until it is dismissed (DELETE …/progress). With wait=true the answer comes once the rebase has ended.",
	}, func(ctx context.Context, in *rebaseIn) (*sandboxOut, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), transferTimeout)
		defer cancel()
		view, err := s.Sandboxes.Rebase(ctx, in.Env, in.ID, in.Body.TemplateID)
		if err != nil || !in.Wait {
			return &sandboxOut{view}, apiError(err)
		}
		if err := s.Sandboxes.Wait(ctx, in.ID); err != nil {
			return nil, apiError(err)
		}
		view, err = s.Sandboxes.Get(ctx, in.Env, in.ID)
		return &sandboxOut{view}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "dismissSandboxProgress", Method: http.MethodDelete,
		Path: "/api/environments/{env}/sandboxes/{id}/progress", Tags: []string{"sandboxes"},
		Summary: "Dismiss a failed rebase",
		Description: "Clears the report of the sandbox's failed rebase from its progress. " +
			"A running operation, or a failed create or fork, can't be dismissed: delete the sandbox instead.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *sandboxPath) (*struct{}, error) {
		return nil, apiError(s.Sandboxes.ClearProgress(ctx, in.Env, in.ID))
	})

	type forkIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"40" doc:"Name of the new sandbox"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "forkSandbox", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/fork", Tags: []string{"sandboxes"},
		Summary: "Copy a sandbox into a new one",
		Description: "Creates a sandbox with the same template and resources and a copy of /workspace and /home/agent, then starts it. " +
			"It returns at once in state creating; the copy and boot run in the background, and a failed fork stays as a failed sandbox until it is deleted. " +
			"Docker state is not copied. A running sandbox is copied while it runs, so files written during the copy may be inconsistent.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *forkIn) (*sandboxOut, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), transferTimeout)
		defer cancel()
		view, err := s.Sandboxes.Fork(ctx, in.Env, in.ID, in.Body.Name)
		return &sandboxOut{view}, apiError(err)
	})
}
