package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/login"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
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

// registerSessions adds agent sessions: the owner teammate's harness TUI in a tmux session
// of one of its sandboxes. They are terminals too, so the web terminal attaches to them.
func (s *Server) registerSessions(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "startSession", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/sessions", Tags: []string{"sessions"},
		DefaultStatus: http.StatusCreated,
		Description: "Starts the owner teammate's harness in a new tmux session, after writing its config, " +
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
	if err == nil && !sandboxes.Public(sb) {
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
	harness  harness.Harness
	provider harness.Provider
	files    []agentproto.HomeFile
	env      map[string]string
}

// planSession checks that sb's owner teammate can run its harness and renders the files.
func (s *Server) planSession(ctx context.Context, sb store.Sandbox) (sessionPlan, error) {
	if sb.TeammateID == "" {
		return sessionPlan{}, huma.Error409Conflict("the sandbox has no owner teammate; agent sessions run in a teammate's sandbox")
	}
	p, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID)
	if err != nil {
		return sessionPlan{}, apiError(err)
	}
	if p.ProviderID == "" {
		return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("teammate %s has no provider; choose one in its settings first", p.Name))
	}
	prov, err := s.Store.Provider(ctx, sb.EnvironmentID, p.ProviderID)
	if err != nil {
		return sessionPlan{}, apiError(err)
	}
	view := s.providerViewOf(ctx, prov)
	switch view.State {
	case teammates.StateLoginRequired:
		return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("provider %s needs a sign-in first", prov.Name))
	case teammates.StateExpired:
		return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("the sign-in of provider %s has expired; sign in again", prov.Name))
	case teammates.StateKeyRequired:
		return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("provider %s needs its API key first", prov.Name))
	}
	h, ok := harness.Lookup(p.Harness)
	if !ok || !teammates.Supports(prov.Kind, p.Harness) {
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

	rewrites, err := s.gitRewrites(ctx, sb.EnvironmentID)
	if err != nil {
		return sessionPlan{}, apiError(err)
	}
	hp := harness.Provider{Kind: prov.Kind, BaseURL: prov.BaseURL, Model: prov.Model, EnvVar: view.EnvVar, Placeholder: prov.Placeholder}
	files, err := harness.Files(h,
		s.harnessTeammate(ctx, p),
		harness.Sandbox{Name: sb.Name, GitRewrites: rewrites, Others: s.otherSandboxes(ctx, sb)}, hp)
	if err != nil {
		return sessionPlan{}, huma.Error409Conflict(err.Error())
	}
	if prov.Kind == teammates.KindChatGPTSubscription {
		// The ChatGPT login's auth file: placeholders only, the login package keeps the rest.
		if s.Login == nil {
			return sessionPlan{}, huma.Error503ServiceUnavailable("subscription logins are not available")
		}
		auth, err := s.Login.GuestFiles(ctx, prov)
		if errors.Is(err, login.ErrNoLogin) {
			return sessionPlan{}, huma.Error409Conflict(fmt.Sprintf("provider %s needs a sign-in first", prov.Name))
		}
		if err != nil {
			return sessionPlan{}, apiError(err)
		}
		files = append(files, auth...)
	}
	if s.AgentMem != nil {
		copies, err := s.AgentMem.BrowseFiles(ctx, sb.EnvironmentID, sb.TeammateID)
		if err != nil {
			return sessionPlan{}, apiError(err)
		}
		files = append(files, copies...)
	}
	return sessionPlan{harness: h, provider: hp, files: files, env: harness.Env(hp)}, nil
}

// HarnessConfig is the owner teammate's harness setup for sb as a session gets it: its config
// files and its environment, placeholders only. The sandbox manager writes it whenever the
// guest connects, so the harness is set up in a plain terminal too (sandboxes.HarnessConfig).
func (s *Server) HarnessConfig(ctx context.Context, sb store.Sandbox) ([]agentproto.HomeFile, map[string]string, error) {
	plan, err := s.planSession(ctx, sb)
	if err != nil {
		return nil, nil, err
	}
	return plan.files, plan.env, nil
}

// providerHost is the host a provider's key must be bound to.
func providerHost(prov store.Provider) (string, error) {
	if prov.Kind == teammates.KindOpenAICompatible {
		_, host, err := teammates.BaseURL(prov.BaseURL)
		return host, err
	}
	k, _ := teammates.LookupKind(prov.Kind)
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
		var he agentchan.HarnessError
		if errors.As(err, &he) {
			return agentproto.Session{}, huma.Error409Conflict(he.Error())
		}
		return agentproto.Session{}, apiError(fmt.Errorf("starting %s: %w", name, err))
	}
	return agentproto.Session{Name: name, Harness: req.Harness, Windows: 1}, nil
}

// otherSandboxes are the names of the owner's other sandboxes, for the agent's instructions.
// A failed lookup leaves them out: the agent still learns that sandboxes are separate.
func (s *Server) otherSandboxes(ctx context.Context, sb store.Sandbox) []string {
	all, err := s.Store.Sandboxes(ctx, sb.EnvironmentID)
	if err != nil {
		return nil
	}
	var names []string
	for _, o := range all {
		if o.ID != sb.ID && o.Kind == "" && o.TeammateID != "" && o.TeammateID == sb.TeammateID {
			names = append(names, o.Name)
		}
	}
	slices.Sort(names)
	return names
}
