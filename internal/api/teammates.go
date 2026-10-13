package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// ProviderKind is one row of the fixed provider table (docs/teammates.md).
type ProviderKind struct {
	Kind      string   `json:"kind" enum:"anthropic_api,openai_api,openai_compatible,claude_subscription,chatgpt_subscription"`
	Harnesses []string `json:"harnesses" enum:"opencode,claude,codex" doc:"Harnesses that can use a provider of this kind"`
	APIKey    bool     `json:"apiKey" doc:"Whether the provider is an API key; otherwise it is a subscription login"`
	Host      string   `json:"host,omitempty" doc:"The host the key is bound to; for openai_compatible, the base URL's host"`
	EnvVar    string   `json:"envVar,omitempty" doc:"The variable the harness reads the credential from"`
}

// ProviderView is a provider with what its kind implies. It never carries the key.
type ProviderView struct {
	store.Provider
	State     string   `json:"state" enum:"ready,login_required,key_required,expired" doc:"login_required until a subscription login is stored; expired when a ChatGPT login lapsed and could not be refreshed; key_required until an API key is added"`
	EnvVar    string   `json:"envVar,omitempty" doc:"The variable guests get the placeholder in"`
	Harnesses []string `json:"harnesses" enum:"opencode,claude,codex" doc:"Harnesses teammates using this provider can run"`
}

func providerView(p store.Provider) ProviderView {
	k, _ := teammates.LookupKind(p.Kind)
	v := ProviderView{Provider: p, EnvVar: k.EnvVar, Harnesses: k.Harnesses, State: teammates.StateReady}
	switch {
	case k.Login && p.SecretID == "":
		// A subscription has a login once its sign-in stored what it yields (docs/teammates.md).
		v.State = teammates.StateLoginRequired
	case k.APIKey && p.SecretID == "":
		v.State = teammates.StateKeyRequired
	}
	return v
}

// providerViewOf is providerView with the sign-in's expiry: a ChatGPT login whose access token
// lapsed and could not be refreshed is expired.
func (s *Server) providerViewOf(ctx context.Context, p store.Provider) ProviderView {
	v := providerView(p)
	if v.State != teammates.StateReady || s.Login == nil {
		return v
	}
	if k, _ := teammates.LookupKind(p.Kind); !k.Login {
		return v
	}
	expired, err := s.Login.Expired(ctx, p)
	if err != nil {
		s.Log.Warn("checking a sign-in", "provider", p.ID, "err", err)
		return v
	}
	if expired {
		v.State = teammates.StateExpired
	}
	return v
}

type providerPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Provider ID"`
}

type teammatePath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Teammate ID"`
}

// TeammateInput is the editable part of a teammate.
type TeammateInput struct {
	Name          string `json:"name" minLength:"1" maxLength:"64"`
	Role          string `json:"role,omitempty" maxLength:"200" doc:"A short description of what the teammate does"`
	Avatar        string `json:"avatar,omitempty" maxLength:"64" doc:"One emoji, or shape:<name> for a generated avatar; omitted shows the initials"`
	ApprovalScope string `json:"approvalScope,omitempty" enum:"sandbox,teammate,environment" default:"environment" doc:"The scope the approval cards for this teammate's sandboxes preselect for new rules"`
	Soul          string `json:"soul,omitempty" maxLength:"16384" doc:"Personality and working rules, as markdown"`
	Harness       string `json:"harness" enum:"opencode,claude,codex"`
	ProviderID    string `json:"providerId" minLength:"1"`
	Model         string `json:"model,omitempty" maxLength:"128" doc:"Required for API-key providers; defaults to the model of an OpenAI-compatible provider"`
	GitName       string `json:"gitName,omitempty" maxLength:"100" doc:"Defaults to the name"`
	GitEmail      string `json:"gitEmail,omitempty" maxLength:"254" doc:"Defaults to <slug>@agents.invalid"`
}

func (s *Server) registerTeammates(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listProviderKinds", Method: http.MethodGet, Path: "/api/provider-kinds", Tags: []string{"providers"},
		Description: "The fixed table of provider kinds and the harnesses each supports.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []ProviderKind }, error) {
		out := []ProviderKind{}
		for _, kind := range []string{teammates.KindAnthropicAPI, teammates.KindOpenAIAPI, teammates.KindOpenAICompatible, teammates.KindClaudeSubscription, teammates.KindChatGPTSubscription} {
			k, _ := teammates.LookupKind(kind)
			out = append(out, ProviderKind{Kind: k.Kind, Harnesses: k.Harnesses, APIKey: k.APIKey, Host: k.Host, EnvVar: k.EnvVar})
		}
		return &struct{ Body []ProviderKind }{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listProviders", Method: http.MethodGet, Path: "/api/environments/{env}/providers", Tags: []string{"providers"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []ProviderView }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.Providers(ctx, in.Env)
		if err != nil {
			return nil, apiError(err)
		}
		out := make([]ProviderView, len(list))
		for i, p := range list {
			out[i] = s.providerViewOf(ctx, p)
		}
		return &struct{ Body []ProviderView }{out}, nil
	})

	type createProviderIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Name    string `json:"name" minLength:"1" maxLength:"64"`
			Kind    string `json:"kind" enum:"anthropic_api,openai_api,openai_compatible,claude_subscription,chatgpt_subscription"`
			APIKey  string `json:"apiKey,omitempty" writeOnly:"true" doc:"API-key kinds: the key. It is sealed in the vault on arrival and never returned."`
			BaseURL string `json:"baseUrl,omitempty" maxLength:"2048" doc:"openai_compatible only: the https:// base URL"`
			Model   string `json:"model,omitempty" maxLength:"128" doc:"openai_compatible only: the endpoint's single model"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "createProvider", Method: http.MethodPost, Path: "/api/environments/{env}/providers", Tags: []string{"providers"},
		DefaultStatus: http.StatusCreated,
		Description:   "API-key providers store their key as a vault secret bound to the vendor's API host, so guests only ever see a placeholder. A provider added to an environment without teammates also creates the default teammate.",
	}, func(ctx context.Context, in *createProviderIn) (*struct{ Body ProviderView }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		b := in.Body
		k, ok := teammates.LookupKind(b.Kind)
		if !ok {
			return nil, huma.Error422UnprocessableEntity("unknown provider kind")
		}
		if err := teammates.CheckName("provider", b.Name); err != nil {
			return nil, apiError(err)
		}
		p := store.Provider{ID: store.NewID(), EnvironmentID: in.Env, Name: b.Name, Kind: b.Kind}
		host := k.Host
		if b.Kind == teammates.KindOpenAICompatible {
			var err error
			if p.BaseURL, host, err = teammates.BaseURL(b.BaseURL); err != nil {
				return nil, apiError(err)
			}
			if b.Model == "" {
				return nil, huma.Error422UnprocessableEntity("OpenAI-compatible providers need a model")
			}
			if err := teammates.CheckModel(b.Model); err != nil {
				return nil, apiError(err)
			}
			p.Model = b.Model
		} else if b.BaseURL != "" || b.Model != "" {
			return nil, huma.Error422UnprocessableEntity("only OpenAI-compatible providers have a base URL and model")
		}
		var sec *store.Secret
		switch {
		case k.APIKey && b.APIKey == "":
			return nil, huma.Error422UnprocessableEntity("this provider needs an API key")
		case k.APIKey:
			prepared, err := s.Vault.Prepare(in.Env, teammates.SecretName(b.Name, p.ID), b.APIKey, []string{host}, "Key of provider "+b.Name)
			if err != nil {
				return nil, apiError(err)
			}
			sec = &prepared
		case b.APIKey != "":
			return nil, huma.Error422UnprocessableEntity("subscription providers log in instead of taking an API key")
		}
		created, err := s.Store.CreateProvider(ctx, p, sec)
		if err != nil {
			return nil, apiError(err)
		}
		if sec != nil {
			s.Vault.Changed(in.Env)
			s.publishSecrets(in.Env)
		}
		if err := s.createDefaultTeammate(ctx, in.Env, created); err != nil {
			s.Log.Warn("default teammate", "environment", in.Env, "err", err)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body ProviderView }{s.providerViewOf(ctx, created)}, nil
	})

	type updateProviderIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Provider ID"`
		Body struct {
			APIKey  *string `json:"apiKey,omitempty" writeOnly:"true" doc:"A new key. Omit it to keep the stored one."`
			BaseURL *string `json:"baseUrl,omitempty" maxLength:"2048" doc:"openai_compatible only"`
			Model   *string `json:"model,omitempty" maxLength:"128" doc:"openai_compatible only"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateProvider", Method: http.MethodPut, Path: "/api/environments/{env}/providers/{id}", Tags: []string{"providers"},
		Description: "The name and kind are fixed at creation. Omitted fields keep their values.",
	}, func(ctx context.Context, in *updateProviderIn) (*struct{ Body ProviderView }, error) {
		p, err := s.Store.Provider(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		b := in.Body
		var u store.ProviderUpdate
		if p.Kind == teammates.KindOpenAICompatible {
			if b.BaseURL != nil {
				baseURL, host, err := teammates.BaseURL(*b.BaseURL)
				if err != nil {
					return nil, apiError(err)
				}
				u.BaseURL, u.Hosts = &baseURL, []string{host}
			}
			if b.Model != nil {
				if err := teammates.CheckModel(*b.Model); err != nil {
					return nil, apiError(err)
				}
				u.Model = b.Model
			}
		} else if b.BaseURL != nil || b.Model != nil {
			return nil, huma.Error422UnprocessableEntity("only OpenAI-compatible providers have a base URL and model")
		}
		// A provider whose key was never added (an import) gets its first key here; the rest of
		// the update is applied before it, so the key is bound to the host the update names.
		addKey := false
		if b.APIKey != nil {
			k, _ := teammates.LookupKind(p.Kind)
			switch {
			case !k.APIKey:
				return nil, huma.Error422UnprocessableEntity("subscription providers log in instead of taking an API key")
			case p.SecretID == "":
				addKey = true
			default:
				if u.Sealed, err = s.Vault.SealValue(p.SecretID, *b.APIKey); err != nil {
					return nil, apiError(err)
				}
			}
		}
		updated, err := s.Store.UpdateProvider(ctx, in.Env, in.ID, u)
		if err != nil {
			return nil, apiError(err)
		}
		if addKey {
			host, err := providerHost(updated)
			if err != nil {
				return nil, apiError(err)
			}
			sec, err := s.Vault.Prepare(in.Env, teammates.SecretName(updated.Name, updated.ID), *b.APIKey, []string{host}, "Key of provider "+updated.Name)
			if err != nil {
				return nil, apiError(err)
			}
			if updated, err = s.Store.AttachProviderSecret(ctx, in.Env, in.ID, sec); err != nil {
				return nil, apiError(err)
			}
		}
		if u.Sealed != nil || u.Hosts != nil || addKey {
			s.Vault.Changed(in.Env)
			s.publishSecrets(in.Env)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body ProviderView }{s.providerViewOf(ctx, updated)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteProvider", Method: http.MethodDelete, Path: "/api/environments/{env}/providers/{id}", Tags: []string{"providers"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Deletes the provider and its key. Refused while a teammate uses the provider.",
	}, func(ctx context.Context, in *providerPath) (*struct{}, error) {
		p, err := s.Store.Provider(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if err := s.Store.DeleteProvider(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		if s.Login != nil {
			s.Login.Forget(ctx, p)
		}
		if p.SecretID != "" {
			s.Vault.Changed(in.Env)
			s.publishSecrets(in.Env)
		}
		s.publishTeammates(in.Env)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listTeammates", Method: http.MethodGet, Path: "/api/environments/{env}/teammates", Tags: []string{"teammates"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Teammate }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.Teammates(ctx, in.Env)
		return &struct{ Body []store.Teammate }{list}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getTeammate", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{id}", Tags: []string{"teammates"},
	}, func(ctx context.Context, in *teammatePath) (*struct{ Body store.Teammate }, error) {
		p, err := s.Store.Teammate(ctx, in.Env, in.ID)
		return &struct{ Body store.Teammate }{p}, apiError(err)
	})

	type createTeammateIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body TeammateInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createTeammate", Method: http.MethodPost, Path: "/api/environments/{env}/teammates", Tags: []string{"teammates"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createTeammateIn) (*struct{ Body store.Teammate }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		p, err := s.teammateFrom(ctx, in.Env, store.NewID(), in.Body)
		if err != nil {
			return nil, err
		}
		if p, err = s.Store.CreateTeammate(ctx, p); err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body store.Teammate }{p}, nil
	})

	type updateTeammateIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Teammate ID"`
		Body TeammateInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateTeammate", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{id}", Tags: []string{"teammates"},
	}, func(ctx context.Context, in *updateTeammateIn) (*struct{ Body store.Teammate }, error) {
		if _, err := s.Store.Teammate(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		p, err := s.teammateFrom(ctx, in.Env, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		if p, err = s.Store.UpdateTeammate(ctx, p); err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body store.Teammate }{p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteTeammate", Method: http.MethodDelete, Path: "/api/environments/{env}/teammates/{id}", Tags: []string{"teammates"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Deletes the teammate and the network rules scoped to it. Refused while it owns sandboxes.",
	}, func(ctx context.Context, in *teammatePath) (*struct{}, error) {
		if err := s.forgetBrowser(ctx, in.Env, in.ID); err != nil {
			return nil, err
		}
		if err := s.Store.DeleteTeammate(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		// Its rules are gone; held connections and cached rules must see that.
		return nil, apiError(s.Policy.Settle(ctx, in.Env))
	})

	type copyTeammateIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Teammate ID"`
		Body struct {
			TargetEnvironmentID string `json:"targetEnvironmentId" minLength:"1" doc:"The environment to copy the teammate to; not the one it is in"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "copyTeammate", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{id}/copy", Tags: []string{"teammates"},
		DefaultStatus: http.StatusCreated,
		Description: "Creates a copy of the teammate in another environment, with its name (a number is added if taken), role, avatar, soul, " +
			"harness, model, git identity and approval scope. The copy uses the target environment's provider of the same kind, preferring one with the same name; " +
			"without one it has no provider until one is chosen. Sandboxes, chats, memory, secrets and provider keys are not copied.",
	}, func(ctx context.Context, in *copyTeammateIn) (*struct{ Body store.Teammate }, error) {
		src, err := s.Store.Teammate(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		target := in.Body.TargetEnvironmentID
		if target == in.Env {
			return nil, huma.Error422UnprocessableEntity("the teammate is already in this environment")
		}
		if _, err := s.Store.Environment(ctx, target); errors.Is(err, store.ErrNotFound) {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("environment %s does not exist", target))
		} else if err != nil {
			return nil, apiError(err)
		}
		p := store.Teammate{
			ID: store.NewID(), EnvironmentID: target, Role: src.Role, Avatar: src.Avatar, ApprovalScope: src.ApprovalScope,
			Soul: src.Soul, Harness: src.Harness, Model: src.Model, GitName: src.GitName, GitEmail: src.GitEmail,
		}
		if p.ProviderID, err = s.matchingProvider(ctx, in.Env, target, src.ProviderID); err != nil {
			return nil, err
		}
		existing, err := s.Store.Teammates(ctx, target)
		if err != nil {
			return nil, apiError(err)
		}
		p.Name = uniqueName(src.Name, func(n string) bool {
			return slices.ContainsFunc(existing, func(t store.Teammate) bool { return t.Name == n })
		})
		created, err := s.Store.CreateTeammate(ctx, p)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(target)
		return &struct{ Body store.Teammate }{created}, nil
	})
}

// matchingProvider returns the provider of environment to that a teammate copied from
// environment from, with provider fromID, is given: one of the same kind, preferring one with
// the same name. It returns "" if there is none, and the teammate then has no provider.
func (s *Server) matchingProvider(ctx context.Context, from, to, fromID string) (string, error) {
	var kind, name string
	if fromID != "" {
		src, err := s.Store.Provider(ctx, from, fromID)
		if err != nil {
			return "", apiError(err)
		}
		kind, name = src.Kind, src.Name
	}
	if kind == "" {
		return "", nil
	}
	list, err := s.Store.Providers(ctx, to)
	if err != nil {
		return "", apiError(err)
	}
	match := ""
	for _, p := range list {
		if p.Kind != kind {
			continue
		}
		if p.Name == name {
			return p.ID, nil
		}
		if match == "" {
			match = p.ID
		}
	}
	return match, nil
}

// teammateFrom validates in and returns the teammate it describes, with defaults filled in.
func (s *Server) teammateFrom(ctx context.Context, env, id string, in TeammateInput) (store.Teammate, error) {
	p := store.Teammate{
		ID: id, EnvironmentID: env, Name: in.Name, Role: strings.TrimSpace(in.Role), Avatar: in.Avatar, Soul: in.Soul,
		Harness: in.Harness, ProviderID: in.ProviderID, Model: strings.TrimSpace(in.Model),
		GitName: strings.TrimSpace(in.GitName), GitEmail: strings.TrimSpace(in.GitEmail), ApprovalScope: in.ApprovalScope,
	}
	if p.ApprovalScope == "" {
		p.ApprovalScope = teammates.ScopeEnvironment
	}
	if p.GitName == "" {
		p.GitName = p.Name
	}
	if p.GitEmail == "" {
		p.GitEmail = teammates.DefaultGitEmail(p.Name, id)
	}
	for _, err := range []error{
		teammates.CheckName("teammate", p.Name), teammates.CheckRole(p.Role), teammates.CheckAvatar(p.Avatar),
		teammates.CheckSoul(p.Soul), teammates.CheckGitName(p.GitName), teammates.CheckGitEmail(p.GitEmail),
	} {
		if err != nil {
			return p, apiError(err)
		}
	}
	provider, err := s.Store.Provider(ctx, env, in.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return p, huma.Error422UnprocessableEntity(fmt.Sprintf("provider %s is not in this environment", in.ProviderID))
	}
	if err != nil {
		return p, apiError(err)
	}
	k, _ := teammates.LookupKind(provider.Kind)
	if !teammates.Supports(provider.Kind, p.Harness) {
		return p, huma.Error422UnprocessableEntity(fmt.Sprintf("provider %s can't run %s; it supports %s", provider.Name, p.Harness, strings.Join(k.Harnesses, " and ")))
	}
	if p.Model == "" && provider.Kind == teammates.KindOpenAICompatible {
		p.Model = provider.Model
	}
	if p.Model == "" && k.APIKey {
		return p, huma.Error422UnprocessableEntity("API-key providers need a model")
	}
	if p.Model != "" {
		if err := teammates.CheckModel(p.Model); err != nil {
			return p, apiError(err)
		}
	}
	return p, nil
}

// createDefaultTeammate creates the default teammate on a provider that was just added, if
// the environment has no teammates yet. Its fields go through the same checks as any
// teammate's. A failure leaves the provider in place; the teammate can be added by hand.
func (s *Server) createDefaultTeammate(ctx context.Context, envID string, p store.Provider) error {
	existing, err := s.Store.Teammates(ctx, envID)
	if err != nil || len(existing) > 0 {
		return err
	}
	harness, model := teammates.DefaultTeammateHarness(p.Kind)
	t, err := s.teammateFrom(ctx, envID, store.NewID(), TeammateInput{
		Name: teammates.DefaultTeammateName, Role: teammates.DefaultTeammateRole, Soul: teammates.DefaultTeammateSoul,
		Harness: harness, ProviderID: p.ID, Model: model,
		GitName: teammates.DefaultTeammateGitName, GitEmail: teammates.DefaultTeammateGitEmail,
	})
	if err != nil {
		return err
	}
	_, err = s.Store.CreateTeammate(ctx, t)
	return err
}

// checkTeammate answers 422 unless teammateID is empty or a teammate of the environment.
func (s *Server) checkTeammate(ctx context.Context, env, teammateID string) error {
	if teammateID == "" {
		return nil
	}
	_, err := s.Store.Teammate(ctx, env, teammateID)
	if errors.Is(err, store.ErrNotFound) {
		return huma.Error422UnprocessableEntity(fmt.Sprintf("teammate %s is not in this environment", teammateID))
	}
	return apiError(err)
}

// publishTeammates tells open tabs that the environment's teammates or providers changed.
func (s *Server) publishTeammates(envID string) {
	s.Bus.Publish(events.Event{Topic: events.TopicTeammates, EnvironmentID: envID})
}
