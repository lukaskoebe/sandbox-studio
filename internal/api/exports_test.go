package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

func exportBody(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	err := sandboxes.WriteExportHeader(&buf, sandboxes.ExportManifest{Format: sandboxes.ExportFormat, Name: "box",
		Resources: resources.Resources{CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	return buf.String() + "archive"
}

func TestSandboxImportRoutes(t *testing.T) {
	h, _, env, _, _ := checkpointAPI(t)
	base := testOrigin + "/api/environments/" + env.ID + "/sandbox-imports"
	octet := []string{"Content-Type", "application/octet-stream"}

	if rec := do(h, http.MethodPost, base, exportBody(t)); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("JSON upload: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, base, "not an export file at all, but long enough", octet...); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "not a Sandbox Studio export") {
		t.Fatalf("bad magic: %d %s", rec.Code, rec.Body)
	}
	cross := append(append([]string{}, octet...), "Origin", "http://evil.example")
	if rec := do(h, http.MethodPost, base, exportBody(t), cross...); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin upload: %d %s", rec.Code, rec.Body)
	}
	// The fake runtime cannot copy workspaces, which is reported before anything is stored.
	if rec := do(h, http.MethodPost, base, exportBody(t), octet...); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("valid upload: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, testOrigin+"/api/environments/nope/sandbox-imports", exportBody(t), octet...); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown environment: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, base+"/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown import: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, base+"/nope/cancel", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("cancel unknown import: %d %s", rec.Code, rec.Body)
	}
}

func TestSandboxExportRoute(t *testing.T) {
	h, _, env, sb, _ := checkpointAPI(t)
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/"
	if rec := do(h, http.MethodGet, base+"nope/export", ""); rec.Code != http.StatusNotFound ||
		rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unknown sandbox: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, http.MethodGet, base+sb.ID+"/export", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("export without transfer support: %d %s", rec.Code, rec.Body)
	}
}

func TestExportOperationsAreDocumented(t *testing.T) {
	doc := OpenAPI()
	for path, method := range map[string]string{
		"/api/environments/{env}/sandboxes/{id}/export":       http.MethodGet,
		"/api/environments/{env}/sandbox-imports":             http.MethodPost,
		"/api/environments/{env}/sandbox-imports/{id}":        http.MethodGet,
		"/api/environments/{env}/sandbox-imports/{id}/cancel": http.MethodPost,
	} {
		item := doc.Paths[path]
		if item == nil || (method == http.MethodGet && item.Get == nil) || (method == http.MethodPost && item.Post == nil) {
			t.Errorf("%s %s is not documented", method, path)
		}
	}
}
