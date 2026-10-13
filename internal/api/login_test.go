package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/login"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// loginTokenText is a Claude Code token as `claude setup-token` prints it.
const loginTokenText = "sk-ant-oat01-AbCdEfGhIjKlMnOpQrStUvWx_-123456"

// loginVMs starts the system VMs of sign-ins; nothing runs in them but what loginGuest answers.
type loginVMs struct{}

func (loginVMs) EnsureSystem(_ context.Context, envID, kind, name string) (store.Sandbox, error) {
	return store.Sandbox{ID: "vm-" + name, EnvironmentID: envID, Kind: kind, Name: name}, nil
}

func (loginVMs) RemoveSystem(context.Context, string, string, string) error { return nil }
func (loginVMs) StopSystem(context.Context, string, string, string) error   { return nil }

// loginGuest answers a sign-in's commands. The login's pane shows pane, its CLI has exited when
// dead, and cat reads files in the VM's home.
type loginGuest struct {
	mu    sync.Mutex
	pane  string
	dead  bool
	files map[string]string
}

func (g *loginGuest) Run(_ context.Context, _ string, req agentproto.RunRequest) (agentproto.RunResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch strings.Join(req.Args[:2], " ") {
	case "tmux new-session", "tmux set-option":
		return agentproto.RunResult{}, nil
	case "tmux capture-pane":
		return agentproto.RunResult{Stdout: g.pane}, nil
	case "tmux display-message":
		if g.dead {
			return agentproto.RunResult{Stdout: "1\n"}, nil
		}
		return agentproto.RunResult{Stdout: "0\n"}, nil
	}
	if req.Args[0] == "cat" {
		if content, ok := g.files[req.Args[1]]; ok {
			return agentproto.RunResult{Stdout: content}, nil
		}
		return agentproto.RunResult{ExitCode: 1}, nil
	}
	return agentproto.RunResult{}, errors.New("unexpected command " + req.Args[0])
}

func (g *loginGuest) OpenPTY(context.Context, string, string, uint16, uint16) (*agentchan.PTY, error) {
	return nil, errors.New("no terminal in tests")
}

func (g *loginGuest) WriteHomeFiles(context.Context, string, []agentproto.HomeFile) error { return nil }

func (g *loginGuest) Connected(string) (agentproto.Hello, bool) { return agentproto.Hello{}, true }

func (g *loginGuest) set(pane string, dead bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pane, g.dead = pane, dead
}

// newLoginServer is the test server with a sign-in service over fast polling and loginGuest.
func newLoginServer(t *testing.T) (http.Handler, *Server, *loginGuest, store.Environment) {
	t.Helper()
	h, s := newTestServer(t)
	lg := &loginGuest{files: map[string]string{}}
	s.Login = login.New(s.Store, s.Vault, loginVMs{}, lg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Login.Poll = 5 * time.Millisecond
	return h, s, lg, newEnvironment(t, s, "work")
}

// awaitLogin polls a provider's sign-in until it has ended, and returns it.
func awaitLogin(t *testing.T, h http.Handler, env store.Environment, providerID string) LoginView {
	t.Helper()
	url := testOrigin + "/api/environments/" + env.ID + "/providers/" + providerID + "/login"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := do(h, "GET", url, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status: %d %s", rec.Code, rec.Body)
		}
		v := decode[LoginView](t, rec)
		if v.State == login.StateDone || v.State == login.StateFailed || v.State == login.StateIdle {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the sign-in did not end")
	return LoginView{}
}

// A Claude subscription signs in through the API: the sign-in starts, the token the CLI prints
// is stored as the provider's key, and the provider is ready without the token in any response.
func TestClaudeSignInThroughTheAPI(t *testing.T) {
	h, _, lg, env := newLoginServer(t)
	prov := createProvider(t, h, env, `{"name":"Claude Max","kind":"claude_subscription"}`)
	if prov.State != "login_required" {
		t.Fatalf("a new subscription: %+v", prov)
	}
	lg.set("Open https://claude.ai/oauth/authorize?x=1 to finish.", false)
	base := testOrigin + "/api/environments/" + env.ID + "/providers/" + prov.ID + "/login"

	rec := do(h, "POST", base, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	lg.set("Your OAuth token (valid for 1 year):\n\n"+loginTokenText+"\n", true)
	if v := awaitLogin(t, h, env, prov.ID); v.State != login.StateDone {
		t.Fatalf("the sign-in ended %+v", v)
	}

	rec = do(h, "GET", testOrigin+"/api/environments/"+env.ID+"/providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("providers: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), loginTokenText) {
		t.Fatal("the token reached a response")
	}
	for _, p := range decode[[]ProviderView](t, rec) {
		if p.ID == prov.ID && (p.State != "ready" || p.SecretID == "") {
			t.Fatalf("provider after sign-in: %+v", p)
		}
	}

	// Pasting is refused for a token that isn't one, and accepted for one that is.
	paste := testOrigin + "/api/environments/" + env.ID + "/providers/" + prov.ID + "/login/token"
	if rec := do(h, "POST", paste, `{"token":"not a token"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a bad paste: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", paste, `{"token":"`+loginTokenText+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("a paste: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", base, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
}

// The sign-in endpoints refuse what they can't do: an API key provider, an unknown provider, a
// terminal with no sign-in running, and every endpoint without the service.
func TestLoginEndpointsRefuse(t *testing.T) {
	h, s, _, env := newLoginServer(t)
	key := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/providers/"+key.ID+"/login", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a key provider signed in: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/providers/missing/login", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown provider: %d", rec.Code)
	}
	sub := createProvider(t, h, env, `{"name":"Max","kind":"claude_subscription"}`)
	if rec := do(h, "GET", testOrigin+"/api/environments/"+env.ID+"/providers/"+sub.ID+"/login/terminal", ""); rec.Code != http.StatusConflict {
		t.Fatalf("a terminal with no sign-in: %d", rec.Code)
	}
	s.Login = nil
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/providers/"+sub.ID+"/login", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("without the service: %d", rec.Code)
	}
}

// A ChatGPT login signs in from the auth file the CLI writes, and a session of its teammate gets
// placeholders only: no refresh token, no signature of the access token.
func TestChatGPTSessionGetsPlaceholdersOnly(t *testing.T) {
	h, s, lg, env := newLoginServer(t)
	g := &fakeGuest{sessions: []agentproto.Session{{Name: "main", Windows: 1}}}
	s.Guest = g
	prov := createProvider(t, h, env, `{"name":"Work","kind":"chatgpt_subscription"}`)
	lg.set("Open https://auth.openai.com/codex/device and enter WXYZ-1234", false)
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/providers/"+prov.ID+"/login", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	lg.mu.Lock()
	lg.files[".codex/auth.json"] = chatGPTAuthFile(t, time.Now().Add(time.Hour), "rt_secret_refresh", "accesssecretsig")
	lg.mu.Unlock()
	if v := awaitLogin(t, h, env, prov.ID); v.State != login.StateDone {
		t.Fatalf("the sign-in ended %+v", v)
	}

	ada := createTeammateFor(t, h, env, "codex", prov.ID)
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, TeammateID: ada})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes/"+sb.ID+"/sessions", ""); rec.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	sent, _ := json.Marshal(g.files)
	if strings.Contains(string(sent), "rt_secret_refresh") || strings.Contains(string(sent), "accesssecretsig") {
		t.Fatalf("a secret reached the guest: %s", sent)
	}
	var auth string
	for _, f := range g.files {
		if f.Path == ".codex/auth.json" {
			auth = f.Content
		}
	}
	if auth == "" || !strings.Contains(auth, "studio-") {
		t.Fatalf("the session's auth file: %q", auth)
	}
}

// A ChatGPT login whose access token has lapsed shows as expired, and sessions refuse it.
func TestExpiredChatGPTLoginRefusesSessions(t *testing.T) {
	h, s, lg, env := newLoginServer(t)
	s.Guest = &fakeGuest{}
	prov := createProvider(t, h, env, `{"name":"Work","kind":"chatgpt_subscription"}`)
	lg.set("Open https://auth.openai.com/codex/device and enter WXYZ-1234", false)
	do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/providers/"+prov.ID+"/login", "")
	lg.mu.Lock()
	lg.files[".codex/auth.json"] = chatGPTAuthFile(t, time.Now().Add(-time.Hour), "rt_secret_refresh", "sig")
	lg.mu.Unlock()
	awaitLogin(t, h, env, prov.ID)

	rec := do(h, "GET", testOrigin+"/api/environments/"+env.ID+"/providers", "")
	for _, p := range decode[[]ProviderView](t, rec) {
		if p.ID == prov.ID && p.State != "expired" {
			t.Fatalf("a lapsed login shows %s", p.State)
		}
	}
	ada := createTeammateFor(t, h, env, "codex", prov.ID)
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, TeammateID: ada})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes/"+sb.ID+"/sessions", ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("a session on an expired login: %d %s", rec.Code, rec.Body)
	}
}

// createTeammateFor creates a teammate on a provider and returns its ID.
func createTeammateFor(t *testing.T, h http.Handler, env store.Environment, harness, providerID string) string {
	t.Helper()
	rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/teammates",
		`{"name":"Ada","harness":"`+harness+`","providerId":"`+providerID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("teammate: %d %s", rec.Code, rec.Body)
	}
	return decode[store.Teammate](t, rec).ID
}

// chatGPTAuthFile is the auth file of a ChatGPT login whose access token expires at expires.
func chatGPTAuthFile(t *testing.T, expires time.Time, refresh, sig string) string {
	t.Helper()
	token := func(claims map[string]any, signature string) string {
		head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
		body, err := json.Marshal(claims)
		if err != nil {
			t.Fatal(err)
		}
		return head + "." + base64.RawURLEncoding.EncodeToString(body) + "." + signature
	}
	b, err := json.Marshal(map[string]any{
		"OPENAI_API_KEY": nil,
		"tokens": map[string]string{
			"id_token":      token(map[string]any{"email": "ada@example.com", "chatgpt_account_id": "acct"}, "idsig"),
			"access_token":  token(map[string]any{"exp": expires.Unix(), "sub": "user"}, sig),
			"refresh_token": refresh,
			"account_id":    "acct",
		},
		"last_refresh": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A Claude subscription's session gets its token's placeholder in CLAUDE_CODE_OAUTH_TOKEN, and the
// token itself appears in nothing the guest is sent.
func TestClaudeSessionGetsThePlaceholderOnly(t *testing.T) {
	h, s, lg, env := newLoginServer(t)
	g := &fakeGuest{sessions: []agentproto.Session{{Name: "main", Windows: 1}}}
	s.Guest = g
	prov := createProvider(t, h, env, `{"name":"Claude Max","kind":"claude_subscription"}`)
	lg.set("Open https://claude.ai/oauth/authorize?x=1 to finish.", false)
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/providers/"+prov.ID+"/login", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	lg.set("Your OAuth token (valid for 1 year):\n\n"+loginTokenText+"\n", true)
	if v := awaitLogin(t, h, env, prov.ID); v.State != login.StateDone {
		t.Fatalf("the sign-in ended %+v", v)
	}
	var placeholder string
	for _, p := range decode[[]ProviderView](t, do(h, "GET", testOrigin+"/api/environments/"+env.ID+"/providers", "")) {
		if p.ID == prov.ID {
			placeholder = p.Placeholder
		}
	}
	if placeholder == "" {
		t.Fatal("the provider has no placeholder")
	}

	ada := createTeammateFor(t, h, env, "claude", prov.ID)
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, TeammateID: ada})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes/"+sb.ID+"/sessions", ""); rec.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	if len(g.started) != 1 || len(g.started[0].Env) != 1 || g.started[0].Env["CLAUDE_CODE_OAUTH_TOKEN"] != placeholder {
		t.Fatalf("the session's environment: %+v", g.started)
	}
	sent, _ := json.Marshal(struct {
		F []agentproto.HomeFile
		S []agentproto.StartSession
	}{g.files, g.started})
	if strings.Contains(string(sent), loginTokenText) || strings.Contains(string(sent), "sk-ant-oat") {
		t.Fatalf("the token reached the guest: %s", sent)
	}
}
