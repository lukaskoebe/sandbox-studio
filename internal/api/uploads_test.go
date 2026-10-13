package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Upload is the guest's side of an upload, scripted: taken.txt is taken under the default
// policy and keeps a copy under keep. The bodies are recorded by path.
func (g *chatGuest) Upload(_ context.Context, _ string, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return agentproto.UploadReply{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.uploads = append(g.uploads, req)
	name := req.Name
	switch {
	case name == "taken.txt" && req.Conflict == "":
		return agentproto.UploadReply{}, agentchan.NameTakenError("taken.txt already exists")
	case name == "taken.txt" && req.Conflict == agentproto.ConflictKeep:
		name = "taken (2).txt"
	}
	if g.uploaded == nil {
		g.uploaded = map[string]string{}
	}
	g.uploaded[req.Dir+"/"+name] = string(data)
	return agentproto.UploadReply{Name: name, Size: int64(len(data))}, nil
}

func TestUploadFileAPI(t *testing.T) {
	h, s, g, env := newChatServer(t, &acptest.Agent{SessionID: "ses_upload"})
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "dev", CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	files := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/files"
	octet := []string{"Content-Type", "application/octet-stream"}

	rec := do(h, "POST", files+"?path=src&name=new.txt", "hello", octet...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if got := decode[UploadedFile](t, rec); got != (UploadedFile{Path: "src/new.txt", Name: "new.txt", Size: 5}) {
		t.Fatalf("reply %+v", got)
	}
	if g.uploaded["src/new.txt"] != "hello" || g.uploads[0] != (agentproto.UploadRequest{Dir: "src", Name: "new.txt", Size: 5}) {
		t.Fatalf("guest got %+v, %q", g.uploads, g.uploaded)
	}

	if rec := do(h, "POST", files+"?name=root.txt", "x", octet...); rec.Code != http.StatusCreated || decode[UploadedFile](t, rec).Path != "root.txt" {
		t.Fatalf("workspace root: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", files+"?name=taken.txt", "x", octet...); rec.Code != http.StatusConflict {
		t.Fatalf("taken name: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", files+"?name=taken.txt&conflict=keep", "x", octet...); rec.Code != http.StatusCreated || decode[UploadedFile](t, rec).Path != "taken (2).txt" {
		t.Fatalf("keep: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", files+"?name=a.txt", "x", "Content-Type", "text/plain"); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("media type: %d", rec.Code)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/"+env.ID+"/sandboxes/nope/files?name=a.txt", "x", octet...); rec.Code != http.StatusNotFound {
		t.Fatalf("missing sandbox: %d", rec.Code)
	}

	// Oversized files are refused from the declared length, before anything is read.
	req := httptest.NewRequest("POST", files+"?name=big.bin", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = agentproto.MaxUploadBytes + 1
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "100 MiB") {
		t.Fatalf("oversized: %d %s", rec.Code, rec.Body)
	}
	var problem map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("problem JSON: %v", err)
	}
}
