package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// Limits of a model listing: the endpoint answers within modelsTimeout, in at most
// modelsMaxBody bytes, and at most modelsMax ids are kept.
const (
	modelsTimeout = 10 * time.Second
	modelsMaxBody = 4 << 20
	modelsMax     = 500
)

// ProviderModels are the models an OpenAI-compatible endpoint lists.
type ProviderModels struct {
	Models []string `json:"models" doc:"Model ids, sorted; ids that are not valid model names are left out"`
}

func (s *Server) registerProviderModels(api huma.API) {
	type listModelsIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			BaseURL    string `json:"baseUrl" maxLength:"2048" doc:"The endpoint's base URL: https:// on a public host, or http:// on this computer or the local network"`
			APIKey     string `json:"apiKey,omitempty" writeOnly:"true" doc:"The key to list with. It is only sent to the base URL and never stored or returned."`
			ProviderID string `json:"providerId,omitempty" doc:"A saved OpenAI-compatible provider whose stored key lists, when no key is given. Its base URL must be the one given."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "listProviderModels", Method: http.MethodPost, Path: "/api/environments/{env}/providers/models", Tags: []string{"providers"},
		Summary:     "The models an OpenAI-compatible endpoint lists",
		Description: "Studio requests GET <baseUrl>/models, with the key as a bearer token when there is one, and returns the ids. A public base URL is dialed on public addresses only. The forms suggest them for the model field.",
	}, func(ctx context.Context, in *listModelsIn) (*struct{ Body ProviderModels }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		e, err := teammates.ParseBaseURL(in.Body.BaseURL)
		if err != nil {
			return nil, apiError(err)
		}
		base := e.URL
		key := in.Body.APIKey
		if key == "" && in.Body.ProviderID != "" {
			p, err := s.Store.Provider(ctx, in.Env, in.Body.ProviderID)
			if err != nil {
				return nil, apiError(err)
			}
			// The stored key only goes where the provider sends it.
			if p.Kind != teammates.KindOpenAICompatible || p.BaseURL != base {
				return nil, huma.Error422UnprocessableEntity("the stored key only lists the models of the provider's own base URL; enter the key to try another")
			}
			if p.SecretID != "" {
				if key, err = s.Vault.Value(ctx, in.Env, p.SecretID); err != nil {
					return nil, apiError(err)
				}
			}
		}
		if key != "" && !e.TakesKey() {
			return nil, huma.Error422UnprocessableEntity(lanKeyRefused)
		}
		models, err := s.fetchModels(ctx, e, key)
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		return &struct{ Body ProviderModels }{ProviderModels{models}}, nil
	})
}

// lanKeyRefused refuses a key for a server on the local network.
const lanKeyRefused = "a server on the local network is reached over plain http://, which would carry its key across the network: leave the key empty, or serve it over https:// on a public name"

// fetchModels lists the models of the OpenAI-compatible endpoint e, with key unless it is
// empty. Its errors never carry the key or the endpoint's answer. Studio dials a public
// endpoint on public addresses only, and one on this computer or the local network where it is.
func (s *Server) fetchModels(ctx context.Context, e teammates.Endpoint, key string) ([]string, error) {
	base := e.URL
	client := s.ModelsClient
	if client == nil {
		dial := gateway.DialPublic
		if e.Where != teammates.WherePublic {
			dial = (&net.Dialer{Timeout: modelsTimeout}).DialContext
		}
		client = &http.Client{
			Transport:     &http.Transport{DialContext: dial, TLSHandshakeTimeout: modelsTimeout, DisableKeepAlives: true},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		// The URL is the user's own; what failed is the part worth showing.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("the endpoint did not answer: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the endpoint answered %s", res.Status)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, modelsMaxBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("the endpoint's answer is not an OpenAI model list")
	}
	out := []string{}
	for _, m := range body.Data {
		if teammates.CheckModel(m.ID) == nil && !slices.Contains(out, m.ID) {
			out = append(out, m.ID)
		}
		if len(out) == modelsMax {
			break
		}
	}
	slices.Sort(out)
	return out, nil
}
