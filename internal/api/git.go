package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// decideIntegration settles an approval an integration owns. ok is false for approvals
// that belong to the network policy.
func (s *Server) decideIntegration(ctx context.Context, env, id string, d integrations.Decision) (store.Approval, bool, error) {
	a, err := s.Store.Approval(ctx, env, id)
	if err != nil {
		return a, false, apiError(err)
	}
	k, ok := s.Integrations.Kind(a.Kind)
	if !ok {
		return a, false, nil
	}
	if a.Status != store.StatusPending {
		return a, true, huma.Error409Conflict("the approval was already decided")
	}
	if err := k.Decide(ctx, a, d); err != nil {
		return a, true, apiError(err)
	}
	a, err = s.Store.Approval(ctx, env, id)
	return a, true, apiError(err)
}

// ForgeInput describes a forge to add.
type ForgeInput struct {
	Name    string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,31}$" doc:"Names the forge in the remote URL: https://git.studio.internal/<name>/<owner>/<repo>.git"`
	Kind    string `json:"kind" enum:"forgejo,github" doc:"github is not implemented yet"`
	BaseURL string `json:"baseUrl" maxLength:"2048" doc:"The forge's public HTTPS URL, such as https://codeberg.org"`
	Token   string `json:"token" minLength:"1" maxLength:"4096" doc:"An access token with read and write access to the repositories; Studio keeps it and sandboxes never see it"`
}

// ForgeTest is the outcome of checking a forge's token.
type ForgeTest struct {
	OK      bool   `json:"ok"`
	User    string `json:"user,omitempty" doc:"The user the token belongs to"`
	Message string `json:"message,omitempty"`
}

func (s *Server) registerForges(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listForges", Method: http.MethodGet, Path: "/api/environments/{env}/forges", Tags: []string{"git"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Forge }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.Forges(ctx, in.Env)
		return &struct{ Body []store.Forge }{list}, apiError(err)
	})

	type createIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body ForgeInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createForge", Method: http.MethodPost, Path: "/api/environments/{env}/forges", Tags: []string{"git"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createIn) (*struct{ Body store.Forge }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		b := in.Body
		base, host, err := gitreview.CheckForge(b.Name, b.Kind, b.BaseURL)
		if err != nil {
			return nil, apiError(err)
		}
		secretName := "FORGE_" + strings.ToUpper(strings.ReplaceAll(b.Name, "-", "_")) + "_TOKEN"
		sec, err := s.Vault.Prepare(in.Env, secretName, b.Token, []string{host}, "Token of forge "+b.Name)
		if err != nil {
			return nil, apiError(err)
		}
		f, err := s.Store.CreateForge(ctx, store.Forge{ID: store.NewID(), EnvironmentID: in.Env, Name: b.Name, Kind: b.Kind, BaseURL: base}, sec)
		if err != nil {
			return nil, apiError(err)
		}
		s.Vault.Changed(in.Env)
		s.publishSecrets(in.Env)
		return &struct{ Body store.Forge }{f}, nil
	})

	type forgePath struct {
		Env string `path:"env" doc:"Environment ID"`
		ID  string `path:"id" doc:"Forge ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "deleteForge", Method: http.MethodDelete, Path: "/api/environments/{env}/forges/{id}", Tags: []string{"git"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Removes the forge, its token and the staged pushes to it. Pending pushes can no longer be approved.",
	}, func(ctx context.Context, in *forgePath) (*struct{}, error) {
		if err := s.Store.DeleteForge(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		if s.Git != nil {
			s.Git.Forget(in.Env, in.ID)
		}
		s.Vault.Changed(in.Env)
		s.publishSecrets(in.Env)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "testForge", Method: http.MethodPost, Path: "/api/environments/{env}/forges/{id}/test", Tags: []string{"git"},
		Description: "Checks that the forge answers and accepts the token.",
	}, func(ctx context.Context, in *forgePath) (*struct{ Body ForgeTest }, error) {
		f, err := s.Store.Forge(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if s.Git == nil {
			return nil, huma.Error503ServiceUnavailable("the git remote is not running")
		}
		user, err := s.Git.TestForge(ctx, f)
		if err != nil {
			return &struct{ Body ForgeTest }{ForgeTest{Message: fmt.Sprint(err)}}, nil
		}
		return &struct{ Body ForgeTest }{ForgeTest{OK: true, User: user}}, nil
	})
}
