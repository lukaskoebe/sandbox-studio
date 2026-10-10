package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeGuest records what the API sends to a sandbox's guest agent.
type fakeGuest struct {
	mu       sync.Mutex
	sessions []agentproto.Session
	files    []agentproto.HomeFile
	started  []agentproto.StartSession
	killed   []string
	offline  bool
}

func (g *fakeGuest) Sessions(context.Context, string) ([]agentproto.Session, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.offline {
		return nil, agentchan.ErrNotConnected
	}
	return append([]agentproto.Session(nil), g.sessions...), nil
}

func (g *fakeGuest) KillSession(_ context.Context, _, name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.killed = append(g.killed, name)
	return nil
}

func (g *fakeGuest) WriteHomeFiles(_ context.Context, _ string, files []agentproto.HomeFile) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files = append(g.files, files...)
	return nil
}

func (g *fakeGuest) StartSession(_ context.Context, _ string, req agentproto.StartSession) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.started = append(g.started, req)
	g.sessions = append(g.sessions, agentproto.Session{Name: req.Name, Harness: req.Harness, Windows: 1})
	return nil
}

func TestSessionStart(t *testing.T) {
	h, s := newTestServer(t)
	g := &fakeGuest{sessions: []agentproto.Session{{Name: "main", Windows: 1}}}
	s.Guest = g
	env := newEnvironment(t, s, "work")
	prov := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/personas",
		`{"name":"Ada","soul":"Careful.","harness":"claude","providerId":"`+prov.ID+`","model":"claude-sonnet-4-5","gitName":"Ada L","gitEmail":"ada@example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("persona: %d %s", rec.Code, rec.Body)
	}
	ada := decode[store.Persona](t, rec)
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, PersonaID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/sessions"

	rec = do(h, "POST", base, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if got := decode[agentproto.Session](t, rec); got.Name != "claude" || got.Harness != "claude" {
		t.Fatalf("started: %+v", got)
	}
	if rec := do(h, "POST", base, ""); rec.Code != http.StatusCreated || decode[agentproto.Session](t, rec).Name != "claude-2" {
		t.Fatalf("second start: %d %s", rec.Code, rec.Body)
	}

	// The guest gets the files and the placeholder, never the key.
	sent, _ := json.Marshal(struct {
		F []agentproto.HomeFile
		S []agentproto.StartSession
	}{g.files, g.started})
	if strings.Contains(string(sent), providerKey) {
		t.Fatalf("the key reached the guest: %s", sent)
	}
	if g.started[0].Env["ANTHROPIC_API_KEY"] != prov.Placeholder || len(g.started[0].Env) != 1 ||
		strings.Join(g.started[0].Command, " ") != "claude --mcp-config /home/agent/.claude/studio-mcp.json" {
		t.Fatalf("start request: %+v", g.started[0])
	}
	paths := map[string]agentproto.HomeFile{}
	for _, f := range g.files {
		paths[f.Path] = f
	}
	if !paths[".claude/CLAUDE.md"].Block || !strings.Contains(paths[".claude/CLAUDE.md"].Content, "`remember`") ||
		!strings.Contains(paths[".claude/settings.json"].Content, `"claude-sonnet-4-5"`) ||
		!strings.Contains(paths[".config/git/config"].Content, `name = "Ada L"`) {
		t.Fatalf("files: %+v", paths)
	}

	// Only agent sessions are listed, and only they can be stopped here.
	list := decode[[]agentproto.Session](t, do(h, "GET", base, ""))
	if len(list) != 2 || list[0].Name != "claude" || list[1].Name != "claude-2" {
		t.Fatalf("list: %+v", list)
	}
	if rec := do(h, "DELETE", base+"/main", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("stop a plain terminal: %d", rec.Code)
	}
	if rec := do(h, "DELETE", base+"/claude-2", ""); rec.Code != http.StatusNoContent || len(g.killed) != 1 || g.killed[0] != "claude-2" {
		t.Fatalf("stop: %d %v", rec.Code, g.killed)
	}

	// A stopped sandbox has no guest to talk to.
	g.offline = true
	if rec := do(h, "POST", base, ""); rec.Code != http.StatusConflict {
		t.Fatalf("offline start: %d", rec.Code)
	}
}

func TestSessionStartRefused(t *testing.T) {
	h, s := newTestServer(t)
	g := &fakeGuest{}
	s.Guest = g
	env := newEnvironment(t, s, "work")
	ctx := context.Background()
	start := func(sb store.Sandbox) int {
		return do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes/"+sb.ID+"/sessions", "").Code
	}

	// No owner persona.
	plain, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "plain", CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if code := start(plain); code != http.StatusConflict {
		t.Fatalf("unowned: %d", code)
	}

	// A subscription provider that hasn't logged in.
	sub := createProvider(t, h, env, `{"name":"Claude Max","kind":"claude_subscription"}`)
	rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/personas", `{"name":"Eve","harness":"claude","providerId":"`+sub.ID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("persona: %d %s", rec.Code, rec.Body)
	}
	eve := decode[store.Persona](t, rec)
	eveSB, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "eve-dev", CPUs: 1, PersonaID: eve.ID})
	if err != nil {
		t.Fatal(err)
	}
	if code := start(eveSB); code != http.StatusConflict {
		t.Fatalf("login required: %d", code)
	}

	// A key that isn't bound to the provider's host.
	prov := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	rec = do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/personas", `{"name":"Ada","harness":"claude","providerId":"`+prov.ID+`","model":"claude-sonnet-4-5"}`)
	ada := decode[store.Persona](t, rec)
	adaSB, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, PersonaID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Vault.Update(ctx, env.ID, prov.SecretID, nil, []string{"evil.example.com"}, ""); err != nil {
		t.Fatal(err)
	}
	if code := start(adaSB); code != http.StatusConflict {
		t.Fatalf("unbound key: %d", code)
	}

	// An unknown sandbox.
	if code := start(store.Sandbox{ID: "nope"}); code != http.StatusNotFound {
		t.Fatalf("unknown: %d", code)
	}
	if len(g.files) != 0 || len(g.started) != 0 {
		t.Fatalf("refused starts reached the guest: %+v %+v", g.files, g.started)
	}
	if rec := do(h, "DELETE", testOrigin+"/api/environments/"+env.ID+"/sandboxes/"+adaSB.ID+"/sessions/a%20b", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid name: %d", rec.Code)
	}
}
