package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

const environmentImportURL = testOrigin + "/api/environment-imports"

func TestEnvironmentFileRoundTrip(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	work := newEnvironment(t, s, "work")
	anthropic := createProvider(t, h, work, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	local := createProvider(t, h, work, `{"name":"Local","kind":"openai_compatible","apiKey":"sk-local-key","baseUrl":"https://llm.example.com/v1","model":"qwen3-coder"}`)
	teammateURL := testOrigin + "/api/environments/" + work.ID + "/teammates"
	ada := decode[store.Teammate](t, do(h, "POST", teammateURL, `{"name":"Ada","role":"Reviewer","avatar":"🦊","approvalScope":"teammate","soul":"# Ada\nCareful.","harness":"claude","providerId":"`+anthropic.ID+`","model":"claude-sonnet-5-5","gitName":"Ada (agent)","gitEmail":"ada@agents.invalid","useHostGitIdentity":true}`))
	do(h, "POST", teammateURL, `{"name":"Bob","harness":"opencode","providerId":"`+local.ID+`"}`)

	rulesURL := testOrigin + "/api/environments/" + work.ID + "/rules"
	for _, body := range []string{
		`{"host":"example.com","ports":[443],"action":"allow","note":"docs"}`,
		`{"host":"ada.example.org","action":"deny","teammateId":"` + ada.ID + `"}`,
	} {
		if rec := do(h, "POST", rulesURL, body); rec.Code != http.StatusCreated {
			t.Fatalf("rule %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if _, err := s.Vault.Create(ctx, store.Secret{EnvironmentID: work.ID, Name: "TOKEN", Hosts: []string{"api.example.com"}}, "tok-env-value"); err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "POST", rulesURL, `{"host":"api.example.com","action":"proxy","config":{"headers":{"Authorization":"Bearer {secret.TOKEN}"}}}`); rec.Code != http.StatusCreated {
		t.Fatalf("proxy rule: %d %s", rec.Code, rec.Body)
	}
	dev := newSandbox(t, s, work, "dev")
	if rec := do(h, "POST", rulesURL, `{"host":"dev.example.com","action":"allow","sandboxId":"`+dev.ID+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("sandbox rule: %d %s", rec.Code, rec.Body)
	}
	if _, err := s.Vault.Create(ctx, store.Secret{EnvironmentID: work.ID, SandboxID: dev.ID, Name: "DEV_ONLY", Hosts: []string{"dev.example.net"}}, "dev-value"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Vault.Create(ctx, store.Secret{EnvironmentID: work.ID, TeammateID: ada.ID, Name: "ADA_TOKEN", Hosts: []string{"api.github.com"}}, "ada-value"); err != nil {
		t.Fatal(err)
	}
	createReadyAPITemplate(t, s.Store, work.ID)

	// The export has the configuration and none of the values, keys or sandboxes.
	rec := do(h, "GET", testOrigin+"/api/environments/"+work.ID+"/export", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Disposition"), "work.studio-environment") {
		t.Fatalf("export: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	exported := rec.Body.String()
	for _, private := range []string{"tok-env-value", "ada-value", "dev-value", "sk-local-key", providerKey, dev.ID, "dev.example.com", "DEV_ONLY", "PROVIDER_"} {
		if strings.Contains(exported, private) {
			t.Errorf("export carries %q", private)
		}
	}
	file := decode[EnvironmentFile](t, rec)
	if file.Format != environmentFormat || file.Version != environmentVersion || file.Name != "work" {
		t.Fatalf("file header: %+v", file)
	}
	// The first provider also creates the default teammate, Iris.
	if len(file.Providers) != 2 || len(file.Teammates) != 3 || len(file.Rules) != 3 || len(file.Secrets) != 2 || len(file.Templates) != 1 {
		t.Fatalf("file content: %d providers, %d teammates, %d rules, %d secrets, %d templates",
			len(file.Providers), len(file.Teammates), len(file.Rules), len(file.Secrets), len(file.Templates))
	}
	if file.Teammates[0].Name != "Ada" || file.Teammates[0].Provider != "Anthropic" || file.Teammates[0].ApprovalScope != "teammate" || file.Teammates[0].Avatar != "🦊" {
		t.Fatalf("exported teammate: %+v", file.Teammates[0])
	}
	if _, err := templatespec.ParseYAML([]byte(file.Templates[0].Spec)); err != nil {
		t.Fatalf("exported template spec: %v", err)
	}

	// Importing it creates a second environment, with the providers still missing their keys.
	rec = do(h, "POST", environmentImportURL, exported)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	imported := decode[ImportedEnvironment](t, rec)
	if imported.Environment.Name != "work 2" || imported.Environment.ID == work.ID {
		t.Fatalf("imported environment: %+v", imported.Environment)
	}
	base := testOrigin + "/api/environments/" + imported.Environment.ID
	providers := decode[[]ProviderView](t, do(h, "GET", base+"/providers", ""))
	if len(providers) != 2 {
		t.Fatalf("imported providers: %+v", providers)
	}
	var newAnthropic ProviderView
	for _, p := range providers {
		if p.Name == "Anthropic" {
			newAnthropic = p
		}
		if p.State != "key_required" || p.SecretID != "" {
			t.Fatalf("imported provider has a key or is ready: %+v", p)
		}
	}
	local2 := providers[0]
	if local2.Name != "Local" {
		local2 = providers[1]
	}
	if local2.BaseURL != "https://llm.example.com/v1" || local2.Model != "qwen3-coder" {
		t.Fatalf("compatible provider: %+v", local2)
	}
	teammates := decode[[]store.Teammate](t, do(h, "GET", base+"/teammates", ""))
	if len(teammates) != 3 {
		t.Fatalf("imported teammates: %+v", teammates)
	}
	var newAda store.Teammate
	for _, tm := range teammates {
		if tm.Name == "Ada" {
			newAda = tm
		}
	}
	if newAda.ProviderID != newAnthropic.ID || newAda.Avatar != "🦊" || newAda.ApprovalScope != "teammate" || newAda.Soul != ada.Soul ||
		newAda.Model != ada.Model || newAda.GitEmail != ada.GitEmail || newAda.Role != ada.Role || !newAda.UseHostGitIdentity {
		t.Fatalf("imported Ada: %+v", newAda)
	}
	rules := decode[[]store.Rule](t, do(h, "GET", base+"/rules", ""))
	if len(rules) != 3 {
		t.Fatalf("imported rules: %+v", rules)
	}
	if i := slices.IndexFunc(rules, func(r store.Rule) bool { return r.Host == "ada.example.org" }); i < 0 || rules[i].TeammateID != newAda.ID || rules[i].Action != store.ActionDeny {
		t.Fatalf("teammate rule: %+v", rules)
	}
	if i := slices.IndexFunc(rules, func(r store.Rule) bool { return r.Action == store.ActionProxy }); i < 0 || rules[i].Config.Headers["Authorization"] != "Bearer {secret.TOKEN}" {
		t.Fatalf("proxy rule: %+v", rules)
	}
	if list, _ := s.Store.Secrets(ctx, imported.Environment.ID); len(list) != 0 {
		t.Fatalf("an import created secrets: %+v", list)
	}
	if list, _ := s.Store.Sandboxes(ctx, imported.Environment.ID); len(list) != 0 {
		t.Fatalf("an import created sandboxes: %+v", list)
	}

	// What the user still has to add: the two provider keys, and the two secret values.
	var keys, secretNames []string
	for _, item := range imported.ToAdd {
		switch item.Kind {
		case "providerKey":
			keys = append(keys, item.Name)
		case "secret":
			secretNames = append(secretNames, item.Name+"@"+item.Teammate)
		}
	}
	slices.Sort(keys)
	slices.Sort(secretNames)
	if !slices.Equal(keys, []string{"Anthropic", "Local"}) || !slices.Equal(secretNames, []string{"ADA_TOKEN@Ada", "TOKEN@"}) {
		t.Fatalf("to add: %+v", imported.ToAdd)
	}
	if len(imported.Templates) != 1 {
		t.Fatalf("templates to build: %+v", imported.Templates)
	}

	// Adding the key makes the provider ready; a second key rotates it.
	rec = do(h, "PUT", base+"/providers/"+newAnthropic.ID, `{"apiKey":"`+providerKey+`"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), providerKey) {
		t.Fatalf("add key: %d %s", rec.Code, rec.Body)
	}
	if got := decode[ProviderView](t, rec); got.State != "ready" || got.SecretName != "PROVIDER_ANTHROPIC_KEY" || got.Placeholder == "" {
		t.Fatalf("provider after its key: %+v", got)
	}
	if got := boundValue(t, s, imported.Environment.ID); got != providerKey {
		t.Fatalf("bound key %q", got)
	}
	if rec := do(h, "PUT", base+"/providers/"+newAnthropic.ID, `{"apiKey":"sk-rotated-key"}`); rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	if got := boundValue(t, s, imported.Environment.ID); got != "sk-rotated-key" {
		t.Fatalf("rotated key %q", got)
	}

	// The original is untouched.
	if list := decode[[]store.Teammate](t, do(h, "GET", teammateURL, "")); len(list) != 3 {
		t.Fatalf("the source environment changed: %+v", list)
	}
}

func TestEnvironmentFileRefusals(t *testing.T) {
	h, s := newTestServer(t)
	count := func() int {
		list, err := s.Store.Environments(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	before := count()
	const empty = `"providers":[],"teammates":[],"rules":[],"secrets":[],"templates":[]`
	file := func(name, rest string) string {
		return `{"format":"studio-environment","version":1,"name":"` + name + `","exportedAt":"2026-10-13T10:00:00Z",` + empty + rest + `}`
	}
	anthropic := `{"name":"Anthropic","kind":"anthropic_api"}`
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"not JSON", "nope", http.StatusUnprocessableEntity},
		{"unknown field", file("x", `,"extra":1`), http.StatusUnprocessableEntity},
		{"other format", strings.Replace(file("x", ""), "studio-environment", "studio-sandbox", 1), http.StatusUnprocessableEntity},
		{"other version", strings.Replace(file("x", ""), `"version":1`, `"version":2`, 1), http.StatusUnprocessableEntity},
		{"trailing data", file("x", "") + " {}", http.StatusUnprocessableEntity},
		{"blank name", file(" x", ""), http.StatusUnprocessableEntity},
		{"unknown provider kind", file("x", `,"providers":[{"name":"G","kind":"gemini_api"}]`), http.StatusUnprocessableEntity},
		{"compatible without a model", file("x", `,"providers":[{"name":"L","kind":"openai_compatible","baseUrl":"https://llm.example.com"}]`), http.StatusUnprocessableEntity},
		{"compatible on a private address", file("x", `,"providers":[{"name":"L","kind":"openai_compatible","baseUrl":"https://10.0.0.1","model":"m"}]`), http.StatusUnprocessableEntity},
		{"base URL on an API provider", file("x", `,"providers":[{"name":"A","kind":"anthropic_api","baseUrl":"https://a.example.com"}]`), http.StatusUnprocessableEntity},
		{"duplicate provider", file("x", `,"providers":[`+anthropic+`,`+anthropic+`]`), http.StatusUnprocessableEntity},
		{"teammate of a missing provider", file("x", `,"teammates":[{"name":"Ada","harness":"claude","provider":"Nope","model":"m"}]`), http.StatusUnprocessableEntity},
		{"harness the provider can't run", file("x", `,"providers":[`+anthropic+`],"teammates":[{"name":"Ada","harness":"codex","provider":"Anthropic","model":"m"}]`), http.StatusUnprocessableEntity},
		{"API provider teammate without a model", file("x", `,"providers":[`+anthropic+`],"teammates":[{"name":"Ada","harness":"claude","provider":"Anthropic"}]`), http.StatusUnprocessableEntity},
		{"unknown harness", file("x", `,"teammates":[{"name":"Ada","harness":"aider"}]`), http.StatusUnprocessableEntity},
		{"unknown approval scope", file("x", `,"teammates":[{"name":"Ada","harness":"claude","approvalScope":"everywhere"}]`), http.StatusUnprocessableEntity},
		{"avatar that is a link", file("x", `,"teammates":[{"name":"Ada","harness":"claude","avatar":"https://evil.example/a.png"}]`), http.StatusUnprocessableEntity},
		{"duplicate teammate", file("x", `,"teammates":[{"name":"Ada","harness":"opencode"},{"name":"Ada","harness":"opencode"}]`), http.StatusUnprocessableEntity},
		{"rule of a missing teammate", file("x", `,"rules":[{"host":"example.com","action":"allow","teammate":"Nobody"}]`), http.StatusUnprocessableEntity},
		{"rule with a bad host", file("x", `,"rules":[{"host":"*.*.com","action":"allow"}]`), http.StatusUnprocessableEntity},
		{"rule with a bad port", file("x", `,"rules":[{"host":"example.com","ports":[70000],"action":"allow"}]`), http.StatusUnprocessableEntity},
		{"rule with an unknown action", file("x", `,"rules":[{"host":"example.com","action":"block"}]`), http.StatusUnprocessableEntity},
		{"headers on an allow rule", file("x", `,"rules":[{"host":"example.com","action":"allow","headers":{"X-A":"1"}}]`), http.StatusUnprocessableEntity},
		{"header naming an unknown secret", file("x", `,"rules":[{"host":"example.com","action":"proxy","headers":{"X-Key":"{secret.NOPE}"}}]`), http.StatusUnprocessableEntity},
		{"secret name in lower case", file("x", `,"secrets":[{"name":"token","hosts":["a.example.com"]}]`), http.StatusUnprocessableEntity},
		{"secret without hosts", file("x", `,"secrets":[{"name":"TOKEN","hosts":[]}]`), http.StatusUnprocessableEntity},
		{"secret with a bare wildcard", file("x", `,"secrets":[{"name":"TOKEN","hosts":["*"]}]`), http.StatusUnprocessableEntity},
		{"secret of a missing teammate", file("x", `,"secrets":[{"name":"TOKEN","hosts":["a.example.com"],"teammate":"Nobody"}]`), http.StatusUnprocessableEntity},
		{"two secrets of one name", file("x", `,"secrets":[{"name":"TOKEN","hosts":["a.example.com"]},{"name":"TOKEN","hosts":["b.example.com"]}]`), http.StatusUnprocessableEntity},
		{"template that is not a spec", file("x", `,"templates":[{"spec":"not: [valid"}]`), http.StatusUnprocessableEntity},
		{"too large", strings.Repeat(" ", maxEnvironmentFileBytes) + file("x", ""), http.StatusRequestEntityTooLarge},
	} {
		if rec := do(h, "POST", environmentImportURL, tc.body); rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
	if rec := do(h, "POST", environmentImportURL, file("x", ""), "Content-Type", "text/plain"); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text body: %d", rec.Code)
	}
	if count() != before {
		t.Fatalf("a refused import created an environment")
	}
	if rec := do(h, "POST", environmentImportURL, file("fresh", "")); rec.Code != http.StatusCreated || count() != before+1 {
		t.Fatalf("a valid empty file: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", environmentImportURL, file("fresh", "")); rec.Code != http.StatusCreated || decode[ImportedEnvironment](t, rec).Environment.Name != "fresh 2" {
		t.Fatalf("second import of the same name: %s", rec.Body)
	}
}

func TestEnvironmentExportNotFound(t *testing.T) {
	h, _ := newTestServer(t)
	if rec := do(h, "GET", testOrigin+"/api/environments/nope/export", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown environment: %d", rec.Code)
	}
}
