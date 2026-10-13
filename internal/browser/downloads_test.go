package browser

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// callDownloadTool runs a download tool the way the hub routes a call: the handler of the
// integration that serves the method, with the sandbox the channel named.
func (f *fixture) callDownloadTool(sb store.Sandbox, method, params string) (any, error) {
	f.t.Helper()
	for _, tool := range f.svc.Tools() {
		if tool.Def.Name == method {
			return tool.Handle(f.ctx, sb, json.RawMessage(params))
		}
	}
	f.t.Fatalf("no tool %s", method)
	return nil, nil
}

// listed is the downloads of a browser_downloads answer.
func (f *fixture) listed(res any, err error) []Download {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
	return res.(map[string]any)["downloads"].([]Download)
}

// An agent lists the downloads of its teammate's browser and copies one into its workspace; the
// user copies one into any sandbox of the teammate from the Browser page.
func TestDownloadsListAndCopyToTheCallersWorkspace(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Start(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	f.fb.saveDownload(0, "report.pdf", "%PDF body", now.Add(-time.Hour))
	f.fb.saveDownload(0, "data.csv", "a,b", now.Add(-time.Minute))
	session := f.openEphemeral(f.sbAda, "https://example.com/")
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	slot := st.pageFor(session).slot
	f.svc.mu.Unlock()
	f.fb.saveDownload(slot, "temp.txt", "t", now)

	// The owner sees its ephemeral session's download first; another sandbox does not.
	list := f.listed(f.callDownloadTool(f.sbAda, agentproto.MethodBrowserDownloads, `{}`))
	if len(list) != 3 || list[0].Name != "temp.txt" || list[0].Session != session || list[2].Name != "report.pdf" {
		t.Fatalf("owner's list %+v", list)
	}
	other := f.listed(f.callDownloadTool(f.sbAda2, agentproto.MethodBrowserDownloads, `{}`))
	if len(other) != 2 {
		t.Fatalf("another sandbox's list %+v", other)
	}
	if _, err := f.callDownloadTool(f.sbAda, agentproto.MethodBrowserDownloads, `{"sandbox":"x"}`); !integrations.IsToolError(err) {
		t.Fatalf("an unknown argument: %v", err)
	}

	// A copy lands in downloads/ of the calling sandbox's workspace, under the download's name.
	res, err := f.callDownloadTool(f.sbAda, agentproto.MethodBrowserDownloadToWorkspace, `{"name":"report.pdf"}`)
	if err != nil || res.(map[string]any)["path"] != "downloads/report.pdf" {
		t.Fatalf("copy result %+v", res)
	}
	sent := f.fb.sent()
	if len(sent) != 1 || sent[0].sandbox != f.sbAda.ID || sent[0].body != "%PDF body" ||
		sent[0].req != (agentproto.UploadRequest{Dir: "downloads", Name: "report.pdf", Size: 9, Conflict: agentproto.ConflictKeep, CreateDir: true}) {
		t.Fatalf("uploads %+v", sent)
	}
	// Another sandbox cannot copy the owner's ephemeral download, and a sandbox without a
	// teammate has no browser at all.
	if _, err := f.callDownloadTool(f.sbAda2, agentproto.MethodBrowserDownloadToWorkspace, `{"name":"temp.txt","session":"`+session+`"}`); !integrations.IsToolError(err) {
		t.Fatalf("another sandbox copied an ephemeral download: %v", err)
	}
	if _, err := f.callDownloadTool(f.sbPlain, agentproto.MethodBrowserDownloads, `{}`); !integrations.IsToolError(err) || !strings.Contains(err.Error(), "no teammate") {
		t.Fatalf("sandbox without a teammate: %v", err)
	}
	if _, err := f.callDownloadTool(f.sbAda, agentproto.MethodBrowserDownloadToWorkspace, `{"name":"missing.zip"}`); err == nil || !strings.Contains(err.Error(), "could not copy missing.zip") {
		t.Fatalf("copy of a missing download: %v", err)
	}
	if _, err := f.callDownloadTool(f.sbAda, agentproto.MethodBrowserDownloadToWorkspace, `{}`); !integrations.IsToolError(err) {
		t.Fatalf("copy without a name: %v", err)
	}

	// The copy is in the action log.
	actions, err := f.svc.Actions(f.ctx, f.env, f.ada.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range actions {
		found = found || (a.Action == "send download" && a.Outcome == "ok" && a.Actor == "agent" && a.Target == "report.pdf")
	}
	if !found {
		t.Fatalf("no log entry for the copy: %+v", actions)
	}
}

// The user sees a teammate's downloads on the Browser page and sends one to any of the teammate's
// sandboxes; a sandbox of another teammate is not a target.
func TestUserSendsADownloadToASandbox(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Start(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	f.fb.saveDownload(0, "data.csv", "a,b", time.Now())
	list, err := f.svc.Downloads(f.ctx, f.env, f.ada.ID)
	if err != nil || len(list) != 1 || list[0].Name != "data.csv" || list[0].Size != 3 {
		t.Fatalf("downloads %+v, %v", list, err)
	}
	reply, err := f.svc.SendDownload(f.ctx, f.env, f.ada.ID, "", "data.csv", f.sbAda2.ID)
	if err != nil || reply.Name != "data.csv" {
		t.Fatalf("send: %+v, %v", reply, err)
	}
	if sent := f.fb.sent(); len(sent) != 1 || sent[0].sandbox != f.sbAda2.ID || sent[0].req.Dir != "downloads" {
		t.Fatalf("uploads %+v", sent)
	}
	if _, err := f.svc.SendDownload(f.ctx, f.env, f.ada.ID, "", "data.csv", f.sbBob.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("send to another teammate's sandbox: %v", err)
	}
	// A sandbox that refuses the copy keeps nothing; the failure is logged.
	f.fb.uploadErr = errors.New("the sandbox is not connected")
	if _, err := f.svc.SendDownload(f.ctx, f.env, f.ada.ID, "", "data.csv", f.sbAda.ID); err == nil {
		t.Fatal("a refused copy reported success")
	}
	if len(f.fb.sent()) != 1 {
		t.Fatal("a refused copy reached a sandbox")
	}
	// A browser that is stopped has no downloads to show or copy.
	if err := f.svc.Stop(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := f.svc.Downloads(f.ctx, f.env, f.ada.ID); err != nil || len(list) != 0 {
		t.Fatalf("downloads of a stopped browser: %+v, %v", list, err)
	}
	if _, err := f.svc.SendDownload(f.ctx, f.env, f.ada.ID, "", "data.csv", f.sbAda.ID); !errors.Is(err, ErrNotLive) {
		t.Fatalf("send from a stopped browser: %v", err)
	}
}
