package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const secretValue = "sk-live-7f3a9c2e51"

// memKeys keeps the vault key in memory, so tests never touch the OS keychain.
type memKeys struct {
	name string
	key  []byte
}

func (m *memKeys) Name() string { return m.name }

func (m *memKeys) Get() ([]byte, error) {
	if m.key == nil {
		return nil, secrets.ErrNoKey
	}
	return m.key, nil
}

func (m *memKeys) Set(key []byte) error {
	m.key = key
	return nil
}

// boundValue returns the value the gateway would substitute for the environment's first secret.
func boundValue(t *testing.T, s *Server, envID string) string {
	t.Helper()
	b, err := s.Vault.Bindings(context.Background(), store.Sandbox{EnvironmentID: envID})
	if err != nil || len(b) == 0 {
		t.Fatalf("bindings: %+v %v", b, err)
	}
	return string(b[0].Value)
}

func expectEvent(t *testing.T, ch <-chan events.Event, want events.Event) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("event %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no event %+v", want)
	}
}

func TestSecretsCRUD(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/secrets"
	ch, unsubscribe := s.Bus.Subscribe()
	defer unsubscribe()
	changed := events.Event{Topic: events.TopicSecrets, EnvironmentID: env.ID}

	rec := do(h, "POST", base, `{"name":"OPENAI_API_KEY","value":"`+secretValue+`","hosts":["api.openai.com"],"note":"llm"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), secretValue) {
		t.Fatalf("create response carries the value: %s", rec.Body)
	}
	created := decode[store.Secret](t, rec)
	if created.Name != "OPENAI_API_KEY" || created.EnvironmentID != env.ID || created.Note != "llm" ||
		!slices.Equal(created.Hosts, []string{"api.openai.com"}) || !strings.HasPrefix(created.Placeholder, "studio-") {
		t.Fatalf("created: %+v", created)
	}
	expectEvent(t, ch, changed)

	list := do(h, "GET", base, "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), secretValue) {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	if got := decode[[]store.Secret](t, list); len(got) != 1 || got[0].ID != created.ID || got[0].Placeholder != created.Placeholder {
		t.Fatalf("list: %+v", got)
	}
	var raw []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &raw); err != nil || len(raw) != 1 {
		t.Fatalf("list body: %s (%v)", list.Body, err)
	}
	for _, key := range []string{"value", "sealed"} {
		if _, ok := raw[0][key]; ok {
			t.Fatalf("list exposes %q: %s", key, list.Body)
		}
	}
	if got := boundValue(t, s, env.ID); got != secretValue {
		t.Fatalf("bound value %q", got)
	}

	// Omitting the value keeps the stored one; hosts and note still change.
	rec = do(h, "PUT", base+"/"+created.ID, `{"hosts":["api.openai.org"],"note":"moved"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), secretValue) {
		t.Fatalf("update without value: %d %s", rec.Code, rec.Body)
	}
	updated := decode[store.Secret](t, rec)
	if updated.Note != "moved" || !slices.Equal(updated.Hosts, []string{"api.openai.org"}) || updated.Placeholder != created.Placeholder {
		t.Fatalf("updated: %+v", updated)
	}
	if got := boundValue(t, s, env.ID); got != secretValue {
		t.Fatalf("update without value replaced the value: %q", got)
	}
	expectEvent(t, ch, changed)

	if rec := do(h, "PUT", base+"/"+created.ID, `{"value":"sk-rotated","hosts":["api.openai.org"]}`); rec.Code != http.StatusOK {
		t.Fatalf("update with value: %d %s", rec.Code, rec.Body)
	}
	if got := boundValue(t, s, env.ID); got != "sk-rotated" {
		t.Fatalf("rotated value %q", got)
	}
	expectEvent(t, ch, changed)

	if rec := do(h, "DELETE", base+"/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	expectEvent(t, ch, changed)
	if rec := do(h, "DELETE", base+"/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", rec.Code)
	}
	if got := decode[[]store.Secret](t, do(h, "GET", base, "")); len(got) != 0 {
		t.Fatalf("list after delete: %+v", got)
	}
}

func TestSecretValidation(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/secrets"

	for _, tc := range []struct{ name, body string }{
		{"lower-case name", `{"name":"api_key","value":"v","hosts":["a.com"]}`},
		{"reserved name", `{"name":"PATH","value":"v","hosts":["a.com"]}`},
		{"reserved prefix", `{"name":"STUDIO_TOKEN","value":"v","hosts":["a.com"]}`},
		{"empty value", `{"name":"KEY","value":"","hosts":["a.com"]}`},
		{"newline-only value", `{"name":"KEY","value":"\n","hosts":["a.com"]}`},
		{"bare wildcard", `{"name":"KEY","value":"v","hosts":["*"]}`},
		{"no hosts", `{"name":"KEY","value":"v","hosts":[]}`},
		{"bad host", `{"name":"KEY","value":"v","hosts":["*.*.com"]}`},
	} {
		if rec := do(h, "POST", base, tc.body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422 (%s)", tc.name, rec.Code, rec.Body)
		}
	}

	rec := do(h, "POST", base, `{"name":"KEY","value":"v","hosts":["a.com"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := decode[store.Secret](t, rec).ID
	if rec := do(h, "POST", base, `{"name":"KEY","value":"other","hosts":["a.com"]}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", base+"/"+id, `{"hosts":["*"]}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("update to bare wildcard: got %d, want 422", rec.Code)
	}
	if rec := do(h, "PUT", base+"/nope", `{"hosts":["a.com"]}`); rec.Code != http.StatusNotFound {
		t.Errorf("update unknown secret: got %d, want 404", rec.Code)
	}
	if rec := do(h, "GET", testOrigin+"/api/environments/nope/secrets", ""); rec.Code != http.StatusNotFound {
		t.Errorf("list in unknown environment: got %d, want 404", rec.Code)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/nope/secrets", `{"name":"KEY","value":"v","hosts":["a.com"]}`); rec.Code != http.StatusNotFound {
		t.Errorf("create in unknown environment: got %d, want 404", rec.Code)
	}
}

func TestScopedSecrets(t *testing.T) {
	h, s := newTestServer(t)
	ctx := t.Context()
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	base := testOrigin + "/api/environments/" + env.ID + "/secrets"
	prov, _ := s.Store.CreateProvider(ctx, store.Provider{ID: store.NewID(), EnvironmentID: env.ID, Name: "claude", Kind: "claude_subscription"}, nil)
	ada, err := s.Store.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Ada", Harness: "claude", ProviderID: prov.ID, GitName: "Ada", GitEmail: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	adaBox, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "ada-box", CPUs: 1, TeammateID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	loose := newSandbox(t, s, env, "loose")
	elsewhere := newSandbox(t, s, other, "elsewhere")
	ch, unsubscribe := s.Bus.Subscribe()
	defer unsubscribe()

	create := func(scope string) store.Secret {
		t.Helper()
		rec := do(h, "POST", base, `{"name":"GH_TOKEN","value":"v","hosts":["api.github.com"]`+scope+`}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", scope, rec.Code, rec.Body)
		}
		return decode[store.Secret](t, rec)
	}
	envSecret := create("")
	expectEvent(t, ch, events.Event{Topic: events.TopicSecrets, EnvironmentID: env.ID})
	teammateSecret := create(`,"teammateId":"` + ada.ID + `"`)
	expectEvent(t, ch, events.Event{Topic: events.TopicSecrets, EnvironmentID: env.ID, TeammateID: ada.ID})
	sandboxSecret := create(`,"sandboxId":"` + adaBox.ID + `"`)
	expectEvent(t, ch, events.Event{Topic: events.TopicSecrets, EnvironmentID: env.ID, SandboxID: adaBox.ID})
	if envSecret.TeammateID != "" || envSecret.SandboxID != "" || teammateSecret.TeammateID != ada.ID || sandboxSecret.SandboxID != adaBox.ID {
		t.Fatalf("scopes: %+v %+v %+v", envSecret, teammateSecret, sandboxSecret)
	}

	for _, tc := range []struct {
		body string
		code int
	}{
		{`,"teammateId":"` + ada.ID + `"`, http.StatusConflict},
		{`,"teammateId":"` + ada.ID + `","sandboxId":"` + loose.ID + `"`, http.StatusUnprocessableEntity},
		{`,"sandboxId":"` + elsewhere.ID + `"`, http.StatusUnprocessableEntity},
		{`,"teammateId":"nobody"`, http.StatusUnprocessableEntity},
	} {
		if rec := do(h, "POST", base, `{"name":"GH_TOKEN","value":"x","hosts":["a.com"]`+tc.body+`}`); rec.Code != tc.code {
			t.Errorf("%s: %d %s", tc.body, rec.Code, rec.Body)
		}
	}

	list := decode[[]store.Secret](t, do(h, "GET", base, ""))
	if len(list) != 3 {
		t.Fatalf("list: %+v", list)
	}
	raw := decode[[]map[string]any](t, do(h, "GET", base, ""))
	var teammates, sandboxes int
	for _, r := range raw {
		if r["teammateId"] == ada.ID {
			teammates++
		}
		if r["sandboxId"] == adaBox.ID {
			sandboxes++
		}
	}
	if teammates != 1 || sandboxes != 1 {
		t.Fatalf("list hides the scopes: %v", raw)
	}

	// The scope is fixed: an update naming one is refused, and nothing changes.
	if rec := do(h, "PUT", base+"/"+teammateSecret.ID, `{"hosts":["api.github.com"],"sandboxId":"`+loose.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update with a scope: %d %s", rec.Code, rec.Body)
	}
	// Editing keeps the scope; only the affected sandboxes hear of it.
	if rec := do(h, "PUT", base+"/"+teammateSecret.ID, `{"hosts":["api.github.com"],"note":"n"}`); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if got, _ := s.Store.SecretByID(ctx, env.ID, teammateSecret.ID); got.TeammateID != ada.ID || got.SandboxID != "" || got.Note != "n" {
		t.Fatalf("update changed the scope or dropped the note: %+v", got)
	}
	expectEvent(t, ch, events.Event{Topic: events.TopicSecrets, EnvironmentID: env.ID, TeammateID: ada.ID})

	// Each sandbox resolves GH_TOKEN to its most specific scope.
	sees := func(sb store.Sandbox, want store.Secret) {
		t.Helper()
		if e, err := s.Vault.Env(ctx, sb); err != nil || e["GH_TOKEN"] != want.Placeholder {
			t.Errorf("%s sees %v (%v), want %s", sb.Name, e, err, want.Placeholder)
		}
	}
	sees(adaBox, sandboxSecret)
	sees(loose, envSecret)

	if rec := do(h, "DELETE", base+"/"+sandboxSecret.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	expectEvent(t, ch, events.Event{Topic: events.TopicSecrets, EnvironmentID: env.ID, SandboxID: adaBox.ID})
	sees(adaBox, teammateSecret)
	sees(loose, envSecret)
}
