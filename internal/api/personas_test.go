package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const providerKey = "sk-ant-api03-4be1c9d07a"

func createProvider(t *testing.T, h http.Handler, env store.Environment, body string) ProviderView {
	t.Helper()
	rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/providers", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create provider %s: %d %s", body, rec.Code, rec.Body)
	}
	return decode[ProviderView](t, rec)
}

func TestProvidersCRUD(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/providers"
	ch, unsubscribe := s.Bus.Subscribe()
	defer unsubscribe()

	rec := do(h, "POST", base, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), providerKey) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	p := decode[ProviderView](t, rec)
	if p.State != "ready" || p.EnvVar != "ANTHROPIC_API_KEY" || p.SecretName != "PROVIDER_ANTHROPIC_KEY" ||
		!strings.HasPrefix(p.Placeholder, "studio-") || !slices.Equal(p.Hosts, []string{"api.anthropic.com"}) ||
		!slices.Equal(p.Harnesses, []string{"claude", "opencode"}) {
		t.Fatalf("created: %+v", p)
	}
	expectEvent(t, ch, events.Event{Topic: events.TopicSecrets, EnvironmentID: env.ID})
	expectEvent(t, ch, events.Event{Topic: events.TopicPersonas, EnvironmentID: env.ID})
	if got := boundValue(t, s, env.ID); got != providerKey {
		t.Fatalf("bound key %q", got)
	}

	// The key never comes back out, not even through the secrets API.
	for _, url := range []string{base, base + "/../secrets", testOrigin + "/api/environments/" + env.ID + "/secrets"} {
		if rec := do(h, "GET", url, ""); strings.Contains(rec.Body.String(), providerKey) {
			t.Fatalf("%s carries the key: %s", url, rec.Body)
		}
	}
	// Its secret is managed by the provider.
	secretURL := testOrigin + "/api/environments/" + env.ID + "/secrets/" + p.SecretID
	if rec := do(h, "PUT", secretURL, `{"value":"x","hosts":["evil.example.com"]}`); rec.Code != http.StatusConflict {
		t.Fatalf("update provider secret directly: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", secretURL, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete provider secret directly: %d %s", rec.Code, rec.Body)
	}

	// Rotating the key keeps the placeholder.
	rec = do(h, "PUT", base+"/"+p.ID, `{"apiKey":"sk-rotated-key"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sk-rotated-key") {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	if got := decode[ProviderView](t, rec); got.Placeholder != p.Placeholder {
		t.Fatalf("rotation changed the placeholder: %+v", got)
	}
	if got := boundValue(t, s, env.ID); got != "sk-rotated-key" {
		t.Fatalf("rotated key %q", got)
	}

	compat := createProvider(t, h, env, `{"name":"Local LLM","kind":"openai_compatible","apiKey":"k","baseUrl":"https://llm.example.com/v1/","model":"qwen3-coder"}`)
	if compat.BaseURL != "https://llm.example.com/v1" || compat.Model != "qwen3-coder" || compat.EnvVar != "OPENAI_API_KEY" ||
		!slices.Equal(compat.Hosts, []string{"llm.example.com"}) {
		t.Fatalf("compatible: %+v", compat)
	}
	rec = do(h, "PUT", base+"/"+compat.ID, `{"baseUrl":"https://other.example.net/v1"}`)
	if got := decode[ProviderView](t, rec); rec.Code != http.StatusOK || !slices.Equal(got.Hosts, []string{"other.example.net"}) || got.BaseURL != "https://other.example.net/v1" {
		t.Fatalf("move compatible: %d %+v", rec.Code, got)
	}

	sub := createProvider(t, h, env, `{"name":"Claude Max","kind":"claude_subscription"}`)
	if sub.State != "login_required" || sub.SecretID != "" || sub.Placeholder != "" {
		t.Fatalf("subscription: %+v", sub)
	}

	list := decode[[]ProviderView](t, do(h, "GET", base, ""))
	if len(list) != 3 {
		t.Fatalf("list: %+v", list)
	}

	if rec := do(h, "DELETE", base+"/"+p.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := s.Store.SecretByID(context.Background(), env.ID, p.SecretID); err == nil {
		t.Fatal("the provider's secret survived it")
	}
	if rec := do(h, "DELETE", base+"/"+p.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

func TestProviderValidation(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	base := testOrigin + "/api/environments/" + env.ID + "/providers"
	if _, err := s.Vault.Create(context.Background(), env.ID, "PROVIDER_TAKEN_KEY", "v", []string{"a.com"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"unknown kind", `{"name":"x","kind":"gemini_api","apiKey":"k"}`, http.StatusUnprocessableEntity},
		{"missing key", `{"name":"x","kind":"openai_api"}`, http.StatusUnprocessableEntity},
		{"subscription with key", `{"name":"x","kind":"chatgpt_subscription","apiKey":"k"}`, http.StatusUnprocessableEntity},
		{"model on fixed kind", `{"name":"x","kind":"openai_api","apiKey":"k","model":"gpt-5"}`, http.StatusUnprocessableEntity},
		{"compatible without model", `{"name":"x","kind":"openai_compatible","apiKey":"k","baseUrl":"https://llm.example.com"}`, http.StatusUnprocessableEntity},
		{"compatible over http", `{"name":"x","kind":"openai_compatible","apiKey":"k","baseUrl":"http://llm.example.com","model":"m"}`, http.StatusUnprocessableEntity},
		{"compatible on loopback", `{"name":"x","kind":"openai_compatible","apiKey":"k","baseUrl":"https://127.0.0.1:8000","model":"m"}`, http.StatusUnprocessableEntity},
		{"compatible on private ip", `{"name":"x","kind":"openai_compatible","apiKey":"k","baseUrl":"https://192.168.1.10","model":"m"}`, http.StatusUnprocessableEntity},
		{"compatible on localhost", `{"name":"x","kind":"openai_compatible","apiKey":"k","baseUrl":"https://llm.localhost","model":"m"}`, http.StatusUnprocessableEntity},
		{"compatible with credentials", `{"name":"x","kind":"openai_compatible","apiKey":"k","baseUrl":"https://u:p@llm.example.com","model":"m"}`, http.StatusUnprocessableEntity},
		{"bad model", `{"name":"x","kind":"openai_compatible","apiKey":"k","baseUrl":"https://llm.example.com","model":"a b"}`, http.StatusUnprocessableEntity},
		{"secret name taken", `{"name":"taken","kind":"openai_api","apiKey":"k"}`, http.StatusConflict},
	} {
		if rec := do(h, "POST", base, tc.body); rec.Code != tc.want || strings.Contains(rec.Body.String(), `"k"`) {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
	createProvider(t, h, env, `{"name":"dup","kind":"claude_subscription"}`)
	if rec := do(h, "POST", base, `{"name":"dup","kind":"chatgpt_subscription"}`); rec.Code != http.StatusConflict {
		t.Errorf("duplicate name: %d %s", rec.Code, rec.Body)
	}
	p := createProvider(t, h, env, `{"name":"openai","kind":"openai_api","apiKey":"k"}`)
	if rec := do(h, "GET", testOrigin+"/api/environments/"+other.ID+"/providers", ""); len(decode[[]ProviderView](t, rec)) != 0 {
		t.Errorf("providers leak across environments: %s", rec.Body)
	}
	if rec := do(h, "PUT", testOrigin+"/api/environments/"+other.ID+"/providers/"+p.ID, `{"apiKey":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("cross-environment update: %d", rec.Code)
	}
	if rec := do(h, "PUT", base+"/"+p.ID, `{"baseUrl":"https://llm.example.com"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("base URL on a fixed kind: %d", rec.Code)
	}
	if rec := do(h, "GET", testOrigin+"/api/environments/missing/providers", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing environment: %d", rec.Code)
	}
}

func TestPersonasCRUD(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	base := testOrigin + "/api/environments/" + env.ID + "/personas"
	anthropic := createProvider(t, h, env, `{"name":"anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	compat := createProvider(t, h, env, `{"name":"local","kind":"openai_compatible","apiKey":"k","baseUrl":"https://llm.example.com/v1","model":"qwen3-coder"}`)
	foreign := createProvider(t, h, other, `{"name":"anthropic","kind":"anthropic_api","apiKey":"k"}`)

	rec := do(h, "POST", base, `{"name":"Ada Lovelace","role":"Reviewer","soul":"# Ada\nCareful.","harness":"claude","providerId":"`+anthropic.ID+`","model":"claude-sonnet-4-5"}`)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), providerKey) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	ada := decode[store.Persona](t, rec)
	if ada.GitName != "Ada Lovelace" || ada.GitEmail != "ada-lovelace@agents.invalid" || ada.Soul != "# Ada\nCareful." {
		t.Fatalf("defaults: %+v", ada)
	}
	rec = do(h, "POST", base, `{"name":"Bob","harness":"opencode","providerId":"`+compat.ID+`","gitEmail":"bob@example.com"}`)
	bob := decode[store.Persona](t, rec)
	if rec.Code != http.StatusCreated || bob.Model != "qwen3-coder" || bob.GitEmail != "bob@example.com" {
		t.Fatalf("compatible model default: %d %+v", rec.Code, bob)
	}

	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"duplicate name", `{"name":"Bob","harness":"claude","providerId":"` + anthropic.ID + `","model":"m"}`, http.StatusConflict},
		{"unsupported harness", `{"name":"x","harness":"codex","providerId":"` + anthropic.ID + `","model":"m"}`, http.StatusUnprocessableEntity},
		{"missing model", `{"name":"x","harness":"claude","providerId":"` + anthropic.ID + `"}`, http.StatusUnprocessableEntity},
		{"foreign provider", `{"name":"x","harness":"claude","providerId":"` + foreign.ID + `","model":"m"}`, http.StatusUnprocessableEntity},
		{"git name newline", `{"name":"x","harness":"claude","providerId":"` + anthropic.ID + `","model":"m","gitName":"a\nb"}`, http.StatusUnprocessableEntity},
		{"git email injection", `{"name":"x","harness":"claude","providerId":"` + anthropic.ID + `","model":"m","gitEmail":"a@b.com\n[core]"}`, http.StatusUnprocessableEntity},
		{"git email shape", `{"name":"x","harness":"claude","providerId":"` + anthropic.ID + `","model":"m","gitEmail":"nobody"}`, http.StatusUnprocessableEntity},
		{"name with control", `{"name":"x\u0007","harness":"claude","providerId":"` + anthropic.ID + `","model":"m"}`, http.StatusUnprocessableEntity},
		{"soul too large", `{"name":"x","harness":"claude","providerId":"` + anthropic.ID + `","model":"m","soul":"` + strings.Repeat("a", 16385) + `"}`, http.StatusUnprocessableEntity},
		{"bad harness", `{"name":"x","harness":"aider","providerId":"` + anthropic.ID + `","model":"m"}`, http.StatusUnprocessableEntity},
	} {
		if rec := do(h, "POST", base, tc.body); rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}

	rec = do(h, "PUT", base+"/"+ada.ID, `{"name":"Ada","role":"Author","harness":"opencode","providerId":"`+anthropic.ID+`","model":"claude-opus-4-1","gitName":"Ada L.","gitEmail":"ada@example.com"}`)
	if got := decode[store.Persona](t, rec); rec.Code != http.StatusOK || got.Role != "Author" || got.Harness != "opencode" || got.GitName != "Ada L." {
		t.Fatalf("update: %d %+v", rec.Code, got)
	}
	if rec := do(h, "PUT", testOrigin+"/api/environments/"+other.ID+"/personas/"+ada.ID, `{"name":"Ada","harness":"claude","providerId":"`+foreign.ID+`","model":"m"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-environment update: %d %s", rec.Code, rec.Body)
	}
	if got := decode[[]store.Persona](t, do(h, "GET", base, "")); len(got) != 2 {
		t.Fatalf("list: %+v", got)
	}
	if got := decode[[]store.Persona](t, do(h, "GET", testOrigin+"/api/environments/"+other.ID+"/personas", "")); len(got) != 0 {
		t.Fatalf("personas leak across environments: %+v", got)
	}

	// A provider in use and a persona owning sandboxes can't be deleted.
	if rec := do(h, "DELETE", testOrigin+"/api/environments/"+env.ID+"/providers/"+anthropic.ID, ""); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "Ada") {
		t.Fatalf("delete provider in use: %d %s", rec.Code, rec.Body)
	}
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, PersonaID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "DELETE", base+"/"+ada.ID, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "ada-dev") {
		t.Fatalf("delete persona owning a sandbox: %d %s", rec.Code, rec.Body)
	}
	if err := s.Store.DeleteSandbox(context.Background(), env.ID, sb.ID); err != nil {
		t.Fatal(err)
	}

	// Persona-scoped rules: one scope, same environment; they go with the persona.
	rules := testOrigin + "/api/environments/" + env.ID + "/rules"
	other2 := newSandbox(t, s, env, "free")
	if rec := do(h, "POST", rules, `{"host":"example.com","action":"allow","personaId":"`+ada.ID+`","sandboxId":"`+other2.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rule with two scopes: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+other.ID+"/rules", `{"host":"example.com","action":"allow","personaId":"`+ada.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rule for another environment's persona: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", rules, `{"host":"example.com","action":"allow","personaId":"`+ada.ID+`"}`)
	if got := decode[store.Rule](t, rec); rec.Code != http.StatusCreated || got.PersonaID != ada.ID {
		t.Fatalf("persona rule: %d %s", rec.Code, rec.Body)
	}

	if rec := do(h, "DELETE", base+"/"+ada.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if got := decode[[]store.Rule](t, do(h, "GET", rules, "")); len(got) != 0 {
		t.Fatalf("persona rules survived it: %+v", got)
	}
	if rec := do(h, "DELETE", base+"/"+ada.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

func TestSandboxPersonaValidation(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	sub := createProvider(t, h, other, `{"name":"sub","kind":"claude_subscription"}`)
	rec := do(h, "POST", testOrigin+"/api/environments/"+other.ID+"/personas", `{"name":"Eve","harness":"claude","providerId":"`+sub.ID+`"}`)
	eve := decode[store.Persona](t, rec)
	if rec.Code != http.StatusCreated {
		t.Fatalf("persona: %d %s", rec.Code, rec.Body)
	}
	// The persona is checked before anything boots.
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes", `{"name":"dev","personaId":"`+eve.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("sandbox owned by another environment's persona: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/templates/t1/sandboxes", `{"name":"dev","personaId":"`+eve.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("template sandbox owned by another environment's persona: %d %s", rec.Code, rec.Body)
	}
}

func TestPersonaEndpointsGuarded(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	for _, path := range []string{"/providers", "/personas"} {
		url := testOrigin + "/api/environments/" + env.ID + path
		if rec := do(h, "POST", url, `{"name":"x","kind":"claude_subscription"}`, "Origin", "http://3000-x.localhost:7878"); rec.Code != http.StatusForbidden {
			t.Errorf("cross-site POST %s: %d", path, rec.Code)
		}
	}
	s.Auth = previewTestAuth(t, func(string) bool { return false })
	authed := Guard(s.Auth.Middleware(h))
	for _, path := range []string{"/providers", "/personas", "/../../provider-kinds"} {
		if rec := do(authed, "GET", testOrigin+"/api/environments/"+env.ID+path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session: %d", path, rec.Code)
		}
	}
}
