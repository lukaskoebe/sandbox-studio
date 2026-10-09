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
		Body struct {
			TemplateID string `json:"templateId" minLength:"1" doc:"A ready template in the same environment"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "rebaseSandbox", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/rebase", Tags: []string{"sandboxes"},
		Summary: "Move a sandbox onto another template",
		Description: "Copies /workspace into a new VM built from the template, with the template's resources. " +
			"Docker images, containers and volumes are lost. Tmux sessions end. " +
			"A running sandbox is stopped for the copy and started again; a stopped one stays stopped. " +
			"If the rebase fails, the sandbox is left stopped on its old template.",
	}, func(ctx context.Context, in *rebaseIn) (*sandboxOut, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), transferTimeout)
		defer cancel()
		view, err := s.Sandboxes.Rebase(ctx, in.Env, in.ID, in.Body.TemplateID)
		return &sandboxOut{view}, apiError(err)
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
		Description: "Creates a sandbox with the same template and resources and a copy of /workspace, then starts it. " +
			"Docker state is not copied. A running sandbox is copied while it runs, so files written during the copy may be inconsistent.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *forkIn) (*sandboxOut, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), transferTimeout)
		defer cancel()
		view, err := s.Sandboxes.Fork(ctx, in.Env, in.ID, in.Body.Name)
		return &sandboxOut{view}, apiError(err)
	})
}
