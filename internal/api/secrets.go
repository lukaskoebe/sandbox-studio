package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/credentials"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type secretPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Secret ID"`
}

// registerSecrets adds the secret endpoints. Values go in through writeOnly fields and never
// come back out: responses are store.Secret, which has no value. A browser login's password,
// which the user typed in their teammate's browser, is the one exception (getSecretValue).
func (s *Server) registerSecrets(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listSecretKinds", Method: http.MethodGet, Path: "/api/secret-kinds", Tags: []string{"secrets"},
		Description: "The kinds a secret can have besides a plain value. Sandboxes get short-lived credentials made from a secret with a kind, through their tools' own helpers, instead of a placeholder.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []credentials.KindInfo }, error) {
		return &struct{ Body []credentials.KindInfo }{credentials.Kinds()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listSecrets", Method: http.MethodGet, Path: "/api/environments/{env}/secrets", Tags: []string{"secrets"},
		Description: "The secrets of an environment, of every scope. A sandbox sees the name of each secret of its environment, its teammate and its own as an environment variable holding the placeholder; of secrets sharing a name the most specific wins.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Secret }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Vault.List(ctx, in.Env)
		return &struct{ Body []store.Secret }{list}, apiError(err)
	})

	type createSecretIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Name       string   `json:"name" maxLength:"64" doc:"Environment variable name sandboxes see, e.g. OPENAI_API_KEY. Unique per scope."`
			Value      string   `json:"value" writeOnly:"true" doc:"The secret value. It is sealed on arrival and never returned."`
			Hosts      []string `json:"hosts" minItems:"1" doc:"Host patterns the value may be sent to, e.g. api.openai.com or *.example.com"`
			SandboxID  string   `json:"sandboxId,omitempty" doc:"Limits the secret to one sandbox of this environment"`
			TeammateID string   `json:"teammateId,omitempty" doc:"Limits the secret to the sandboxes one teammate of this environment owns; excludes sandboxId"`
			Note       string   `json:"note,omitempty" maxLength:"500"`
			// A browser login: only the teammate's browser uses it, never a sandbox.
			BrowserOnly bool   `json:"browserOnly,omitempty" doc:"Makes it a browser login of teammateId: Studio fills it into pages of its hosts and sends it to them as HTTP Basic credentials, and no sandbox sees it"`
			Username    string `json:"username,omitempty" maxLength:"200" doc:"A browser login's username"`
			AgentMayUse bool   `json:"agentMayUse,omitempty" doc:"Lets the teammate's agents use the browser login without asking"`
			// A secret with a kind: sandboxes get credentials made from it.
			Kind   string            `json:"kind,omitempty" maxLength:"32" doc:"A kind from listSecretKinds: sandboxes get short-lived credentials made from the secret instead of a placeholder. value is the kind's secret field."`
			Fields map[string]string `json:"fields,omitempty" doc:"The kind's other fields, by name"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "createSecret", Method: http.MethodPost, Path: "/api/environments/{env}/secrets", Tags: []string{"secrets"},
		DefaultStatus: http.StatusCreated,
		Description:   "The scope is fixed at creation.",
	}, func(ctx context.Context, in *createSecretIn) (*struct{ Body store.Secret }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		if err := s.checkScope(ctx, in.Env, in.Body.TeammateID, in.Body.SandboxID); err != nil {
			return nil, err
		}
		if in.Body.BrowserOnly && in.Body.TeammateID == "" {
			return nil, huma.Error422UnprocessableEntity("a browser login belongs to a teammate: set teammateId")
		}
		fields := map[string]string(nil)
		if in.Body.Kind != "" {
			var err error
			if fields, err = credentials.CheckSecret(in.Body.Kind, in.Body.Fields); err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
		} else if len(in.Body.Fields) > 0 {
			return nil, huma.Error422UnprocessableEntity("only a secret with a kind has fields")
		}
		sec, err := s.Vault.Create(ctx, store.Secret{EnvironmentID: in.Env, TeammateID: in.Body.TeammateID, SandboxID: in.Body.SandboxID,
			Name: in.Body.Name, Hosts: in.Body.Hosts, Note: in.Body.Note, BrowserOnly: in.Body.BrowserOnly, Username: in.Body.Username, AgentMayUse: in.Body.AgentMayUse,
			Kind: in.Body.Kind, Fields: fields}, in.Body.Value)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishSecret(sec)
		return &struct{ Body store.Secret }{sec}, nil
	})

	type updateSecretIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Secret ID"`
		Body struct {
			Value *string  `json:"value,omitempty" writeOnly:"true" doc:"A new value. Omit it to keep the stored one."`
			Hosts []string `json:"hosts" minItems:"1" doc:"Host patterns the value may be sent to"`
			Note  string   `json:"note,omitempty" maxLength:"500"`
			// Username is a browser login's; omitted, it stays.
			Username *string `json:"username,omitempty" maxLength:"200" doc:"A browser login's username; omit it to keep the stored one"`
			// AgentMayUse is a browser login's; omitted, it stays.
			AgentMayUse *bool `json:"agentMayUse,omitempty" doc:"Whether the teammate's agents use the browser login without asking; omit it to keep it"`
			// Fields are a secret with a kind's; omitted, they stay.
			Fields map[string]string `json:"fields,omitempty" doc:"A secret with a kind's other fields, all of them; omit it to keep them"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateSecret", Method: http.MethodPut, Path: "/api/environments/{env}/secrets/{id}", Tags: []string{"secrets"},
		Description: "The name and scope are fixed at creation. Omitting value keeps the stored value.",
	}, func(ctx context.Context, in *updateSecretIn) (*struct{ Body store.Secret }, error) {
		if err := s.checkUnmanaged(ctx, in.Env, in.ID); err != nil {
			return nil, err
		}
		var fields map[string]string
		if in.Body.Fields != nil {
			cur, err := s.Store.SecretByID(ctx, in.Env, in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			if cur.Kind == "" {
				return nil, huma.Error422UnprocessableEntity("only a secret with a kind has fields")
			}
			if fields, err = credentials.CheckSecret(cur.Kind, in.Body.Fields); err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
		}
		sec, err := s.Vault.Update(ctx, in.Env, in.ID, in.Body.Value, in.Body.Hosts, in.Body.Note, in.Body.Username)
		if err != nil {
			return nil, apiError(err)
		}
		if fields != nil {
			if sec, err = s.Vault.SetFields(ctx, in.Env, in.ID, fields); err != nil {
				return nil, apiError(err)
			}
		}
		if in.Body.AgentMayUse != nil && *in.Body.AgentMayUse != sec.AgentMayUse {
			if !sec.BrowserOnly {
				return nil, huma.Error422UnprocessableEntity("only a browser login is used by agents without asking")
			}
			if sec, err = s.Vault.SetAgentMayUse(ctx, in.Env, in.ID, *in.Body.AgentMayUse); err != nil {
				return nil, apiError(err)
			}
		}
		s.publishSecret(sec)
		return &struct{ Body store.Secret }{sec}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getSecretValue", Method: http.MethodGet, Path: "/api/environments/{env}/secrets/{id}/value", Tags: []string{"secrets"},
		Description: "A browser login's password, for the user who saved it in their teammate's browser. No other secret's value ever comes back out: 409.",
	}, func(ctx context.Context, in *secretPath) (*struct {
		Body struct {
			Value string `json:"value"`
		}
	}, error) {
		sec, err := s.Store.SecretByID(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if !sec.BrowserOnly {
			return nil, huma.Error409Conflict("only a browser login's password can be read back; other values never leave the vault")
		}
		value, err := s.Vault.Value(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		out := &struct {
			Body struct {
				Value string `json:"value"`
			}
		}{}
		out.Body.Value = value
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteSecret", Method: http.MethodDelete, Path: "/api/environments/{env}/secrets/{id}", Tags: []string{"secrets"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *secretPath) (*struct{}, error) {
		if err := s.checkUnmanaged(ctx, in.Env, in.ID); err != nil {
			return nil, err
		}
		sec, err := s.Store.SecretByID(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		if err := s.Vault.Delete(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		s.publishSecret(sec)
		return nil, nil
	})
}

// publishSecrets tells open tabs, and the sandboxes, that the environment's secrets changed.
func (s *Server) publishSecrets(envID string) {
	s.Bus.Publish(events.Event{Topic: events.TopicSecrets, EnvironmentID: envID})
}

// publishSecret announces a change of sec, for the sandboxes of its scope only.
func (s *Server) publishSecret(sec store.Secret) {
	s.Bus.Publish(events.Event{Topic: events.TopicSecrets, EnvironmentID: sec.EnvironmentID, TeammateID: sec.TeammateID, SandboxID: sec.SandboxID})
}

// checkScope answers 422 unless the teammate or sandbox a secret is scoped to belongs to
// environment env; at most one of them may be set.
func (s *Server) checkScope(ctx context.Context, env, teammateID, sandboxID string) error {
	if teammateID != "" && sandboxID != "" {
		return huma.Error422UnprocessableEntity("the scope is a sandbox or a teammate, not both")
	}
	if sandboxID != "" {
		_, err := s.publicSandbox(ctx, env, sandboxID)
		if errors.Is(err, store.ErrNotFound) {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("sandbox %s is not in this environment", sandboxID))
		}
		if err != nil {
			return apiError(err)
		}
	}
	return s.checkTeammate(ctx, env, teammateID)
}

// checkUnmanaged answers 409 if a provider owns the secret: its key is changed and removed
// through the provider.
func (s *Server) checkUnmanaged(ctx context.Context, envID, secretID string) error {
	provider, err := s.Store.SecretProvider(ctx, envID, secretID)
	if err != nil {
		return apiError(err)
	}
	if provider != "" {
		return huma.Error409Conflict(fmt.Sprintf("the secret is the key of provider %s; change it on the Providers page", provider))
	}
	forge, err := s.Store.SecretForge(ctx, envID, secretID)
	if err != nil {
		return apiError(err)
	}
	if forge != "" {
		return huma.Error409Conflict(fmt.Sprintf("the secret is the credential of forge %s; change it in Settings → Git forges", forge))
	}
	return nil
}

// ruleSecrets are the secrets rules may refer to: all but the studio-only ones, of every
// scope. Which one a reference means is decided per sandbox, when a request is sent.
func (s *Server) ruleSecrets(ctx context.Context, envID string) ([]store.Secret, error) {
	list, err := s.Store.Secrets(ctx, envID)
	return slices.DeleteFunc(list, func(sec store.Secret) bool { return sec.StudioOnly }), err
}
