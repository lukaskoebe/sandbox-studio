package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
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
	expectEvent(t, ch, events.Event{Topic: events.TopicTeammates, EnvironmentID: env.ID})
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

	// The default teammate runs on this provider, so the provider stays until the teammate is gone.
	if rec := do(h, "DELETE", base+"/"+p.ID, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete with a teammate on it: %d %s", rec.Code, rec.Body)
	}
	iris := listTeammates(t, h, env.ID)[0]
	if rec := do(h, "DELETE", testOrigin+"/api/environments/"+env.ID+"/teammates/"+iris.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete default teammate: %d %s", rec.Code, rec.Body)
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
	if _, err := s.Vault.Create(context.Background(), store.Secret{EnvironmentID: env.ID, Name: "PROVIDER_TAKEN_KEY", Hosts: []string{"a.com"}}, "v"); err != nil {
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

func TestTeammatesCRUD(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	base := testOrigin + "/api/environments/" + env.ID + "/teammates"
	anthropic := createProvider(t, h, env, `{"name":"anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	compat := createProvider(t, h, env, `{"name":"local","kind":"openai_compatible","apiKey":"k","baseUrl":"https://llm.example.com/v1","model":"qwen3-coder"}`)
	foreign := createProvider(t, h, other, `{"name":"anthropic","kind":"anthropic_api","apiKey":"k"}`)

	rec := do(h, "POST", base, `{"name":"Ada Lovelace","role":"Reviewer","soul":"# Ada\nCareful.","harness":"claude","providerId":"`+anthropic.ID+`","model":"claude-sonnet-4-5"}`)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), providerKey) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	ada := decode[store.Teammate](t, rec)
	if ada.GitName != "Ada Lovelace" || ada.GitEmail != "ada-lovelace@agents.invalid" || ada.Soul != "# Ada\nCareful." {
		t.Fatalf("defaults: %+v", ada)
	}
	rec = do(h, "POST", base, `{"name":"Bob","harness":"opencode","providerId":"`+compat.ID+`","gitEmail":"bob@example.com"}`)
	bob := decode[store.Teammate](t, rec)
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
	if got := decode[store.Teammate](t, rec); rec.Code != http.StatusOK || got.Role != "Author" || got.Harness != "opencode" || got.GitName != "Ada L." {
		t.Fatalf("update: %d %+v", rec.Code, got)
	}
	if rec := do(h, "PUT", testOrigin+"/api/environments/"+other.ID+"/teammates/"+ada.ID, `{"name":"Ada","harness":"claude","providerId":"`+foreign.ID+`","model":"m"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-environment update: %d %s", rec.Code, rec.Body)
	}
	// Iris, the default teammate on the first provider, is listed with Ada and Bob.
	if got := decode[[]store.Teammate](t, do(h, "GET", base, "")); len(got) != 3 {
		t.Fatalf("list: %+v", got)
	}
	// The other environment has only its own default teammate, on its own provider.
	if got := decode[[]store.Teammate](t, do(h, "GET", testOrigin+"/api/environments/"+other.ID+"/teammates", "")); len(got) != 1 || got[0].ProviderID != foreign.ID {
		t.Fatalf("teammates leak across environments: %+v", got)
	}

	// A provider in use and a teammate owning sandboxes can't be deleted.
	if rec := do(h, "DELETE", testOrigin+"/api/environments/"+env.ID+"/providers/"+anthropic.ID, ""); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "Ada") {
		t.Fatalf("delete provider in use: %d %s", rec.Code, rec.Body)
	}
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, TeammateID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "DELETE", base+"/"+ada.ID, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "ada-dev") {
		t.Fatalf("delete teammate owning a sandbox: %d %s", rec.Code, rec.Body)
	}
	if err := s.Store.DeleteSandbox(context.Background(), env.ID, sb.ID); err != nil {
		t.Fatal(err)
	}

	// Teammate-scoped rules: one scope, same environment; they go with the teammate.
	rules := testOrigin + "/api/environments/" + env.ID + "/rules"
	other2 := newSandbox(t, s, env, "free")
	if rec := do(h, "POST", rules, `{"host":"example.com","action":"allow","teammateId":"`+ada.ID+`","sandboxId":"`+other2.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rule with two scopes: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+other.ID+"/rules", `{"host":"example.com","action":"allow","teammateId":"`+ada.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rule for another environment's teammate: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", rules, `{"host":"example.com","action":"allow","teammateId":"`+ada.ID+`"}`)
	if got := decode[store.Rule](t, rec); rec.Code != http.StatusCreated || got.TeammateID != ada.ID {
		t.Fatalf("teammate rule: %d %s", rec.Code, rec.Body)
	}

	if rec := do(h, "DELETE", base+"/"+ada.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if got := decode[[]store.Rule](t, do(h, "GET", rules, "")); len(got) != 0 {
		t.Fatalf("teammate rules survived it: %+v", got)
	}
	if rec := do(h, "DELETE", base+"/"+ada.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

func TestTeammateAvatar(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/teammates"
	anthropic := createProvider(t, h, env, `{"name":"anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	body := func(avatar string) string {
		return `{"name":"Ada","harness":"claude","providerId":"` + anthropic.ID + `","model":"m","avatar":"` + avatar + `"}`
	}

	rec := do(h, "POST", base, body("🧑🏽‍💻"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with emoji: %d %s", rec.Code, rec.Body)
	}
	ada := decode[store.Teammate](t, rec)
	if ada.Avatar != "🧑🏽‍💻" {
		t.Fatalf("avatar %q", ada.Avatar)
	}
	rec = do(h, "PUT", base+"/"+ada.ID, body("shape:hexagon"))
	if got := decode[store.Teammate](t, rec); rec.Code != http.StatusOK || got.Avatar != "shape:hexagon" {
		t.Fatalf("generated avatar: %d %+v", rec.Code, got)
	}
	rec = do(h, "PUT", base+"/"+ada.ID, body(""))
	if got := decode[store.Teammate](t, rec); rec.Code != http.StatusOK || got.Avatar != "" {
		t.Fatalf("back to initials: %d %+v", rec.Code, got)
	}
	for _, avatar := range []string{"shape:blob", "🦊🦊", "Ada", "<img src=x>", "data:image/png;base64,AAAA"} {
		if rec := do(h, "PUT", base+"/"+ada.ID, body(avatar)); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("avatar %q: got %d (%s)", avatar, rec.Code, rec.Body)
		}
	}
}

func TestSandboxTeammateValidation(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	sub := createProvider(t, h, other, `{"name":"sub","kind":"claude_subscription"}`)
	rec := do(h, "POST", testOrigin+"/api/environments/"+other.ID+"/teammates", `{"name":"Eve","harness":"claude","providerId":"`+sub.ID+`"}`)
	eve := decode[store.Teammate](t, rec)
	if rec.Code != http.StatusCreated {
		t.Fatalf("teammate: %d %s", rec.Code, rec.Body)
	}
	// The teammate is checked before anything boots.
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes", `{"name":"dev","teammateId":"`+eve.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("sandbox owned by another environment's teammate: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/templates/t1/sandboxes", `{"name":"dev","teammateId":"`+eve.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("template sandbox owned by another environment's teammate: %d %s", rec.Code, rec.Body)
	}
}

func TestTeammateEndpointsGuarded(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	for _, path := range []string{"/providers", "/teammates"} {
		url := testOrigin + "/api/environments/" + env.ID + path
		if rec := do(h, "POST", url, `{"name":"x","kind":"claude_subscription"}`, "Origin", "http://3000-x.localhost:7878"); rec.Code != http.StatusForbidden {
			t.Errorf("cross-site POST %s: %d", path, rec.Code)
		}
	}
	s.Auth = previewTestAuth(t, func(string) bool { return false })
	authed := Guard(s.Auth.Middleware(h))
	for _, path := range []string{"/providers", "/teammates", "/../../provider-kinds"} {
		if rec := do(authed, "GET", testOrigin+"/api/environments/"+env.ID+path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session: %d", path, rec.Code)
		}
	}
}

func TestCopyTeammate(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	work := newEnvironment(t, s, "work")
	private := newEnvironment(t, s, "private")
	other := newEnvironment(t, s, "other")
	anthropic := createProvider(t, h, work, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	base := testOrigin + "/api/environments/" + work.ID + "/teammates"
	ada := decode[store.Teammate](t, do(h, "POST", base, `{"name":"Ada Lovelace","role":"Reviewer","avatar":"🦊","soul":"# Ada","harness":"claude","providerId":"`+anthropic.ID+`","model":"claude-sonnet-5-5","gitName":"Ada (agent)","gitEmail":"ada@agents.invalid","approvalScope":"teammate"}`))
	if _, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: work.ID, Name: "ada-dev", CPUs: 1, TeammateID: ada.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Vault.Create(ctx, store.Secret{EnvironmentID: work.ID, TeammateID: ada.ID, Name: "ADA_TOKEN", Hosts: []string{"api.example.com"}}, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetApprovalPattern(ctx, work.ID, ada.ID, "wildcard"); err != nil {
		t.Fatal(err)
	}

	// The private environment has its own Anthropic provider and already has an Ada Lovelace.
	privateAnthropic := createProvider(t, h, private, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"sk-private"}`)
	if rec := do(h, "POST", testOrigin+"/api/environments/"+private.ID+"/teammates", `{"name":"Ada Lovelace","harness":"claude","providerId":"`+privateAnthropic.ID+`","model":"m"}`); rec.Code != http.StatusCreated {
		t.Fatalf("taken name: %d %s", rec.Code, rec.Body)
	}
	copyURL := base + "/" + ada.ID + "/copy"
	ch, unsubscribe := s.Bus.Subscribe()
	defer unsubscribe()

	rec := do(h, "POST", copyURL, `{"targetEnvironmentId":"`+private.ID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("copy: %d %s", rec.Code, rec.Body)
	}
	copied := decode[store.Teammate](t, rec)
	if copied.ID == ada.ID || copied.EnvironmentID != private.ID || copied.Name != "Ada Lovelace 2" || copied.ProviderID != privateAnthropic.ID {
		t.Fatalf("copy identity: %+v", copied)
	}
	if copied.Role != ada.Role || copied.Avatar != ada.Avatar || copied.Soul != ada.Soul || copied.Harness != ada.Harness ||
		copied.Model != ada.Model || copied.GitName != ada.GitName || copied.GitEmail != ada.GitEmail || copied.ApprovalScope != "teammate" {
		t.Fatalf("copied settings differ: %+v", copied)
	}
	expectEvent(t, ch, events.Event{Topic: events.TopicTeammates, EnvironmentID: private.ID})
	// The remembered pattern level, sandboxes and secrets stay behind.
	if got, _ := s.Store.Teammate(ctx, private.ID, copied.ID); got.ApprovalPattern != "" {
		t.Fatalf("the pattern level was copied: %q", got.ApprovalPattern)
	}
	if list, _ := s.Store.Sandboxes(ctx, private.ID); len(list) != 0 {
		t.Fatalf("sandboxes copied: %+v", list)
	}
	if list, _ := s.Store.Secrets(ctx, private.ID); slices.ContainsFunc(list, func(sec store.Secret) bool { return sec.Name == "ADA_TOKEN" }) {
		t.Fatalf("the teammate's secret was copied: %+v", list)
	}
	if _, err := s.Store.Teammate(ctx, work.ID, ada.ID); err != nil {
		t.Fatalf("the source teammate changed: %v", err)
	}
	if rec := do(h, "POST", copyURL, `{"targetEnvironmentId":"`+private.ID+`"}`); decode[store.Teammate](t, rec).Name != "Ada Lovelace 3" {
		t.Fatalf("second copy: %d %s", rec.Code, rec.Body)
	}

	// Without a provider of the same kind the copy is created with none.
	createProvider(t, h, other, `{"name":"OpenAI","kind":"openai_api","apiKey":"sk-openai"}`)
	rec = do(h, "POST", copyURL, `{"targetEnvironmentId":"`+other.ID+`"}`)
	if none := decode[store.Teammate](t, rec); rec.Code != http.StatusCreated || none.ProviderID != "" || none.Name != "Ada Lovelace" {
		t.Fatalf("copy without a provider: %d %+v", rec.Code, none)
	}

	for _, tc := range []struct {
		name, url, body string
		want            int
	}{
		{"same environment", copyURL, `{"targetEnvironmentId":"` + work.ID + `"}`, http.StatusUnprocessableEntity},
		{"unknown environment", copyURL, `{"targetEnvironmentId":"nope"}`, http.StatusUnprocessableEntity},
		{"no target", copyURL, `{}`, http.StatusUnprocessableEntity},
		{"unknown teammate", base + "/nope/copy", `{"targetEnvironmentId":"` + private.ID + `"}`, http.StatusNotFound},
	} {
		if rec := do(h, "POST", tc.url, tc.body); rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
}

func TestUniqueName(t *testing.T) {
	long := strings.Repeat("a", teammates.MaxNameLen)
	for _, tc := range []struct {
		name, want string
		taken      []string
	}{
		{"Ada", "Ada", nil},
		{"Ada", "Ada 2", []string{"Ada"}},
		{"Ada", "Ada 3", []string{"Ada", "Ada 2"}},
		{long, strings.Repeat("a", teammates.MaxNameLen-2) + " 2", []string{long}},
	} {
		got := uniqueName(tc.name, func(n string) bool { return slices.Contains(tc.taken, n) })
		if got != tc.want || len([]rune(got)) > teammates.MaxNameLen {
			t.Errorf("uniqueName(%q, %v) = %q, want %q", tc.name, tc.taken, got, tc.want)
		}
	}
}
