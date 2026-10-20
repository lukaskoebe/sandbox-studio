package api

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// roundTrip answers a request in place of the network.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestListProviderModels(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	path := testOrigin + "/api/environments/" + env.ID + "/providers/models"

	var asked []string
	status, answer := http.StatusOK, `{"object":"list","data":[{"id":"qwen3-coder"},{"id":"llama-3.3"},{"id":"bad model"},{"id":"qwen3-coder"}]}`
	s.ModelsClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		asked = append(asked, r.Method+" "+r.URL.String()+" "+r.Header.Get("Authorization"))
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(answer)), Header: http.Header{}}, nil
	})}

	rec := do(h, "POST", path, `{"baseUrl":"https://llm.example.com/v1/","apiKey":"sk-typed"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sk-typed") {
		t.Fatalf("typed key: %d %s", rec.Code, rec.Body)
	}
	if got := decode[ProviderModels](t, rec).Models; !slices.Equal(got, []string{"llama-3.3", "qwen3-coder"}) {
		t.Fatalf("models %v; want the valid ids, once each, sorted", got)
	}
	if !slices.Equal(asked, []string{"GET https://llm.example.com/v1/models Bearer sk-typed"}) {
		t.Fatalf("asked %v", asked)
	}

	// A saved provider lists with its stored key, but only at its own base URL.
	compat := createProvider(t, h, env, `{"name":"Local LLM","kind":"openai_compatible","apiKey":"sk-stored","baseUrl":"https://llm.example.com/v1","model":"qwen3-coder"}`)
	asked = nil
	rec = do(h, "POST", path, `{"baseUrl":"https://llm.example.com/v1","providerId":"`+compat.ID+`"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sk-stored") || !slices.Equal(asked, []string{"GET https://llm.example.com/v1/models Bearer sk-stored"}) {
		t.Fatalf("stored key: %d %s, asked %v", rec.Code, rec.Body, asked)
	}
	asked = nil
	if rec := do(h, "POST", path, `{"baseUrl":"https://evil.example.com/v1","providerId":"`+compat.ID+`"}`); rec.Code != http.StatusUnprocessableEntity || len(asked) > 0 {
		t.Fatalf("stored key to another host: %d %s, asked %v", rec.Code, rec.Body, asked)
	}
	anthropic := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	if rec := do(h, "POST", path, `{"baseUrl":"https://api.anthropic.com","providerId":"`+anthropic.ID+`"}`); rec.Code != http.StatusUnprocessableEntity || len(asked) > 0 {
		t.Fatalf("another kind's key: %d %s, asked %v", rec.Code, rec.Body, asked)
	}

	for _, body := range []string{
		`{"baseUrl":"https://llm.example.com/v1"}`,
		`{"baseUrl":"http://llm.example.com/v1","apiKey":"k"}`,
		`{"baseUrl":"https://127.0.0.1:8000","apiKey":"k"}`,
		`{"baseUrl":"https://llm.localhost","apiKey":"k"}`,
	} {
		if rec := do(h, "POST", path, body); rec.Code != http.StatusUnprocessableEntity || len(asked) > 0 {
			t.Fatalf("%s: %d %s, asked %v; want 422 without a request", body, rec.Code, rec.Body, asked)
		}
	}
	if rec := do(h, "POST", path, `{"baseUrl":"https://llm.example.com/v1","providerId":"nope"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown provider: %d %s", rec.Code, rec.Body)
	}

	// The endpoint's failures are a bad gateway, without its answer.
	status, answer = http.StatusUnauthorized, `{"error":"bad key sk-typed"}`
	if rec := do(h, "POST", path, `{"baseUrl":"https://llm.example.com/v1","apiKey":"sk-typed"}`); rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "sk-typed") {
		t.Fatalf("refused: %d %s", rec.Code, rec.Body)
	}
	status, answer = http.StatusOK, `<html>`
	if rec := do(h, "POST", path, `{"baseUrl":"https://llm.example.com/v1","apiKey":"k"}`); rec.Code != http.StatusBadGateway {
		t.Fatalf("not a list: %d %s", rec.Code, rec.Body)
	}
}
