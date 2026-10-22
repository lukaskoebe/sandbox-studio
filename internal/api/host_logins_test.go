package api

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/hostlogins"
)

func TestHostLogins(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")

	// Without a finder the host has no logins.
	if got := decode[HostLogins](t, do(h, "GET", testOrigin+"/api/system/host-logins", "")); got != (HostLogins{}) {
		t.Fatalf("without a finder: %+v", got)
	}
	if rec := do(h, "GET", testOrigin+"/api/system/host-logins/codex", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("codex without a finder: %d %s", rec.Code, rec.Body)
	}

	home := t.TempDir()
	s.HostLogins = &hostlogins.Finder{Home: home, Getenv: func(string) string { return "" }}
	write := func(rel, body string) {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"ada@example.com","https://api.openai.com/auth":{"chatgpt_plan_type":"plus"}}`))
	write(".codex/auth.json", `{"tokens":{"id_token":"aGVhZA.`+claims+`.c2ln","access_token":"a.b.c","refresh_token":"rt-host-secret"}}`)
	write(".local/share/opencode/auth.json", `{"anthropic":{"type":"api","key":"`+providerKey+`"},"openai":{"type":"oauth","refresh":"rt-host-secret"}}`)

	if got := decode[HostLogins](t, do(h, "GET", testOrigin+"/api/system/host-logins", "")); got != (HostLogins{Codex: true, Opencode: true}) {
		t.Fatalf("host logins: %+v", got)
	}
	rec := do(h, "GET", testOrigin+"/api/system/host-logins/codex", "")
	if got := decode[HostCodexAccount](t, rec); got != (HostCodexAccount{Email: "ada@example.com", Plan: "plus"}) || strings.Contains(rec.Body.String(), "rt-host-secret") {
		t.Fatalf("codex account: %s", rec.Body)
	}

	// The listing names the credentials without their content.
	rec = do(h, "GET", testOrigin+"/api/system/host-logins/opencode", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), providerKey) || strings.Contains(rec.Body.String(), "rt-host-secret") {
		t.Fatalf("opencode keys: %d %s", rec.Code, rec.Body)
	}
	keys := decode[[]HostOpencodeKey](t, rec)
	if len(keys) != 2 || keys[0].Provider != "anthropic" || keys[0].Kind != "anthropic_api" || keys[1].Kind != "" {
		t.Fatalf("opencode keys: %+v", keys)
	}

	base := testOrigin + "/api/environments/" + env.ID + "/providers/import-opencode"
	if rec := do(h, "POST", base, `{"provider":"openai","name":"OpenAI"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("importing an OAuth entry: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", base, `{"provider":"anthropic","name":"Anthropic"}`)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), providerKey) {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if p := decode[ProviderView](t, rec); p.Kind != "anthropic_api" || p.State != "ready" {
		t.Fatalf("imported provider: %+v", p)
	}
	if got := boundValue(t, s, env.ID); got != providerKey {
		t.Fatalf("the vault holds %q", got)
	}
	// The name is the provider's, so a second import of the same key needs another one.
	if rec := do(h, "POST", base, `{"provider":"anthropic","name":"Anthropic"}`); rec.Code != http.StatusConflict {
		t.Fatalf("a second import under the same name: %d %s", rec.Code, rec.Body)
	}
}
