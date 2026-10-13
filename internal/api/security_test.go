package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The security level of an environment and of a teammate, as the API sets it.
func TestSecurityLevelAPI(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	url := testOrigin + "/api/environments/" + env.ID + "/security-level"

	if got := decode[[]store.Environment](t, do(h, "GET", testOrigin+"/api/environments", "")); len(got) != 1 || got[0].SecurityLevel != store.LevelGuarded {
		t.Fatalf("a new environment: %+v", got)
	}
	rec := do(h, "PUT", url, `{"level":"open"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set open: %d %s", rec.Code, rec.Body)
	}
	if got := decode[store.Environment](t, rec); got.SecurityLevel != store.LevelOpen || got.ID != env.ID {
		t.Fatalf("the environment after the change: %+v", got)
	}
	for _, body := range []string{`{"level":"paranoid"}`, `{"level":""}`, `{}`} {
		if rec := do(h, "PUT", url, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 422", body, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "PUT", testOrigin+"/api/environments/missing/security-level", `{"level":"open"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown environment: %d", rec.Code)
	}
	if got, _ := s.Store.Environment(context.Background(), env.ID); got.SecurityLevel != store.LevelOpen {
		t.Fatalf("a refused change was stored: %s", got.SecurityLevel)
	}
}

// A teammate's override: set, refused when unknown, and cleared with an empty level.
func TestTeammateSecurityLevelAPI(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	tm, err := s.Store.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	url := testOrigin + "/api/environments/" + env.ID + "/teammates/" + tm.ID + "/security-level"

	rec := do(h, "PUT", url, `{"level":"locked"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("override: %d %s", rec.Code, rec.Body)
	}
	if got := decode[store.Teammate](t, rec); got.SecurityLevel != store.LevelLocked {
		t.Fatalf("the teammate after the override: %+v", got)
	}
	if got := decode[store.Teammate](t, do(h, "GET", testOrigin+"/api/environments/"+env.ID+"/teammates/"+tm.ID, "")); got.SecurityLevel != store.LevelLocked {
		t.Fatalf("read back: %+v", got)
	}
	if rec := do(h, "PUT", url, `{"level":"paranoid"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown level: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", url, `{"level":""}`); rec.Code != http.StatusOK || decode[store.Teammate](t, rec).SecurityLevel != "" {
		t.Fatalf("clearing the override: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", testOrigin+"/api/environments/"+env.ID+"/teammates/missing/security-level", `{"level":"open"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown teammate: %d", rec.Code)
	}
}
