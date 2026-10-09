package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type secretPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Secret ID"`
}

// registerSecrets adds the secret endpoints. Values go in through writeOnly fields and never
// come back out: responses are store.Secret, which has no value.
func (s *Server) registerSecrets(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listSecrets", Method: http.MethodGet, Path: "/api/environments/{env}/secrets", Tags: []string{"secrets"},
		Description: "The secrets of an environment. Sandboxes see each name as an environment variable holding its placeholder.",
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
			Name  string   `json:"name" maxLength:"64" doc:"Environment variable name sandboxes see, e.g. OPENAI_API_KEY"`
			Value string   `json:"value" writeOnly:"true" doc:"The secret value. It is sealed on arrival and never returned."`
			Hosts []string `json:"hosts" minItems:"1" doc:"Host patterns the value may be sent to, e.g. api.openai.com or *.example.com"`
			Note  string   `json:"note,omitempty" maxLength:"500"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "createSecret", Method: http.MethodPost, Path: "/api/environments/{env}/secrets", Tags: []string{"secrets"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createSecretIn) (*struct{ Body store.Secret }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		sec, err := s.Vault.Create(ctx, in.Env, in.Body.Name, in.Body.Value, in.Body.Hosts, in.Body.Note)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishSecrets(in.Env)
		return &struct{ Body store.Secret }{sec}, nil
	})

	type updateSecretIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Secret ID"`
		Body struct {
			Value *string  `json:"value,omitempty" writeOnly:"true" doc:"A new value. Omit it to keep the stored one."`
			Hosts []string `json:"hosts" minItems:"1" doc:"Host patterns the value may be sent to"`
			Note  string   `json:"note,omitempty" maxLength:"500"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateSecret", Method: http.MethodPut, Path: "/api/environments/{env}/secrets/{id}", Tags: []string{"secrets"},
		Description: "The name is fixed at creation. Omitting value keeps the stored value.",
	}, func(ctx context.Context, in *updateSecretIn) (*struct{ Body store.Secret }, error) {
		sec, err := s.Vault.Update(ctx, in.Env, in.ID, in.Body.Value, in.Body.Hosts, in.Body.Note)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishSecrets(in.Env)
		return &struct{ Body store.Secret }{sec}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteSecret", Method: http.MethodDelete, Path: "/api/environments/{env}/secrets/{id}", Tags: []string{"secrets"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *secretPath) (*struct{}, error) {
		if err := s.Vault.Delete(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		s.publishSecrets(in.Env)
		return nil, nil
	})
}

// publishSecrets tells open tabs that the environment's secrets changed.
func (s *Server) publishSecrets(envID string) {
	s.Bus.Publish(events.Event{Topic: events.TopicSecrets, EnvironmentID: envID})
}
