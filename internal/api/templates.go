package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

// TemplateView exposes only the fields needed to inspect and launch a ready
// template. Registry and build metadata stay on the host.
type TemplateView struct {
	ID            string              `json:"id"`
	EnvironmentID string              `json:"environmentId"`
	Platform      string              `json:"platform"`
	Resources     resources.Resources `json:"resources"`
	CreatedAt     time.Time           `json:"createdAt"`
}

type templatePath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Template ID"`
}

func (s *Server) registerTemplates(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listTemplates", Method: http.MethodGet, Path: "/api/environments/{env}/templates", Tags: []string{"templates"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []TemplateView }, error) {
		if _, err := s.Store.Environment(ctx, in.Env); err != nil {
			return nil, templateAPIError(err)
		}
		templates, err := s.Store.Templates(ctx, in.Env)
		if err != nil {
			return nil, templateAPIError(err)
		}
		out := make([]TemplateView, 0, len(templates))
		for _, template := range templates {
			if template.State != store.TemplateStateReady {
				continue
			}
			view, err := templateView(template)
			if err != nil {
				return nil, templateAPIError(err)
			}
			out = append(out, view)
		}
		return &struct{ Body []TemplateView }{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getTemplate", Method: http.MethodGet, Path: "/api/environments/{env}/templates/{id}", Tags: []string{"templates"},
	}, func(ctx context.Context, in *templatePath) (*struct{ Body TemplateView }, error) {
		template, err := s.Store.Template(ctx, in.Env, in.ID)
		if err != nil {
			return nil, templateAPIError(err)
		}
		if template.State != store.TemplateStateReady {
			return nil, templateAPIError(store.ErrConflict)
		}
		view, err := templateView(template)
		return &struct{ Body TemplateView }{view}, templateAPIError(err)
	})

	type createTemplateSandboxIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Template ID"`
		Body struct {
			Name      string `json:"name" minLength:"1" maxLength:"40"`
			PersonaID string `json:"personaId,omitempty" doc:"The persona that owns the sandbox; omit for an unowned sandbox"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "createTemplateSandbox", Method: http.MethodPost, Path: "/api/environments/{env}/templates/{id}/sandboxes", Tags: []string{"templates", "sandboxes"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createTemplateSandboxIn) (*sandboxOut, error) {
		if err := s.checkPersona(ctx, in.Env, in.Body.PersonaID); err != nil {
			return nil, err
		}
		// Booting continues if the browser disconnects; template cleanup keeps its
		// catalog pin whenever exact VM absence cannot be established.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		view, err := s.Sandboxes.CreateFromTemplateFor(ctx, in.Env, in.ID, in.Body.Name, in.Body.PersonaID)
		return &sandboxOut{view}, templateAPIError(err)
	})
}

func templateAPIError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("template or environment not found")
	case errors.Is(err, store.ErrExists), errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict("template or sandbox name conflicts with existing state")
	case errors.Is(err, runtime.ErrInvalidName):
		return huma.Error422UnprocessableEntity("invalid sandbox name")
	default:
		return huma.Error500InternalServerError("template operation failed")
	}
}

func templateView(template store.Template) (TemplateView, error) {
	spec, err := templatespec.ParseCanonicalJSON([]byte(template.Spec))
	if err != nil {
		return TemplateView{}, errors.New("template metadata is invalid")
	}
	return TemplateView{
		ID: template.ID, EnvironmentID: template.EnvironmentID,
		Platform: template.Platform, Resources: spec.Resources, CreatedAt: template.CreatedAt,
	}, nil
}
