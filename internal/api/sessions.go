package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/personas"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// SessionGuest is the part of the guest-agent channel agent sessions use. It is the
// sandbox manager's Hub unless a test replaces it.
type SessionGuest interface {
	Sessions(ctx context.Context, id string) ([]agentproto.Session, error)
	KillSession(ctx context.Context, id, name string) error
	WriteHomeFiles(ctx context.Context, id string, files []agentproto.HomeFile) error
	StartSession(ctx context.Context, id string, req agentproto.StartSession) error
}

func (s *Server) guest() SessionGuest {
	if s.Guest != nil {
		return s.Guest
	}
	return s.Sandboxes.Hub
}

type sessionPath struct {
	Env  string `path:"env" doc:"Environment ID"`
	ID   string `path:"id" doc:"Sandbox ID"`
	Name string `path:"name" pattern:"^[A-Za-z0-9_-]{1,64}$"`
}

// registerSessions adds agent sessions: the owner persona's harness TUI in a tmux session
// of one of its sandboxes. They are terminals too, so the web terminal attaches to them.
func (s *Server) registerSessions(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "startSession", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/sessions", Tags: []string{"sessions"},
		DefaultStatus: http.StatusCreated,
		Description: "Starts the owner persona's harness in a new tmux session, after writing its config, " +
			"instructions and git identity into the agent user's home. Attach like any terminal.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body agentproto.Session }, error) {
		sess, err := s.startSession(ctx, in.Env, in.ID)
		return &struct{ Body agentproto.Session }{sess}, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "listSessions", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/sessions", Tags: []string{"sessions"},
		Description: "The agent sessions running in a sandbox: its terminals that run a harness.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []agentproto.Session }, error) {
		list, err := s.agentSessions(ctx, in.Env, in.ID)
		return &struct{ Body []agentproto.Session }{list}, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "stopSession", Method: http.MethodDelete, Path: "/api/environments/{env}/sandboxes/{id}/sessions/{name}", Tags: []string{"sessions"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Ends an agent session. The harness keeps its history in the agent user's home.",
	}, func(ctx context.Context, in *sessionPath) (*struct{}, error) {
		list, err := s.agentSessions(ctx, in.Env, in.ID)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(list, func(x agentproto.Session) bool { return x.Name == in.Name }) {
			return nil, huma.Error404NotFound("no agent session named " + in.Name)
		}
		return nil, apiError(s.guest().KillSession(ctx, in.ID, in.Name))
	})
}

// sessionSandbox returns a sandbox agent sessions can use; build VMs are never exposed.
func (s *Server) sessionSandbox(ctx context.Context, envID, id string) (store.Sandbox, error) {
	sb, err := s.Store.Sandbox(ctx, envID, id)
	if err == nil && sb.BuildJobID != "" {
		err = store.ErrNotFound
	}
	return sb, apiError(err)
}

func (s *Server) agentSessions(ctx context.Context, envID, id string) ([]agentproto.Session, error) {
	if _, err := s.sessionSandbox(ctx, envID, id); err != nil {
		return nil, err
	}
	all, err := s.guest().Sessions(ctx, id)
	if err != nil {
		return nil, apiError(err)
	}
	out := []agentproto.Session{}
	for _, x := range all {
		if x.Harness != "" {
			out = append(out, x)
		}
	}
	return out, nil
}

// sessionPlan is everything a session start sends to the guest.
type sessionPlan struct {
	harness harness.Harness
	files   []agentproto.HomeFile
	env     map[string]string
}

// planSession checks that sb's owner persona can run its harness and renders the files.
func (s *Server) planSession(ctx context.Context, sb store.Sandbox) (sessionPlan, error) {
	if sb.PersonaID == "" {
		return sessionPlan{}, huma.Error409Conflict("the sandbox has no owner persona; agent sessions run in a persona's sandbox")
	}
	p, err := s.Store.Persona(ctx, sb.EnvironmentID, sb.PersonaID)
	if err != nil {
		return sessionPlan{}, apiError(err)
	}
	prov, err := s.Store.Provider(ctx, sb.EnvironmentID, p.ProviderID)
	if err != nil {
		return sessionPlan{}, apiError(err)
	}
	view := providerView(prov)
	if view.State != personas.StateReady {
		return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("provider %s needs a login first", prov.Name))
	}
	h, ok := harness.Lookup(p.Harness)
	if !ok || !personas.Supports(prov.Kind, p.Harness) {
		return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("provider %s can't run %s", prov.Name, p.Harness))
	}
	// The key must reach the provider's host through the gateway, which swaps the
	// placeholder only on hosts the secret is bound to.
	host, err := providerHost(prov)
	if err != nil {
		return sessionPlan{}, apiError(err)
	}
	if prov.SecretID == "" || prov.Placeholder == "" || !slices.Contains(prov.Hosts, host) {
		return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("the key of provider %s is not bound to %s", prov.Name, host))
	}

	hp := harness.Provider{Kind: prov.Kind, BaseURL: prov.BaseURL, Model: prov.Model, EnvVar: view.EnvVar, Placeholder: prov.Placeholder}
	files, err := harness.Files(h,
		harness.Persona{Name: p.Name, Role: p.Role, Soul: p.Soul, Model: p.Model, GitName: p.GitName, GitEmail: p.GitEmail},
		harness.Sandbox{Name: sb.Name}, hp)
	if err != nil {
		return sessionPlan{}, huma.Error409Conflict(err.Error())
	}
	return sessionPlan{harness: h, files: files, env: harness.Env(hp)}, nil
}

// providerHost is the host a provider's key must be bound to.
func providerHost(prov store.Provider) (string, error) {
	if prov.Kind == personas.KindOpenAICompatible {
		_, host, err := personas.BaseURL(prov.BaseURL)
		return host, err
	}
	k, _ := personas.LookupKind(prov.Kind)
	return k.Host, nil
}

func (s *Server) startSession(ctx context.Context, envID, id string) (agentproto.Session, error) {
	sb, err := s.sessionSandbox(ctx, envID, id)
	if err != nil {
		return agentproto.Session{}, err
	}
	plan, err := s.planSession(ctx, sb)
	if err != nil {
		return agentproto.Session{}, err
	}
	running, err := s.guest().Sessions(ctx, id)
	if err != nil {
		return agentproto.Session{}, apiError(err)
	}
	name := plan.harness.Name()
	for n := 2; slices.ContainsFunc(running, func(x agentproto.Session) bool { return x.Name == name }); n++ {
		name = plan.harness.Name() + "-" + strconv.Itoa(n)
	}
	if err := s.guest().WriteHomeFiles(ctx, id, plan.files); err != nil {
		return agentproto.Session{}, apiError(fmt.Errorf("writing the harness config: %w", err))
	}
	req := agentproto.StartSession{
		Name:    name,
		Harness: plan.harness.Name(),
		Command: plan.harness.TUICommand(harness.SessionOpts{Workdir: "/workspace"}),
		Env:     plan.env,
	}
	if err := s.guest().StartSession(ctx, id, req); err != nil {
		return agentproto.Session{}, apiError(fmt.Errorf("starting %s: %w", name, err))
	}
	return agentproto.Session{Name: name, Harness: req.Harness, Windows: 1}, nil
}
