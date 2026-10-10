package api

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// browserVMs keeps browser rows in the store; nothing runs.
type browserVMs struct{ st *store.Store }

func (v browserVMs) EnsureBrowser(ctx context.Context, env, persona, _ string) (store.Sandbox, error) {
	return v.st.CreateBrowserSandbox(ctx, store.Sandbox{EnvironmentID: env, PersonaID: persona, Name: "browser-" + persona, CPUs: 1})
}
func (browserVMs) StopBrowser(context.Context, string, string) error { return nil }
func (v browserVMs) RemoveBrowser(ctx context.Context, env, persona string) error {
	if sb, err := v.st.BrowserSandbox(ctx, env, persona); err == nil {
		return v.st.DeleteSandbox(ctx, env, sb.ID)
	}
	return nil
}
func (v browserVMs) BrowserStatus(ctx context.Context, env, persona string) (store.Sandbox, runtime.Status, error) {
	sb, err := v.st.BrowserSandbox(ctx, env, persona)
	if err != nil {
		return sb, runtime.StatusAbsent, nil
	}
	return sb, runtime.StatusStopped, nil
}

type noRunner struct{}

func (noRunner) Browser(context.Context, string, agentproto.BrowserCommand) (agentproto.BrowserResult, error) {
	return agentproto.BrowserResult{}, net.ErrClosed
}
func (noRunner) DialTCP(context.Context, string, int) (net.Conn, error) { return nil, net.ErrClosed }

func TestBrowserAPI(t *testing.T) {
	h, s := newTestServer(t)
	ctx := context.Background()
	env := newEnvironment(t, s, "work")
	if _, err := s.Store.CreateProvider(ctx, store.Provider{ID: "sub", EnvironmentID: env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	p, err := s.Store.CreatePersona(ctx, store.Persona{EnvironmentID: env.ID, Name: "ada", Harness: "claude", ProviderID: "sub", GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	s.Browser = &browser.Service{Store: s.Store, VMs: browserVMs{s.Store}, Runner: noRunner{}, Dir: t.TempDir()}
	s.Integrations = integrations.Set{s.Browser}
	base := "http://localhost:7878/api/environments/" + env.ID + "/personas/" + p.ID + "/browser"

	if rec := do(h, "GET", base, ""); rec.Code != 200 || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", base+"/takeover", `{"on":true}`); rec.Code != 409 {
		t.Errorf("takeover of a browser that is not running: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/patterns", `{"verdict":"allow","action":"click","origin":"javascript:alert(1)"}`); rec.Code != 422 {
		t.Errorf("bad pattern: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "POST", base+"/patterns", `{"verdict":"sensitive","origin":"*","label":"Security*"}`)
	if rec.Code != 201 {
		t.Fatalf("pattern: %d %s", rec.Code, rec.Body)
	}
	var pat store.BrowserPattern
	json.Unmarshal(rec.Body.Bytes(), &pat)
	if pat.PersonaID != p.ID || pat.Action != "*" {
		t.Errorf("pattern %+v", pat)
	}

	// Allow for this origin from the approval stores a pattern.
	payload, _ := json.Marshal(browser.ActionPayload{PersonaID: p.ID, PersonaName: "ada", Action: "click", Origin: "https://example.com", Label: "Buy"})
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, Kind: browser.KindAction, Subject: "ada: click on https://example.com", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	rec = do(h, "GET", "http://localhost:7878/api/environments/"+env.ID+"/approvals?status=pending", "")
	var views []ApprovalView
	json.Unmarshal(rec.Body.Bytes(), &views)
	if len(views) != 1 || views[0].BrowserAction == nil || views[0].BrowserAction.Origin != "https://example.com" {
		t.Errorf("approval view: %s", rec.Body)
	}
	if rec := do(h, "POST", "http://localhost:7878/api/environments/"+env.ID+"/approvals/"+a.ID+"/decide", `{"action":"allow","remember":true}`); rec.Code != 200 {
		t.Fatalf("decide: %d %s", rec.Code, rec.Body)
	}
	pats, _ := s.Store.BrowserPatterns(ctx, env.ID, p.ID)
	if len(pats) != 2 {
		t.Errorf("patterns %+v", pats)
	}
	rec = do(h, "DELETE", base+"/patterns/"+pat.ID, "")
	if rec.Code != 204 {
		t.Errorf("delete pattern: %d", rec.Code)
	}

	// Deleting the persona removes its browser first.
	if _, err := s.Browser.VMs.EnsureBrowser(ctx, env.ID, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "DELETE", "http://localhost:7878/api/environments/"+env.ID+"/personas/"+p.ID, ""); rec.Code != 204 {
		t.Fatalf("delete persona: %d %s", rec.Code, rec.Body)
	}
	if _, err := s.Store.BrowserSandbox(ctx, env.ID, p.ID); err == nil {
		t.Error("the browser outlived its persona")
	}
}
