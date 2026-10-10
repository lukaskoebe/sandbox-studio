// Package api implements the Studio HTTP API under /api. Operations are declared with huma,
// which also produces the OpenAPI document the web client's types are generated from.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/caddyrule"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// Server holds what the handlers need.
type Server struct {
	Store     *store.Store
	Builds    BuildService
	Sandboxes *sandboxes.Manager
	Policy    *policy.Engine
	Vault     *secrets.Vault
	Bus       *events.Bus
	Conns     *gateway.ConnLog
	Caddy     *caddyrule.Engine // checks Caddyfiles; without it, caddy rules are refused
	Auth      *webauth.Auth
	Log       *slog.Logger
	Addr      string // the address Studio listens on, used to build preview URLs
}

// BuildService is the optional durable template-build backend. Keeping this
// interface narrow lets the API depend on the worker's public operations only.
type BuildService interface {
	Submit(context.Context, string, string) (store.BuildJob, bool, error)
	Cancel(context.Context, string, string) (store.BuildJob, error)
}

// Register adds the API operations and websocket endpoints to mux.
func (s *Server) Register(mux *http.ServeMux) huma.API {
	api := humago.New(mux, config())
	s.registerEnvironments(api)
	s.registerBuilds(api)
	s.registerSandboxes(api)
	s.registerTemplates(api)
	s.registerCheckpoints(api)
	s.registerTransfers(api)
	s.registerPreviews(api)
	s.registerNetwork(api)
	s.registerSecrets(api)
	mux.HandleFunc("GET /api/events", s.streamEvents)
	mux.HandleFunc("GET /api/environments/{env}/sandboxes/{id}/terminals/{name}/attach", s.attachTerminal)
	return api
}

// OpenAPI returns the API description without needing a running server.
func OpenAPI() *huma.OpenAPI {
	return (&Server{}).Register(http.NewServeMux()).OpenAPI()
}

func config() huma.Config {
	c := huma.DefaultConfig("Sandbox Studio", version.Version)
	c.OpenAPIPath = "/api/openapi"
	c.DocsPath = ""
	c.SchemasPath = ""
	c.CreateHooks = nil // no $schema links in responses
	return c
}

// --- health and environments --------------------------------------------------------

// Health is the server status.
type Health struct {
	Status  string `json:"status" enum:"ok"`
	Version string `json:"version"`
}

type envPath struct {
	Env string `path:"env" doc:"Environment ID"`
}

func (s *Server) registerEnvironments(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getHealth", Method: http.MethodGet, Path: "/api/health", Tags: []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body Health }, error) {
		return &struct{ Body Health }{Health{Status: "ok", Version: version.Version}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listEnvironments", Method: http.MethodGet, Path: "/api/environments", Tags: []string{"environments"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []store.Environment }, error) {
		envs, err := s.Store.Environments(ctx)
		return &struct{ Body []store.Environment }{envs}, apiError(err)
	})

	type createEnv struct {
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"64"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "createEnvironment", Method: http.MethodPost, Path: "/api/environments", Tags: []string{"environments"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createEnv) (*struct{ Body store.Environment }, error) {
		env, err := s.Store.CreateEnvironment(ctx, in.Body.Name)
		return &struct{ Body store.Environment }{env}, apiError(err)
	})
}

// --- sandboxes ------------------------------------------------------------------------

type sandboxPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Sandbox ID"`
}

type sandboxOut struct{ Body sandboxes.View }

// PreviewPort is a listening guest port and the URL that reaches it.
type PreviewPort struct {
	Port int    `json:"port"`
	URL  string `json:"url"`
}

func (s *Server) registerSandboxes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listSandboxes", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes", Tags: []string{"sandboxes"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []sandboxes.View }, error) {
		list, err := s.Sandboxes.List(ctx, in.Env)
		return &struct{ Body []sandboxes.View }{list}, apiError(err)
	})

	type createIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body sandboxes.CreateRequest
	}
	huma.Register(api, huma.Operation{
		OperationID: "createSandbox", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes", Tags: []string{"sandboxes"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createIn) (*sandboxOut, error) {
		if _, err := s.Store.Environment(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		// Booting continues even if the client goes away; the manager rolls back on failure.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		v, err := s.Sandboxes.Create(ctx, in.Env, in.Body)
		return &sandboxOut{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getSandbox", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}", Tags: []string{"sandboxes"},
	}, func(ctx context.Context, in *sandboxPath) (*sandboxOut, error) {
		v, err := s.Sandboxes.Get(ctx, in.Env, in.ID)
		return &sandboxOut{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "startSandbox", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/start", Tags: []string{"sandboxes"},
	}, func(ctx context.Context, in *sandboxPath) (*sandboxOut, error) {
		v, err := s.Sandboxes.Start(context.WithoutCancel(ctx), in.Env, in.ID)
		return &sandboxOut{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "stopSandbox", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/stop", Tags: []string{"sandboxes"},
	}, func(ctx context.Context, in *sandboxPath) (*sandboxOut, error) {
		v, err := s.Sandboxes.Stop(context.WithoutCancel(ctx), in.Env, in.ID)
		return &sandboxOut{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "suspendSandbox", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/suspend", Tags: []string{"sandboxes"},
	}, func(ctx context.Context, in *sandboxPath) (*sandboxOut, error) {
		v, err := s.Sandboxes.Suspend(context.WithoutCancel(ctx), in.Env, in.ID)
		return &sandboxOut{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "resumeSandbox", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/resume", Tags: []string{"sandboxes"},
	}, func(ctx context.Context, in *sandboxPath) (*sandboxOut, error) {
		v, err := s.Sandboxes.Resume(context.WithoutCancel(ctx), in.Env, in.ID)
		return &sandboxOut{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteSandbox", Method: http.MethodDelete, Path: "/api/environments/{env}/sandboxes/{id}", Tags: []string{"sandboxes"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *sandboxPath) (*struct{}, error) {
		return nil, apiError(s.Sandboxes.Delete(context.WithoutCancel(ctx), in.Env, in.ID))
	})

	huma.Register(api, huma.Operation{
		OperationID: "listTerminals", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/terminals", Tags: []string{"terminals"},
		Description: "Terminals are tmux sessions; attach over a websocket at .../terminals/{name}/attach.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []agentproto.Session }, error) {
		list, err := s.Sandboxes.Terminals(ctx, in.Env, in.ID)
		return &struct{ Body []agentproto.Session }{list}, apiError(err)
	})

	type terminalPath struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Name string `path:"name" pattern:"^[A-Za-z0-9_-]{1,64}$"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "closeTerminal", Method: http.MethodDelete, Path: "/api/environments/{env}/sandboxes/{id}/terminals/{name}", Tags: []string{"terminals"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *terminalPath) (*struct{}, error) {
		return nil, apiError(s.Sandboxes.CloseTerminal(ctx, in.Env, in.ID, in.Name))
	})

	huma.Register(api, huma.Operation{
		OperationID: "listPorts", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/ports", Tags: []string{"previews"},
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []PreviewPort }, error) {
		ports, err := s.Sandboxes.Ports(ctx, in.Env, in.ID)
		out := make([]PreviewPort, 0, len(ports))
		for _, p := range ports {
			out = append(out, PreviewPort{Port: p.Port, URL: s.previewURL(in.ID, p.Port)})
		}
		return &struct{ Body []PreviewPort }{out}, apiError(err)
	})
}

func (s *Server) previewURL(id string, port int) string {
	_, studioPort, _ := net.SplitHostPort(s.Addr)
	return fmt.Sprintf("http://%d-%s.localhost:%s/", port, id, studioPort)
}

// apiError maps domain errors to HTTP problems; unknown errors keep their message, since
// the only client is the local user.
func apiError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, store.ErrExists):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, runtime.ErrCheckpointInUse), errors.Is(err, runtime.ErrCheckpointRestoreUnavailable),
		errors.Is(err, runtime.ErrLiveCheckpointUnsupported),
		errors.Is(err, store.ErrConflict),
		errors.Is(err, sandboxes.ErrBusy),
		errors.Is(err, sandboxes.ErrCheckpointState),
		errors.Is(err, sandboxes.ErrLifecycleState),
		errors.Is(err, sandboxes.ErrWorkspaceTooLarge):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, agentchan.ErrNotConnected):
		return huma.Error409Conflict("the sandbox is not running or still booting")
	case errors.Is(err, runtime.ErrInvalidName), errors.Is(err, sandboxes.ErrInvalidSpec),
		errors.Is(err, policy.ErrDoesNotCover), errors.Is(err, policy.ErrInvalidPattern),
		errors.Is(err, secrets.ErrInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, templatespec.ErrInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, sandboxes.ErrCheckpointName):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, policy.ErrDecided):
		return huma.Error409Conflict(err.Error())
	}
	return huma.Error500InternalServerError(err.Error())
}
