package api

import (
	"context"
	"encoding/json"
	"io"
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

func (v browserVMs) EnsureBrowser(ctx context.Context, env, teammate, _ string) (store.Sandbox, error) {
	return v.st.CreateBrowserSandbox(ctx, store.Sandbox{EnvironmentID: env, TeammateID: teammate, Name: "browser-" + teammate, CPUs: 1})
}
func (browserVMs) StopBrowser(context.Context, string, string) error { return nil }
func (v browserVMs) RemoveBrowser(ctx context.Context, env, teammate string) error {
	if sb, err := v.st.BrowserSandbox(ctx, env, teammate); err == nil {
		return v.st.DeleteSandbox(ctx, env, sb.ID)
	}
	return nil
}
func (v browserVMs) BrowserStatus(ctx context.Context, env, teammate string) (store.Sandbox, runtime.Status, error) {
	sb, err := v.st.BrowserSandbox(ctx, env, teammate)
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
func (noRunner) BrowserDownloads(context.Context, string, agentproto.BrowserDownloadsRequest) (agentproto.BrowserDownloadList, error) {
	return agentproto.BrowserDownloadList{}, net.ErrClosed
}
func (noRunner) BrowserDownload(context.Context, string, agentproto.BrowserDownloadsRequest, func(agentproto.BrowserDownload, io.Reader) error) error {
	return net.ErrClosed
}
func (noRunner) Ports(context.Context, string) ([]agentproto.Port, error) { return nil, net.ErrClosed }
func (noRunner) Forwards(context.Context, string, []int) error            { return net.ErrClosed }
func (noRunner) Upload(context.Context, string, agentproto.UploadRequest, io.Reader) (agentproto.UploadReply, error) {
	return agentproto.UploadReply{}, net.ErrClosed
}

func TestBrowserAPI(t *testing.T) {
	h, s := newTestServer(t)
	ctx := context.Background()
	env := newEnvironment(t, s, "work")
	if _, err := s.Store.CreateProvider(ctx, store.Provider{ID: "sub", EnvironmentID: env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	p, err := s.Store.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "ada", Harness: "claude", ProviderID: "sub", GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	s.Browser = &browser.Service{Store: s.Store, VMs: browserVMs{s.Store}, Runner: noRunner{}, Dir: t.TempDir()}
	s.Integrations = integrations.Set{s.Browser}
	base := "http://localhost:7878/api/environments/" + env.ID + "/teammates/" + p.ID + "/browser"

	if rec := do(h, "GET", base, ""); rec.Code != 200 || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", base+"/takeover", `{"on":true}`); rec.Code != 409 {
		t.Errorf("takeover of a browser that is not running: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/navigate", `{"url":"javascript:alert(1)"}`); rec.Code != 422 {
		t.Errorf("invalid navigation URL: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/navigate", `{"url":"https://example.org"}`); rec.Code != 409 {
		t.Errorf("navigation without takeover: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", base+"/downloads", ""); rec.Code != 200 || !json.Valid(rec.Body.Bytes()) {
		t.Errorf("downloads of a browser that is not running: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/downloads/send", `{"name":"a.pdf","sandboxId":"nope"}`); rec.Code != 404 {
		t.Errorf("send to an unknown sandbox: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/patterns", `{"verdict":"allow","action":"click","origin":"javascript:alert(1)"}`); rec.Code != 422 {
		t.Errorf("bad pattern: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "POST", base+"/patterns", `{"verdict":"sensitive","origin":"*","label":"Security*"}`)
	if rec.Code != 201 {
		t.Fatalf("pattern: %d %s", rec.Code, rec.Body)
	}
	pat := decode[store.BrowserPattern](t, rec)
	if pat.TeammateID != p.ID || pat.Action != "*" {
		t.Errorf("pattern %+v", pat)
	}

	// Allow for this origin from the approval stores a pattern.
	payload, _ := json.Marshal(browser.ActionPayload{TeammateID: p.ID, TeammateName: "ada", Action: "click", Origin: "https://example.com", Label: "Buy"})
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, Kind: browser.KindAction, Subject: "ada: click on https://example.com", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	rec = do(h, "GET", "http://localhost:7878/api/environments/"+env.ID+"/approvals?status=pending", "")
	views := decode[[]ApprovalView](t, rec)
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

	// Deleting the teammate removes its browser first.
	if _, err := s.Browser.VMs.EnsureBrowser(ctx, env.ID, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "DELETE", "http://localhost:7878/api/environments/"+env.ID+"/teammates/"+p.ID, ""); rec.Code != 204 {
		t.Fatalf("delete teammate: %d %s", rec.Code, rec.Body)
	}
	if _, err := s.Store.BrowserSandbox(ctx, env.ID, p.ID); err == nil {
		t.Error("the browser outlived its teammate")
	}
}
