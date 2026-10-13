package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
)

// A scope exports as a markdown file and imports into another environment; a file that does
// not check is refused.
func TestMemoryExchangeAPI(t *testing.T) {
	h, s := newMemoryServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "other")
	base := testOrigin + "/api/environments/" + env.ID + "/memory"
	if rec := do(h, "POST", base+"/facts", `{"scope":"shared","kind":"fact","text":"Use make ship"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create fact: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "GET", base+"/export?scope=shared", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	exp := decode[MemoryExportView](t, rec)
	if exp.Filename != "memory-shared-"+time.Now().Format(time.DateOnly)+".md" || !strings.Contains(exp.Markdown, "Use make ship") {
		t.Fatalf("export %+v", exp)
	}
	if rec := do(h, "GET", base+"/export?scope=bogus", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("export of an invalid scope: %d", rec.Code)
	}

	body, _ := json.Marshal(map[string]string{"scope": "shared", "markdown": exp.Markdown})
	otherBase := testOrigin + "/api/environments/" + other.ID + "/memory"
	rec = do(h, "POST", otherBase+"/import", string(body))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"facts":1`) {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if facts := decode[[]memory.Fact](t, do(h, "GET", otherBase+"/facts?scope=shared", "")); len(facts) != 1 || facts[0].Tier != memory.TierDocument {
		t.Fatalf("imported facts %+v", facts)
	}
	if rec := do(h, "POST", otherBase+"/import", string(body)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"skipped":1`) {
		t.Fatalf("second import: %d %s", rec.Code, rec.Body)
	}
	bad, _ := json.Marshal(map[string]string{"scope": "shared", "markdown": "not a memory file"})
	if rec := do(h, "POST", otherBase+"/import", string(bad)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad file: %d %s", rec.Code, rec.Body)
	}
}
