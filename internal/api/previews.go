package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type previewOpenPath struct {
	Env  string `path:"env" doc:"Environment ID"`
	ID   string `path:"id" doc:"Sandbox ID"`
	Port int    `path:"port" doc:"Preview port" minimum:"1" maximum:"65535"`
	// Embed is for the chat's preview panel, which frames the preview.
	Embed bool `query:"embed" doc:"Log in for an iframe: the preview cookie becomes SameSite=None; Secure; Partitioned"`
	Fresh bool `query:"fresh" doc:"Clear the preview's caches and storage, its service workers among them, as it logs in"`
	// Renew is for the preview panel's hidden frame, which keeps a shown preview's login.
	Renew bool `query:"renew" doc:"Only renew the preview's cookies: the login answers with an empty page instead of the preview"`
	// Keyed is for the desktop app's preview panel, whose webview keeps no cookie in a frame.
	Keyed bool `query:"keyed" doc:"For a webview that keeps no cookie for a framed preview: the URL's host carries the preview's key, which lets it in without one, and stays the same for the preview"`
	// Path is for a link the agent wrote, such as localhost:5173/login.
	Path string `query:"path" maxLength:"2000" doc:"The page to land on after the login, a path on the preview's host such as /login; the root when empty"`
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
		if in.Keyed {
			if u.Host, err = s.Auth.KeyedHost(u.Host); err != nil {
				return nil, apiError(err)
			}
		}
		token, err := s.Auth.IssuePreview(u.Host)
		if err != nil {
			return nil, apiError(err)
		}
		u.Path = "/__studio/preview-login"
		u.RawPath = ""
		query := u.Query()
		query.Set("token", token)
		if in.Embed {
			query.Set("embed", "1")
		}
		if in.Fresh {
			query.Set("fresh", "1")
		}
		if in.Renew {
			query.Set("renew", "1")
		}
		if in.Path != "" && in.Path != "/" {
			// The login checks it again; this only refuses what could never land.
			if !strings.HasPrefix(in.Path, "/") || strings.HasPrefix(in.Path, "//") {
				return nil, huma.Error422UnprocessableEntity("preview path must start with a single /")
			}
			query.Set("next", in.Path)
		}
		u.RawQuery = query.Encode()
		return &struct{ Body previewOpenBody }{Body: previewOpenBody{URL: u.String()}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "signInPreview", Method: http.MethodPut,
		Path:    "/api/environments/{env}/sandboxes/{id}/previews/{port}/basic-auth",
		Tags:    []string{"previews"},
		Summary: "Sign in to a preview that asks for HTTP basic auth: Studio adds the sign-in to the preview's requests until it stops, or until the app refuses it",
	}, func(ctx context.Context, in *struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Port int    `path:"port" doc:"Preview port" minimum:"1" maximum:"65535"`
		Body struct {
			Username string `json:"username" maxLength:"1024"`
			Password string `json:"password" maxLength:"4096"`
		}
	}) (*struct{}, error) {
		if err := s.previewLoginsFor(ctx, previewPort{in.Env, in.ID, in.Port}); err != nil {
			return nil, err
		}
		if strings.Contains(in.Body.Username, ":") {
			return nil, huma.Error422UnprocessableEntity("a basic auth username can't hold a colon")
		}
		s.PreviewLogins.Set(in.ID, in.Port, in.Body.Username, in.Body.Password)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "signOutPreview", Method: http.MethodDelete,
		Path:    "/api/environments/{env}/sandboxes/{id}/previews/{port}/basic-auth",
		Tags:    []string{"previews"},
		Summary: "Forget a preview's basic auth sign-in",
	}, func(ctx context.Context, in *previewPort) (*struct{}, error) {
		if err := s.previewLoginsFor(ctx, *in); err != nil {
			return nil, err
		}
		s.PreviewLogins.Forget(in.ID, in.Port)
		return nil, nil
	})
}

// previewPort names a sandbox's preview.
type previewPort struct {
	Env  string `path:"env" doc:"Environment ID"`
	ID   string `path:"id" doc:"Sandbox ID"`
	Port int    `path:"port" doc:"Preview port" minimum:"1" maximum:"65535"`
}

// previewLoginsFor checks that the preview's sandbox is the environment's and that Studio keeps
// sign-ins.
func (s *Server) previewLoginsFor(ctx context.Context, p previewPort) error {
	if _, err := s.publicSandbox(ctx, p.Env, p.ID); err != nil {
		return apiError(err)
	}
	if s.PreviewLogins == nil {
		return huma.Error503ServiceUnavailable("preview sign-ins are unavailable")
	}
	return nil
}
