package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

func TestResizeSandboxEndpoint(t *testing.T) {
	h, s, env, sb, rt := checkpointAPI(t)
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/resources"
	rt.setStatus(sandboxes.VMName(sb), runtime.StatusStopped)

	if rec := do(h, http.MethodPut, base, `{"cpus":0,"memoryMiB":4096}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("zero CPUs: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPut, base, `{"cpus":2,"memoryMiB":256}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("memory below 512 MiB: %d %s", rec.Code, rec.Body)
	}

	rec := do(h, http.MethodPut, base, `{"cpus":3,"memoryMiB":4096}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resize: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"applied":{"cpus":"nextStart","memoryMiB":"nextStart"}`) {
		t.Fatalf("resize response does not say how each resource applied: %s", rec.Body)
	}
	resized := decode[sandboxes.Resized](t, rec)
	if resized.ID != sb.ID || resized.CPUs != 3 || resized.MemoryMiB != 4096 {
		t.Fatalf("resized sandbox: %+v", resized)
	}
	stored, err := s.Store.Sandbox(context.Background(), env.ID, sb.ID)
	if err != nil || stored.CPUs != 3 || stored.MemoryMiB != 4096 || stored.MaxMemoryMiB != 4096 {
		t.Fatalf("stored sandbox after resize = %+v, %v", stored, err)
	}

	if rec := do(h, http.MethodPut, testOrigin+"/api/environments/other/sandboxes/"+sb.ID+"/resources", `{"cpus":2,"memoryMiB":1024}`); rec.Code != http.StatusNotFound {
		t.Fatalf("resize from another environment: %d %s", rec.Code, rec.Body)
	}
}
