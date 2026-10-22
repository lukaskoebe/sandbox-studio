package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/hostlogins"
)

// The API's names for the host logins, so that the generated schemas say what they are.
type (
	// HostLogins says which CLIs have a login on the computer Studio runs on.
	HostLogins hostlogins.Found
	// HostCodexAccount is the account of the computer's Codex login.
	HostCodexAccount hostlogins.CodexAccount
	// HostOpencodeKey is one credential opencode stores; its key is never sent.
	HostOpencodeKey hostlogins.OpencodeKey
)

// hostLoginError answers ErrNotFound with a 404.
func hostLoginError(err error, what string) error {
	if errors.Is(err, hostlogins.ErrNotFound) {
		return huma.Error404NotFound(what + " has no login on this computer")
	}
	return huma.Error500InternalServerError("Could not read " + what + "'s login: " + err.Error())
}

func (s *Server) registerHostLogins(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getHostLogins", Method: http.MethodGet, Path: "/api/system/host-logins", Tags: []string{"system"},
		Description: "Which of Claude Code, Codex and opencode have a login on the computer Studio runs on. Only checks that their files exist; nothing is read.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body HostLogins }, error) {
		f := s.HostLogins
		if f == nil {
			return &struct{ Body HostLogins }{}, nil
		}
		return &struct{ Body HostLogins }{HostLogins(f.Find())}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getHostCodexAccount", Method: http.MethodGet, Path: "/api/system/host-logins/codex", Tags: []string{"system"},
		Description: "The ChatGPT account of the computer's Codex login, from its ID token's claims. Its tokens stay where they are: Studio signs in on its own.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body HostCodexAccount }, error) {
		f := s.HostLogins
		if f == nil {
			return nil, hostLoginError(hostlogins.ErrNotFound, "Codex")
		}
		a, err := f.CodexAccount()
		if err != nil {
			return nil, hostLoginError(err, "Codex")
		}
		return &struct{ Body HostCodexAccount }{HostCodexAccount(a)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listHostOpencodeKeys", Method: http.MethodGet, Path: "/api/system/host-logins/opencode", Tags: []string{"system"},
		Description: "The credentials opencode stores on this computer, without their content. Those with a kind are plain API keys Studio can import.",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []HostOpencodeKey }, error) {
		f := s.HostLogins
		if f == nil {
			return nil, hostLoginError(hostlogins.ErrNotFound, "opencode")
		}
		keys, err := f.OpencodeKeys()
		if err != nil {
			return nil, hostLoginError(err, "opencode")
		}
		out := make([]HostOpencodeKey, len(keys))
		for i, k := range keys {
			out[i] = HostOpencodeKey(k)
		}
		return &struct{ Body []HostOpencodeKey }{out}, nil
	})

	type importOpencodeIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Provider string `json:"provider" minLength:"1" maxLength:"64" doc:"opencode's name of the provider whose key to import"`
			Name     string `json:"name" minLength:"1" maxLength:"64" doc:"The new provider's name"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "importOpencodeKey", Method: http.MethodPost, Path: "/api/environments/{env}/providers/import-opencode", Tags: []string{"providers"},
		DefaultStatus: http.StatusCreated,
		Description:   "Creates a provider from a plain API key opencode stores on this computer. The key is sealed in the vault like a typed one; opencode keeps its own.",
	}, func(ctx context.Context, in *importOpencodeIn) (*struct{ Body ProviderView }, error) {
		f := s.HostLogins
		if f == nil {
			return nil, hostLoginError(hostlogins.ErrNotFound, "opencode")
		}
		k, err := f.OpencodeKey(in.Body.Provider)
		if errors.Is(err, hostlogins.ErrNotFound) {
			return nil, huma.Error422UnprocessableEntity("opencode has no API key of " + in.Body.Provider + " that Studio can import")
		}
		if err != nil {
			return nil, hostLoginError(err, "opencode")
		}
		v, err := s.addProvider(ctx, in.Env, newProvider{Name: in.Body.Name, Kind: k.Kind, APIKey: k.Key})
		if err != nil {
			return nil, err
		}
		return &struct{ Body ProviderView }{v}, nil
	})
}
