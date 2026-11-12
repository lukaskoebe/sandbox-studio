package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A workspace copy can take a while; it continues if the client disconnects. A disk resize
// copies Docker's data too.
const (
	transferTimeout   = 30 * time.Minute
	diskResizeTimeout = 2 * time.Hour
)

func (s *Server) registerTransfers(api huma.API) {
	type rebaseIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Wait bool   `query:"wait" doc:"Answer once the rebase has ended, with the rebased sandbox or why it failed, instead of at once"`
		Body struct {
			TemplateID string `json:"templateId,omitempty" doc:"A ready template in the same environment; empty or absent for the default image, the base new sandboxes get, keeping the sandbox's resources"`
			KeepTools  *bool  `json:"keepTools,omitempty" doc:"Keep the toolchains in /opt/mise, what mise use installed, which is the default: versions the new template lacks are copied, and the global pins it doesn't have are kept in /opt/mise/config/conf.d/studio-kept-tools.toml. false leaves them out"`
			KeepDocker bool   `json:"keepDocker,omitempty" doc:"Also copy Docker's images, containers and volumes. It takes a while for many images, and the sandbox is stopped for the copy as for any rebase"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "rebaseSandbox", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/rebase", Tags: []string{"sandboxes"},
		Summary: "Move a sandbox onto another template or the current base image",
		Description: "Copies /workspace and /home/agent into a new VM built from the template, with the template's resources, or from the default image with the sandbox's own. " +
			"Software installed outside /workspace and the home, such as apt packages, is lost; the tools mise use installed are kept unless keepTools is false. " +
			"Docker images, containers and volumes are lost unless keepDocker is set. Tmux sessions end. " +
			"A running sandbox is stopped for the copy and started again; a stopped one stays stopped. " +
			"It returns at once in state creating, with the steps in progress; the copy runs in the background. " +
			"If the rebase fails, the sandbox stays on its old template in the state it was in, and its progress names the step that failed " +
			"until the next rebase or until it is dismissed (DELETE …/progress). With wait=true the answer comes once the rebase has ended.",
	}, func(ctx context.Context, in *rebaseIn) (*sandboxOut, error) {
		timeout := transferTimeout
		if in.Body.KeepDocker {
			timeout = diskResizeTimeout
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		keep := sandboxes.RebaseOptions{KeepTools: in.Body.KeepTools == nil || *in.Body.KeepTools, KeepDocker: in.Body.KeepDocker}
		view, err := s.Sandboxes.Rebase(ctx, in.Env, in.ID, in.Body.TemplateID, keep)
		if err != nil || !in.Wait {
			return &sandboxOut{view}, apiError(err)
		}
		if err := s.Sandboxes.Wait(ctx, in.ID); err != nil {
			return nil, apiError(err)
		}
		view, err = s.Sandboxes.Get(ctx, in.Env, in.ID)
		return &sandboxOut{view}, apiError(err)
	})

	type resizeDisksIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Wait bool   `query:"wait" doc:"Answer once the resize has ended, with the resized sandbox or why it failed, instead of at once"`
		Body sandboxes.DiskSizes
	}
	huma.Register(api, huma.Operation{
		OperationID: "resizeSandboxDisks", Method: http.MethodPut,
		Path: "/api/environments/{env}/sandboxes/{id}/disks", Tags: []string{"sandboxes"},
		Summary: "Resize a sandbox's disks",
		Description: "Gives /workspace, Docker's disk and /home/agent new sizes; a size left out stays. A disk can't change its size in place, so this is a rebase onto the sandbox's own template, " +
			"or onto the current base image for a sandbox without one, that keeps its CPUs and memory and copies Docker's images, containers and volumes as well as the workspace and the home. " +
			"A disk may shrink as long as what it holds fits. Software installed outside the three disks, such as apt packages, is lost, and tmux sessions end. " +
			"A running sandbox is stopped for the copy and started again; a stopped one stays stopped. It returns at once in state creating, with the steps in progress. " +
			"If the resize fails, the sandbox stays as it was, and its progress names the step that failed until it is dismissed (DELETE …/progress).",
	}, func(ctx context.Context, in *resizeDisksIn) (*sandboxOut, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), diskResizeTimeout)
		defer cancel()
		view, err := s.Sandboxes.ResizeDisks(ctx, in.Env, in.ID, in.Body)
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
		Summary: "Dismiss a failed rebase or disk resize",
		Description: "Clears the report of the sandbox's failed rebase or disk resize from its progress. " +
			"A running operation, or a failed create or fork, can't be dismissed: delete the sandbox instead.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *sandboxPath) (*struct{}, error) {
		return nil, apiError(s.Sandboxes.ClearProgress(ctx, in.Env, in.ID))
	})

	type forkIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Body struct {
			Name   string `json:"name" minLength:"1" maxLength:"40" doc:"Name of the new sandbox"`
			Docker *bool  `json:"docker,omitempty" doc:"Also copy Docker's images, containers and volumes, which needs a stopped sandbox. Omitted, it follows the sandbox's fork defaults."`
			Tools  *bool  `json:"tools,omitempty" doc:"Also copy the tools installed with mise use and the global mise config. Omitted, it follows the sandbox's fork defaults."`
			Script *bool  `json:"script,omitempty" doc:"Run ~/.config/studio/on-fork.sh in a terminal of the new sandbox once it is up. Omitted, it follows the sandbox's fork defaults."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "forkSandbox", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/fork", Tags: []string{"sandboxes"},
		Summary: "Copy a sandbox into a new one",
		Description: "Creates a sandbox with the same template and resources and a copy of /workspace and /home/agent, then starts it. " +
			"It returns at once in state creating; the copy and boot run in the background, and a failed fork stays as a failed sandbox until it is deleted. " +
			"Docker state and tools are copied when asked for, by the request or the sandbox's fork defaults; Docker state needs a stopped sandbox. A running sandbox is copied while it runs, so files written during the copy may be inconsistent.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *forkIn) (*sandboxOut, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), transferTimeout)
		defer cancel()
		view, err := s.Sandboxes.Fork(ctx, in.Env, in.ID, in.Body.Name, sandboxes.ForkOptions{Docker: in.Body.Docker, Tools: in.Body.Tools, Script: in.Body.Script})
		return &sandboxOut{view}, apiError(err)
	})

	type setBaseIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Body store.ForkDefaults
	}
	huma.Register(api, huma.Operation{
		OperationID: "setSandboxBase", Method: http.MethodPut,
		Path: "/api/environments/{env}/sandboxes/{id}/base", Tags: []string{"sandboxes"},
		Summary:     "Make a sandbox a base",
		Description: "A base is offered as a starting point for new sandboxes, and its forks copy what these defaults say unless the fork request says otherwise. Calling it again changes them.",
	}, func(ctx context.Context, in *setBaseIn) (*sandboxOut, error) {
		view, err := s.Sandboxes.SetBase(ctx, in.Env, in.ID, &in.Body)
		return &sandboxOut{view}, apiError(err)
	})

	type clearBaseIn struct {
		Env string `path:"env" doc:"Environment ID"`
		ID  string `path:"id" doc:"Sandbox ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "clearSandboxBase", Method: http.MethodDelete,
		Path: "/api/environments/{env}/sandboxes/{id}/base", Tags: []string{"sandboxes"},
		Summary:     "Stop a sandbox being a base",
		Description: "The sandbox itself and its existing forks don't change.",
	}, func(ctx context.Context, in *clearBaseIn) (*sandboxOut, error) {
		view, err := s.Sandboxes.SetBase(ctx, in.Env, in.ID, nil)
		return &sandboxOut{view}, apiError(err)
	})
}
