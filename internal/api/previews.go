package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"
)

type previewOpenPath struct {
	Env  string `path:"env" doc:"Environment ID"`
	ID   string `path:"id" doc:"Sandbox ID"`
	Port int    `path:"port" doc:"Preview port" minimum:"1" maximum:"65535"`
}

type previewOpenBody struct {
	URL string `json:"url"`
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
		if _, err := s.Store.Sandbox(ctx, in.Env, in.ID); err != nil {
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
