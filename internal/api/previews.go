package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type previewOpenPath struct {
	Env  string `path:"env" doc:"Environment ID"`
	ID   string `path:"id" doc:"Sandbox ID"`
	Port int    `path:"port" doc:"Preview port" minimum:"1" maximum:"65535"`
}

type previewOpenBody struct {
	URL string `json:"url"`
}

// publicSandbox applies the sandbox visibility boundary to public API validation paths.
// Some API tests exercise a Server without a manager, so the raw-store fallback preserves
// the same owner check without dereferencing a nil Manager.
func (s *Server) publicSandbox(ctx context.Context, envID, sandboxID string) (store.Sandbox, error) {
	if s.Sandboxes != nil {
		return s.Sandboxes.PublicSandbox(ctx, envID, sandboxID)
	}
	sb, err := s.Store.Sandbox(ctx, envID, sandboxID)
	if err != nil {
		return store.Sandbox{}, err
	}
	if !sandboxes.Public(sb) {
		return store.Sandbox{}, store.ErrNotFound
	}
	return sb, nil
}

func (s *Server) registerPreviews(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "openPreview", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/previews/{port}/open",
		Tags: []string{"previews"},
	}, func(ctx context.Context, in *previewOpenPath) (*struct{ Body previewOpenBody }, error) {
		if in.Port < 1 || in.Port > 65535 {
			return nil, huma.Error422UnprocessableEntity("preview port must be between 1 and 65535")
		}
		if _, err := s.publicSandbox(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		if s.Auth == nil {
			return nil, huma.Error503ServiceUnavailable("preview authentication is unavailable")
		}

		u, err := url.Parse(s.previewURL(in.ID, in.Port))
		if err != nil {
			return nil, apiError(err)
		}
		token, err := s.Auth.IssuePreview(u.Host)
		if err != nil {
			return nil, apiError(err)
		}
		u.Path = "/__studio/preview-login"
		u.RawPath = ""
		query := u.Query()
		query.Set("token", token)
		u.RawQuery = query.Encode()
		return &struct{ Body previewOpenBody }{Body: previewOpenBody{URL: u.String()}}, nil
	})
}
