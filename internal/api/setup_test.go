package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestSetupConditions(t *testing.T) {
	h, s := newTestServer(t)
	ctx := context.Background()
	env := newEnvironment(t, s, "work")
	setDoctor := func(status doctor.Status) {
		s.System.Doctor = func(context.Context) doctor.Report { return doctor.Report{Status: status} }
	}
	steps := func(envID string) SetupSteps {
		t.Helper()
		rec := do(h, "GET", testOrigin+"/api/setup?env="+envID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("get setup: %d %s", rec.Code, rec.Body)
		}
		return decode[Setup](t, rec).Steps
	}

	setDoctor(doctor.OK)
	if got := steps(env.ID); got != (SetupSteps{Doctor: setupDone}) {
		t.Fatalf("host check passes: %+v", got)
	}
	setDoctor(doctor.Warn)
	if got := steps(env.ID); got != (SetupSteps{}) {
		t.Fatalf("host check warns: %+v; want nothing done", got)
	}
	setDoctor(doctor.OK)

	// A provider brings the default teammate with it.
	createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	if got := steps(env.ID); got != (SetupSteps{Doctor: setupDone, Provider: setupDone, Teammate: setupDone}) {
		t.Fatalf("after a provider: %+v", got)
	}
	if rec := do(h, "PUT", testOrigin+"/api/settings/ui", `{"agentView":"chat"}`); rec.Code != http.StatusOK {
		t.Fatalf("work mode: %d %s", rec.Code, rec.Body)
	}
	if got := steps(env.ID); got.WorkMode != setupDone {
		t.Fatalf("a chosen work mode: %+v", got)
	}

	if _, err := s.Store.CreateForge(ctx, store.Forge{ID: store.NewID(), EnvironmentID: env.ID, Name: "cb", Kind: "forgejo", BaseURL: "https://codeberg.org"},
		store.Secret{ID: store.NewID(), Name: "FORGE_CB_TOKEN", Sealed: []byte("x"), Hosts: []string{"codeberg.org"}}); err != nil {
		t.Fatal(err)
	}
	if got := steps(env.ID); got.Git != setupDone || got.Sandbox != "" {
		t.Fatalf("with a forge: %+v", got)
	}

	// Browser VMs are sandboxes too; they don't make the first sandbox.
	other := newEnvironment(t, s, "other")
	if _, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: other.ID, Name: "browser", Kind: store.SandboxKindBrowser}); err != nil {
		t.Fatal(err)
	}
	if got := steps(other.ID); got.Sandbox != "" {
		t.Fatalf("a browser VM counted as the first sandbox: %+v", got)
	}
	newSandbox(t, s, env, "first")
	if got := steps(env.ID); got.Sandbox != setupDone {
		t.Fatalf("with a sandbox: %+v", got)
	}
}

func TestSetupRecordsSteps(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	s.System.Doctor = func(context.Context) doctor.Report { return doctor.Report{Status: doctor.Fail} }
	const path = testOrigin + "/api/setup"
	query := "?env=" + env.ID

	if got := decode[Setup](t, do(h, "GET", path+query, "")); got.Dismissed || got.CompletedAt != nil || got.Steps != (SetupSteps{}) {
		t.Fatalf("nothing recorded yet: %+v", got)
	}
	for _, body := range []string{
		`{"steps":{"bogus":"done"},"dismissed":false}`,
		`{"steps":{"network":"later"},"dismissed":false}`,
		`{"steps":{},"dismissed":"no"}`,
		`{"steps":{}}`,
	} {
		if rec := do(h, "PUT", path+query, body); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s; want 422", body, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "GET", path, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("without an environment: %d %s; want 422", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", path+"?env=nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown environment: %d %s; want 404", rec.Code, rec.Body)
	}

	got := decode[Setup](t, do(h, "PUT", path+query, `{"steps":{"workMode":"skipped","network":"done"},"dismissed":false}`))
	if got.Steps != (SetupSteps{WorkMode: "skipped", Network: setupDone}) || got.CompletedAt != nil {
		t.Fatalf("partial: %+v", got)
	}
	if got := decode[Setup](t, do(h, "GET", path+query, "")); got.Steps.Network != setupDone || got.Steps.WorkMode != "skipped" {
		t.Fatalf("stored: %+v", got)
	}

	// Every step done or skipped completes the setup; a later update keeps the first time.
	all := `{"steps":{"doctor":"skipped","workMode":"done","provider":"skipped","teammate":"skipped","git":"skipped","network":"done","sandbox":"skipped"},"dismissed":false}`
	done := decode[Setup](t, do(h, "PUT", path+query, all))
	if done.CompletedAt == nil {
		t.Fatalf("all steps resolved, not completed: %+v", done)
	}
	again := decode[Setup](t, do(h, "PUT", path+query, `{"steps":{"network":"done"},"dismissed":false}`))
	if again.CompletedAt == nil || !again.CompletedAt.Equal(*done.CompletedAt) {
		t.Fatalf("completion changed: %v then %v", done.CompletedAt, again.CompletedAt)
	}

	if got := decode[Setup](t, do(h, "PUT", path+query, `{"steps":{},"dismissed":true}`)); !got.Dismissed {
		t.Fatalf("dismissed: %+v", got)
	}
}
