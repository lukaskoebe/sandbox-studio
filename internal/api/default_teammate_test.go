package api

import (
	"net/http"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

func listTeammates(t *testing.T, h http.Handler, envID string) []store.Teammate {
	t.Helper()
	rec := do(h, "GET", testOrigin+"/api/environments/"+envID+"/teammates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list teammates: %d %s", rec.Code, rec.Body)
	}
	return decode[[]store.Teammate](t, rec)
}

func addDefaultTeammate(t *testing.T, h http.Handler, envID, body string) store.Teammate {
	t.Helper()
	rec := do(h, "POST", testOrigin+"/api/environments/"+envID+"/teammates/default", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add default teammate: %d %s", rec.Code, rec.Body)
	}
	return decode[store.Teammate](t, rec)
}

func TestProvidersCreateNoTeammateAndSetupAddsTheDefault(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")

	// Without a provider there is nothing for the default to run on.
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/teammates/default", `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("default teammate without a provider: %d %s", rec.Code, rec.Body)
	}

	anthropic := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	if list := listTeammates(t, h, env.ID); len(list) != 0 {
		t.Fatalf("teammates after the first provider = %+v; want none", list)
	}

	iris := addDefaultTeammate(t, h, env.ID, `{}`)
	if iris.Name != teammates.DefaultTeammateName || iris.Role != teammates.DefaultTeammateRole || iris.Soul != teammates.DefaultTeammateSoul ||
		iris.Harness != "claude" || iris.ProviderID != anthropic.ID || iris.Model != "claude-sonnet-5-5" ||
		iris.GitName != "Iris (agent)" || iris.GitEmail != "iris@agents.invalid" {
		t.Fatalf("default teammate = %+v", iris)
	}
	// A second one clashes by name, as any teammate would.
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/teammates/default", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("second default teammate: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", testOrigin+"/api/environments/"+env.ID+"/teammates/"+iris.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete default teammate: %d %s", rec.Code, rec.Body)
	}

	// The setup's Change picks another provider, harness and model.
	openai := createProvider(t, h, env, `{"name":"OpenAI","kind":"openai_api","apiKey":"sk-test-openai"}`)
	iris = addDefaultTeammate(t, h, env.ID, `{"providerId":"`+openai.ID+`","harness":"opencode","model":"gpt-6-luna"}`)
	if iris.ProviderID != openai.ID || iris.Harness != "opencode" || iris.Model != "gpt-6-luna" {
		t.Fatalf("default teammate on a picked model = %+v", iris)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/teammates/default", `{"providerId":"nope"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("default teammate on another environment's provider: %d %s", rec.Code, rec.Body)
	}
}

func TestDefaultTeammateHarnessAndModelPerProviderKind(t *testing.T) {
	h, s := newTestServer(t)
	for _, tc := range []struct{ name, body, harness, model string }{
		{"OpenAI", `{"name":"OpenAI","kind":"openai_api","apiKey":"sk-test-openai"}`, "codex", "gpt-6.1-sol"},
		{"Endpoint", `{"name":"Endpoint","kind":"openai_compatible","apiKey":"sk-test-endpoint","baseUrl":"https://llm.example.com/v1","model":"llama-3.1"}`, "opencode", "llama-3.1"},
		{"Claude", `{"name":"Claude","kind":"claude_subscription"}`, "claude", ""},
		{"ChatGPT", `{"name":"ChatGPT","kind":"chatgpt_subscription"}`, "codex", ""},
	} {
		env := newEnvironment(t, s, "env-"+tc.name)
		provider := createProvider(t, h, env, tc.body)
		iris := addDefaultTeammate(t, h, env.ID, `{}`)
		if iris.Harness != tc.harness || iris.Model != tc.model || iris.ProviderID != provider.ID {
			t.Errorf("%s: default teammate = %+v; want one on %s with model %q", tc.name, iris, tc.harness, tc.model)
		}
	}
}
