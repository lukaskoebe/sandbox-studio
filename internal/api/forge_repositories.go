package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
)

// cloneTimeout bounds one clone in a sandbox, in seconds.
const cloneTimeout = 10 * 60

// guestRunner runs one command in a sandbox as the agent user (agentproto.KindRun).
type guestRunner interface {
	Run(ctx context.Context, id string, req agentproto.RunRequest) (agentproto.RunResult, error)
}

// runner is the guest channel's runner: the test guest's, if it runs commands, or the hub's.
func (s *Server) runner() guestRunner {
	if r, ok := s.Guest.(guestRunner); ok {
		return r
	}
	return s.Sandboxes.Hub
}

// CloneInput names a repository of a forge to clone into a sandbox.
type CloneInput struct {
	ForgeID string `json:"forgeId" minLength:"1" doc:"Forge ID"`
	Repo    string `json:"repo" minLength:"1" maxLength:"800" doc:"The repository's path on the forge, such as owner/repo"`
}

// CloneResult says where a repository was cloned to.
type CloneResult struct {
	Dir string `json:"dir" doc:"The clone in the sandbox, /workspace/<repository name>"`
}

func (s *Server) registerForgeRepositories(api huma.API) {
	type listIn struct {
		Env string `path:"env" doc:"Environment ID"`
		ID  string `path:"id" doc:"Forge ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listForgeRepositories", Method: http.MethodGet, Path: "/api/environments/{env}/forges/{id}/repositories", Tags: []string{"git"},
		Summary: "List a forge's repositories",
		Description: fmt.Sprintf("The repositories Studio's credential reaches, through the forge's API (Forgejo, GitHub, GitLab), recently active first; "+
			"for a local forge, the repositories in its folder and one level down. At most %d. Forges of kind https and ssh have no list.", gitreview.MaxRepositories),
	}, func(ctx context.Context, in *listIn) (*struct{ Body []gitreview.Repo }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if s.Git == nil {
			return nil, huma.Error503ServiceUnavailable("the git remote is not running")
		}
		repos, err := s.Git.Repositories(ctx, f)
		switch {
		case errors.Is(err, gitreview.ErrInvalid):
			return nil, huma.Error422UnprocessableEntity(strings.TrimPrefix(err.Error(), gitreview.ErrInvalid.Error()+": "))
		case err != nil:
			return nil, huma.Error502BadGateway(err.Error())
		}
		return &struct{ Body []gitreview.Repo }{repos}, nil
	})

	type cloneIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Body CloneInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "cloneRepository", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/clone", Tags: []string{"git"},
		Summary: "Clone a forge's repository into a sandbox",
		Description: "Runs git clone as the agent user in the running sandbox, from the virtual remote https://" + gitreview.Host +
			"/<forge>/<path>.git into /workspace/<repository name>, so the clone fetches and pushes through Studio like any other. " +
			"A folder of that name that is not empty fails the clone.",
	}, func(ctx context.Context, in *cloneIn) (*struct{ Body CloneResult }, error) {
		sb, err := s.sessionSandbox(ctx, in.Env, in.ID)
		if err != nil {
			return nil, err
		}
		f, err := s.Store.Forge(ctx, in.Env, in.Body.ForgeID)
		if err != nil {
			return nil, apiError(err)
		}
		// The path's segments are repository names, so the folder stays in /workspace and no
		// argument starts with a dash.
		path, name, err := gitreview.CheckRepoPath(f.Kind, in.Body.Repo)
		if err != nil {
			return nil, apiError(err)
		}
		remote := "https://" + gitreview.Host + "/" + f.Name + "/" + path + ".git"
		dir := "/workspace/" + name
		res, err := s.runner().Run(ctx, sb.ID, agentproto.RunRequest{
			Args:       []string{"git", "clone", "--", remote, dir},
			Env:        map[string]string{"GIT_TERMINAL_PROMPT": "0"},
			TimeoutSec: cloneTimeout,
		})
		if err != nil {
			return nil, apiError(err)
		}
		if res.ExitCode != 0 {
			return nil, huma.Error409Conflict("git clone " + path + ": " + lastLines(res.Stderr, 5))
		}
		return &struct{ Body CloneResult }{CloneResult{Dir: dir}}, nil
	})
}

// lastLines is the end of a command's output, for an error message.
func lastLines(out string, n int) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	msg := strings.Join(lines, " ")
	if len(msg) > 600 {
		msg = msg[len(msg)-600:]
	}
	if msg == "" {
		return "failed"
	}
	return msg
}
