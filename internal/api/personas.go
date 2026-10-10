package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/personas"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ProviderKind is one row of the fixed provider table (PLAN.md §6.6).
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
	State     string   `json:"state" enum:"ready,login_required" doc:"login_required until a subscription login is stored"`
	EnvVar    string   `json:"envVar,omitempty" doc:"The variable guests get the placeholder in"`
	Harnesses []string `json:"harnesses" enum:"opencode,claude,codex" doc:"Harnesses personas using this provider can run"`
}

func providerView(p store.Provider) ProviderView {
	k, _ := personas.LookupKind(p.Kind)
	v := ProviderView{Provider: p, EnvVar: k.EnvVar, Harnesses: k.Harnesses, State: personas.StateReady}
	if !k.APIKey {
		// TODO(M4): run `claude setup-token` / `codex login` in a system sandbox and store the
		// result as the provider's secret; until then subscription providers can't log in.
		v.State = personas.StateLoginRequired
	}
	return v
}

type providerPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Provider ID"`
}

type personaPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Persona ID"`
}

// PersonaInput is the editable part of a persona.
type PersonaInput struct {
	Name       string `json:"name" minLength:"1" maxLength:"64"`
	Role       string `json:"role,omitempty" maxLength:"200" doc:"A short description of what the persona does"`
	Soul       string `json:"soul,omitempty" maxLength:"16384" doc:"Personality and working rules, as markdown"`
	Harness    string `json:"harness" enum:"opencode,claude,codex"`
	ProviderID string `json:"providerId" minLength:"1"`
	Model      string `json:"model,omitempty" maxLength:"128" doc:"Required for API-key providers; defaults to the model of an OpenAI-compatible provider"`
	GitName    string `json:"gitName,omitempty" maxLength:"100" doc:"Defaults to the name"`
	GitEmail   string `json:"gitEmail,omitempty" maxLength:"254" doc:"Defaults to <slug>@agents.invalid"`
}

func (s *Server) registerPersonas(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listProviderKinds", Method: http.MethodGet, Path: "/api/provider-kinds", Tags: []string{"providers"},
		Description: "The fixed table of provider kinds and the harnesses each supports.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []ProviderKind }, error) {
		out := []ProviderKind{}
		for _, kind := range []string{personas.KindAnthropicAPI, personas.KindOpenAIAPI, personas.KindOpenAICompatible, personas.KindClaudeSubscription, personas.KindChatGPTSubscription} {
			k, _ := personas.LookupKind(kind)
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
			out[i] = providerView(p)
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
		Description:   "API-key providers store their key as a vault secret bound to the vendor's API host, so guests only ever see a placeholder.",
	}, func(ctx context.Context, in *createProviderIn) (*struct{ Body ProviderView }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		b := in.Body
		k, ok := personas.LookupKind(b.Kind)
		if !ok {
			return nil, huma.Error422UnprocessableEntity("unknown provider kind")
		}
		if err := personas.CheckName("provider", b.Name); err != nil {
			return nil, apiError(err)
		}
		p := store.Provider{ID: store.NewID(), EnvironmentID: in.Env, Name: b.Name, Kind: b.Kind}
		host := k.Host
		if b.Kind == personas.KindOpenAICompatible {
			var err error
			if p.BaseURL, host, err = personas.BaseURL(b.BaseURL); err != nil {
				return nil, apiError(err)
			}
			if b.Model == "" {
				return nil, huma.Error422UnprocessableEntity("OpenAI-compatible providers need a model")
			}
			if err := personas.CheckModel(b.Model); err != nil {
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
			prepared, err := s.Vault.Prepare(in.Env, personas.SecretName(b.Name, p.ID), b.APIKey, []string{host}, "Key of provider "+b.Name)
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
		s.publishPersonas(in.Env)
		return &struct{ Body ProviderView }{providerView(created)}, nil
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
		if p.Kind == personas.KindOpenAICompatible {
			if b.BaseURL != nil {
				baseURL, host, err := personas.BaseURL(*b.BaseURL)
				if err != nil {
					return nil, apiError(err)
				}
				u.BaseURL, u.Hosts = &baseURL, []string{host}
			}
			if b.Model != nil {
				if err := personas.CheckModel(*b.Model); err != nil {
					return nil, apiError(err)
				}
				u.Model = b.Model
			}
		} else if b.BaseURL != nil || b.Model != nil {
			return nil, huma.Error422UnprocessableEntity("only OpenAI-compatible providers have a base URL and model")
		}
		if b.APIKey != nil {
			if p.SecretID == "" {
				return nil, huma.Error422UnprocessableEntity("subscription providers log in instead of taking an API key")
			}
			if u.Sealed, err = s.Vault.SealValue(p.SecretID, *b.APIKey); err != nil {
				return nil, apiError(err)
			}
		}
		updated, err := s.Store.UpdateProvider(ctx, in.Env, in.ID, u)
		if err != nil {
			return nil, apiError(err)
		}
		if u.Sealed != nil || u.Hosts != nil {
			s.Vault.Changed(in.Env)
			s.publishSecrets(in.Env)
		}
		s.publishPersonas(in.Env)
		return &struct{ Body ProviderView }{providerView(updated)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteProvider", Method: http.MethodDelete, Path: "/api/environments/{env}/providers/{id}", Tags: []string{"providers"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Deletes the provider and its key. Refused while a persona uses the provider.",
	}, func(ctx context.Context, in *providerPath) (*struct{}, error) {
		p, err := s.Store.Provider(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if err := s.Store.DeleteProvider(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		if p.SecretID != "" {
			s.Vault.Changed(in.Env)
			s.publishSecrets(in.Env)
		}
		s.publishPersonas(in.Env)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listPersonas", Method: http.MethodGet, Path: "/api/environments/{env}/personas", Tags: []string{"personas"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Persona }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.Personas(ctx, in.Env)
		return &struct{ Body []store.Persona }{list}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getPersona", Method: http.MethodGet, Path: "/api/environments/{env}/personas/{id}", Tags: []string{"personas"},
	}, func(ctx context.Context, in *personaPath) (*struct{ Body store.Persona }, error) {
		p, err := s.Store.Persona(ctx, in.Env, in.ID)
		return &struct{ Body store.Persona }{p}, apiError(err)
	})

	type createPersonaIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body PersonaInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createPersona", Method: http.MethodPost, Path: "/api/environments/{env}/personas", Tags: []string{"personas"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createPersonaIn) (*struct{ Body store.Persona }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		p, err := s.personaFrom(ctx, in.Env, store.NewID(), in.Body)
		if err != nil {
			return nil, err
		}
		if p, err = s.Store.CreatePersona(ctx, p); err != nil {
			return nil, apiError(err)
		}
		s.publishPersonas(in.Env)
		return &struct{ Body store.Persona }{p}, nil
	})

	type updatePersonaIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Persona ID"`
		Body PersonaInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "updatePersona", Method: http.MethodPut, Path: "/api/environments/{env}/personas/{id}", Tags: []string{"personas"},
	}, func(ctx context.Context, in *updatePersonaIn) (*struct{ Body store.Persona }, error) {
		if _, err := s.Store.Persona(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		p, err := s.personaFrom(ctx, in.Env, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		if p, err = s.Store.UpdatePersona(ctx, p); err != nil {
			return nil, apiError(err)
		}
		s.publishPersonas(in.Env)
		return &struct{ Body store.Persona }{p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "deletePersona", Method: http.MethodDelete, Path: "/api/environments/{env}/personas/{id}", Tags: []string{"personas"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Deletes the persona and the network rules scoped to it. Refused while it owns sandboxes.",
	}, func(ctx context.Context, in *personaPath) (*struct{}, error) {
		if err := s.Store.DeletePersona(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		s.publishPersonas(in.Env)
		// Its rules are gone; held connections and cached rules must see that.
		return nil, apiError(s.Policy.Settle(ctx, in.Env))
	})
}

// personaFrom validates in and returns the persona it describes, with defaults filled in.
func (s *Server) personaFrom(ctx context.Context, env, id string, in PersonaInput) (store.Persona, error) {
	p := store.Persona{
		ID: id, EnvironmentID: env, Name: in.Name, Role: strings.TrimSpace(in.Role), Soul: in.Soul,
		Harness: in.Harness, ProviderID: in.ProviderID, Model: strings.TrimSpace(in.Model),
		GitName: strings.TrimSpace(in.GitName), GitEmail: strings.TrimSpace(in.GitEmail),
	}
	if p.GitName == "" {
		p.GitName = p.Name
	}
	if p.GitEmail == "" {
		p.GitEmail = personas.DefaultGitEmail(p.Name, id)
	}
	for _, err := range []error{
		personas.CheckName("persona", p.Name), personas.CheckRole(p.Role), personas.CheckSoul(p.Soul),
		personas.CheckGitName(p.GitName), personas.CheckGitEmail(p.GitEmail),
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
	k, _ := personas.LookupKind(provider.Kind)
	if !personas.Supports(provider.Kind, p.Harness) {
		return p, huma.Error422UnprocessableEntity(fmt.Sprintf("provider %s can't run %s; it supports %s", provider.Name, p.Harness, strings.Join(k.Harnesses, " and ")))
	}
	if p.Model == "" && provider.Kind == personas.KindOpenAICompatible {
		p.Model = provider.Model
	}
	if p.Model == "" && k.APIKey {
		return p, huma.Error422UnprocessableEntity("API-key providers need a model")
	}
	if p.Model != "" {
		if err := personas.CheckModel(p.Model); err != nil {
			return p, apiError(err)
		}
	}
	return p, nil
}

// checkPersona answers 422 unless personaID is empty or a persona of the environment.
func (s *Server) checkPersona(ctx context.Context, env, personaID string) error {
	if personaID == "" {
		return nil
	}
	_, err := s.Store.Persona(ctx, env, personaID)
	if errors.Is(err, store.ErrNotFound) {
		return huma.Error422UnprocessableEntity(fmt.Sprintf("persona %s is not in this environment", personaID))
	}
	return apiError(err)
}

// publishPersonas tells open tabs that the environment's personas or providers changed.
func (s *Server) publishPersonas(envID string) {
	s.Bus.Publish(events.Event{Topic: events.TopicPersonas, EnvironmentID: envID})
}
