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

func TestFirstProviderCreatesTheDefaultTeammate(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")

	anthropic := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	list := listTeammates(t, h, env.ID)
	if len(list) != 1 {
		t.Fatalf("teammates after the first provider = %+v; want the default teammate", list)
	}
	iris := list[0]
	if iris.Name != teammates.DefaultTeammateName || iris.Role != teammates.DefaultTeammateRole || iris.Soul != teammates.DefaultTeammateSoul ||
		iris.Harness != "claude" || iris.ProviderID != anthropic.ID || iris.Model != "claude-sonnet-5-5" ||
		iris.GitName != "Iris (agent)" || iris.GitEmail != "iris@agents.invalid" {
		t.Fatalf("default teammate = %+v", iris)
	}

	// A provider added while the environment has a teammate creates none.
	createProvider(t, h, env, `{"name":"OpenAI","kind":"openai_api","apiKey":"sk-test-openai"}`)
	if list := listTeammates(t, h, env.ID); len(list) != 1 {
		t.Fatalf("teammates after a second provider = %+v; want only the default teammate", list)
	}

	// Once the environment has no teammates, the next provider brings the default back.
	if rec := do(h, "DELETE", testOrigin+"/api/environments/"+env.ID+"/teammates/"+iris.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete default teammate: %d %s", rec.Code, rec.Body)
	}
	chatgpt := createProvider(t, h, env, `{"name":"ChatGPT","kind":"chatgpt_subscription"}`)
	list = listTeammates(t, h, env.ID)
	if len(list) != 1 || list[0].Name != teammates.DefaultTeammateName || list[0].ProviderID != chatgpt.ID || list[0].Harness != "codex" {
		t.Fatalf("teammates after deleting the default = %+v; want Iris on the new provider", list)
	}
}

func TestDefaultTeammateHarnessAndModelPerProviderKind(t *testing.T) {
	h, s := newTestServer(t)
	for _, tc := range []struct{ name, body, harness, model string }{
		{"OpenAI", `{"name":"OpenAI","kind":"openai_api","apiKey":"sk-test-openai"}`, "codex", "gpt-5.1-codex"},
		{"Endpoint", `{"name":"Endpoint","kind":"openai_compatible","apiKey":"sk-test-endpoint","baseUrl":"https://llm.example.com/v1","model":"llama-3.1"}`, "opencode", "llama-3.1"},
		{"Claude", `{"name":"Claude","kind":"claude_subscription"}`, "claude", ""},
		{"ChatGPT", `{"name":"ChatGPT","kind":"chatgpt_subscription"}`, "codex", ""},
	} {
		env := newEnvironment(t, s, "env-"+tc.name)
		provider := createProvider(t, h, env, tc.body)
		list := listTeammates(t, h, env.ID)
		if len(list) != 1 || list[0].Harness != tc.harness || list[0].Model != tc.model || list[0].ProviderID != provider.ID {
			t.Errorf("%s: teammates = %+v; want one on %s with model %q", tc.name, list, tc.harness, tc.model)
		}
	}
}
