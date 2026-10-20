package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// runGuest is a guest that also runs commands, answering each with result.
type runGuest struct {
	fakeGuest
	runMu  sync.Mutex
	runs   []agentproto.RunRequest
	result agentproto.RunResult
}

func (g *runGuest) Run(_ context.Context, _ string, req agentproto.RunRequest) (agentproto.RunResult, error) {
	g.runMu.Lock()
	defer g.runMu.Unlock()
	g.runs = append(g.runs, req)
	return g.result, nil
}

func TestForgeRepositoriesAndClone(t *testing.T) {
	h, s := newTestServer(t)
	s.Git = &gitreview.Service{Store: s.Store, Secrets: s.Vault, Bus: s.Bus, Dir: t.TempDir()}
	g := &runGuest{}
	s.Guest = g
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID
	folder := t.TempDir()
	for _, dir := range []string{"proj/.git", "notes"} {
		if err := os.MkdirAll(filepath.Join(folder, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	quoted, _ := json.Marshal(folder)
	rec := do(h, "POST", base+"/forges", `{"name":"here","kind":"local","baseUrl":`+string(quoted)+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("forge: %d %s", rec.Code, rec.Body)
	}
	local := decode[store.Forge](t, rec)

	rec = do(h, "GET", base+"/forges/"+local.ID+"/repositories", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if got := decode[[]gitreview.Repo](t, rec); !reflect.DeepEqual(got, []gitreview.Repo{{Path: "proj"}}) {
		t.Fatalf("repositories: %+v", got)
	}
	if rec := do(h, "GET", base+"/forges/nope/repositories", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown forge: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", base+"/forges", `{"name":"plain","kind":"https","baseUrl":"https://git.example.com","token":"tok-123"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("https forge: %d %s", rec.Code, rec.Body)
	}
	plain := decode[store.Forge](t, rec)
	if rec := do(h, "GET", base+"/forges/"+plain.ID+"/repositories", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an https forge's list: %d %s", rec.Code, rec.Body)
	}

	sb := newSandbox(t, s, env, "dev")
	clone := base + "/sandboxes/" + sb.ID + "/clone"
	rec = do(h, "POST", clone, `{"forgeId":"`+local.ID+`","repo":"proj"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clone: %d %s", rec.Code, rec.Body)
	}
	if got := decode[CloneResult](t, rec); got.Dir != "/workspace/proj" {
		t.Fatalf("clone result: %+v", got)
	}
	want := []string{"git", "clone", "--", "https://git.studio.internal/here/proj.git", "/workspace/proj"}
	if len(g.runs) != 1 || !reflect.DeepEqual(g.runs[0].Args, want) || g.runs[0].Env["GIT_TERMINAL_PROMPT"] != "0" || g.runs[0].TimeoutSec == 0 {
		t.Fatalf("runs: %+v", g.runs)
	}

	// Paths that would leave /workspace or read as an option are refused before the guest.
	for _, repo := range []string{"../etc", "-u/x", ".git", "a/../../b"} {
		if rec := do(h, "POST", clone, `{"forgeId":"`+local.ID+`","repo":"`+repo+`"}`); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d %s", repo, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", clone, `{"forgeId":"nope","repo":"proj"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown forge: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/sandboxes/nope/clone", `{"forgeId":"`+local.ID+`","repo":"proj"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown sandbox: %d %s", rec.Code, rec.Body)
	}
	if len(g.runs) != 1 {
		t.Fatalf("refused clones reached the guest: %+v", g.runs[1:])
	}

	g.result = agentproto.RunResult{ExitCode: 128, Stderr: "Cloning into '/workspace/proj'...\nfatal: destination path '/workspace/proj' already exists and is not an empty directory.\n"}
	rec = do(h, "POST", clone, `{"forgeId":"`+local.ID+`","repo":"proj"}`)
	if rec.Code != http.StatusConflict || !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("failed clone: %d %s", rec.Code, rec.Body)
	}
}
