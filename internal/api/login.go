package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/login"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// LoginView is a provider's sign-in as the API shows it: login.View, under a name of its own
// for the OpenAPI schema, which is keyed by type name.
type LoginView login.View

// registerLogins adds the sign-ins of subscription providers (docs/teammates.md, subscription
// logins). A sign-in runs in a system VM and is polled here; its terminal is attached below.
func (s *Server) registerLogins(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "startProviderLogin", Method: http.MethodPost, Path: "/api/environments/{env}/providers/{id}/login", Tags: []string{"providers"},
		DefaultStatus: http.StatusAccepted,
		Description:   "Starts the sign-in of a subscription provider. Its CLI runs in a system VM, the user finishes in their browser, and Studio stores the result as the provider's login. Poll the status, or attach the terminal, which the dialog shows.",
	}, func(ctx context.Context, in *providerPath) (*struct{ Body LoginView }, error) {
		if err := s.checkLogins(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Login.Start(ctx, in.Env, in.ID)
		if err != nil {
			return nil, loginError(err)
		}
		return &struct{ Body LoginView }{LoginView(v)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getProviderLogin", Method: http.MethodGet, Path: "/api/environments/{env}/providers/{id}/login", Tags: []string{"providers"},
		Description: "The sign-in of a subscription provider as the dialog shows it: idle when none is running.",
	}, func(ctx context.Context, in *providerPath) (*struct{ Body LoginView }, error) {
		if err := s.checkLogins(ctx, in.Env); err != nil {
			return nil, err
		}
		return &struct{ Body LoginView }{LoginView(s.Login.Status(in.ID))}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "cancelProviderLogin", Method: http.MethodDelete, Path: "/api/environments/{env}/providers/{id}/login", Tags: []string{"providers"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Ends a running sign-in and removes its system VM. Without one it does nothing.",
	}, func(ctx context.Context, in *providerPath) (*struct{}, error) {
		if err := s.checkLogins(ctx, in.Env); err != nil {
			return nil, err
		}
		return nil, loginError(s.Login.Cancel(ctx, in.Env, in.ID))
	})

	type pasteTokenIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Provider ID"`
		Body struct {
			Token string `json:"token" minLength:"1" maxLength:"256" doc:"The long-lived token from claude setup-token, as the terminal printed it"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "pasteProviderLoginToken", Method: http.MethodPost, Path: "/api/environments/{env}/providers/{id}/login/token", Tags: []string{"providers"},
		Description: "Stores a Claude Code token the user copied from the terminal, when Studio couldn't read it there. It ends a running sign-in.",
	}, func(ctx context.Context, in *pasteTokenIn) (*struct{ Body ProviderView }, error) {
		if err := s.checkLogins(ctx, in.Env); err != nil {
			return nil, err
		}
		if err := s.Login.PasteToken(ctx, in.Env, in.ID, in.Body.Token); err != nil {
			return nil, loginError(err)
		}
		p, err := s.Store.Provider(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body ProviderView }{s.providerViewOf(ctx, p)}, nil
	})
}

// checkLogins answers 503 when logins aren't available, and 404 for an environment that isn't there.
func (s *Server) checkLogins(ctx context.Context, envID string) error {
	if s.Login == nil {
		return huma.Error503ServiceUnavailable("subscription logins are not available")
	}
	return s.checkEnvironment(ctx, envID)
}

// loginError maps a sign-in error to the API's answer.
func loginError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, login.ErrNotSubscription), errors.Is(err, login.ErrInvalidToken):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, login.ErrNoLogin):
		return huma.Error409Conflict(err.Error())
	}
	return apiError(err)
}

// attachLoginTerminal bridges a websocket to the terminal of a sign-in in progress: the CLI's
// tmux session in the login VM. It takes the same messages as attachTerminal.
func (s *Server) attachLoginTerminal(w http.ResponseWriter, r *http.Request) {
	if s.Login == nil {
		http.Error(w, "subscription logins are not available", http.StatusServiceUnavailable)
		return
	}
	cols, _ := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
	rows, _ := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)
	pty, err := s.Login.Terminal(r.Context(), r.PathValue("env"), r.PathValue("id"), uint16(cols), uint16(rows))
	switch {
	case errors.Is(err, login.ErrNoLogin):
		http.Error(w, "no sign-in is running for this provider", http.StatusConflict)
		return
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	bridgeTerminal(w, r, pty)
}
