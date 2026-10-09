package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func createAPIOwnedBuildSandbox(t *testing.T, s *Server, env store.Environment) (store.BuildJob, store.Sandbox) {
	t.Helper()
	ctx := context.Background()
	job, _, err := s.Store.CreateBuildJob(ctx, store.BuildJob{
		EnvironmentID:   env.ID,
		RequestKey:      "request-" + store.NewID(),
		Source:          "name: private-build\n",
		Spec:            `{}`,
		BaseRef:         "ubuntu:latest",
		TargetPlatform:  "linux/amd64",
		ExporterVersion: "test-exporter",
	})
	if err != nil {
		t.Fatalf("CreateBuildJob: %v", err)
	}
	job, err = s.Store.ClaimBuildJob(ctx)
	if err != nil {
		t.Fatalf("ClaimBuildJob: %v", err)
	}
	job, err = s.Store.ResolveBuildJob(ctx, env.ID, job.ID, "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("a", 64), "linux/amd64")
	if err != nil {
		t.Fatalf("ResolveBuildJob: %v", err)
	}
	sb, err := s.Store.ReserveBuildSandbox(ctx, env.ID, job.ID, resources.Resources{
		CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024,
	})
	if err != nil {
		t.Fatalf("ReserveBuildSandbox: %v", err)
	}
	return job, sb
}

func TestBuilderPrivateAPIValidationAndUserApproval(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "builder-api")
	job, sb := createAPIOwnedBuildSandbox(t, s, env)
	if sb.BuildJobID != job.ID {
		t.Fatalf("sandbox owner = %q; want job %q", sb.BuildJobID, job.ID)
	}
	s.Auth = previewTestAuth(t, func(string) bool { return true })
	base := "http://localhost:7878/api/environments/" + url.PathEscape(env.ID) + "/sandboxes/" + url.PathEscape(sb.ID)

	previewPath := base + "/previews/3000/open"
	if rec := do(h, http.MethodPost, previewPath, ""); rec.Code != http.StatusNotFound {
		t.Errorf("open builder preview: got %d, want 404 (%s)", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, base+"/connections", ""); rec.Code != http.StatusNotFound {
		t.Errorf("list builder connections: got %d, want 404 (%s)", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "http://localhost:7878/api/environments/"+url.PathEscape(env.ID)+"/rules",
		`{"host":"example.com","action":"allow","sandboxId":"`+sb.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("create rule scoped to builder: got %d, want 422 (%s)", rec.Code, rec.Body)
	}
	existing, err := s.Store.CreateRule(context.Background(), store.Rule{
		EnvironmentID: env.ID, SandboxID: sb.ID, Host: "old.example", Action: store.ActionAllow,
	})
	if err != nil {
		t.Fatalf("create rule fixture: %v", err)
	}
	updatePath := "http://localhost:7878/api/environments/" + url.PathEscape(env.ID) + "/rules/" + url.PathEscape(existing.ID)
	if rec := do(h, http.MethodPut, updatePath,
		`{"host":"example.com","action":"allow","sandboxId":"`+sb.ID+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("update rule scoped to builder: got %d, want 422 (%s)", rec.Code, rec.Body)
	}

	type decisionResult struct {
		rule store.Rule
		err  error
	}
	held := make(chan decisionResult, 1)
	go func() {
		rule, err := s.Policy.Decide(context.Background(), policy.Request{
			EnvironmentID: env.ID,
			SandboxID:     sb.ID,
			SandboxName:   sb.Name,
			Host:          "registry.example",
			Port:          443,
		})
		held <- decisionResult{rule: rule, err: err}
	}()
	pending := awaitApproval(t, h, "registry.example", 443)
	decisionPath := "http://localhost:7878/api/environments/" + url.PathEscape(env.ID) + "/approvals/" + url.PathEscape(pending.ID) + "/decide"
	rec := do(h, http.MethodPost, decisionPath, `{"action":"allow","scope":"sandbox"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve builder network approval: got %d (%s)", rec.Code, rec.Body)
	}
	result := <-held
	if result.err != nil || result.rule.SandboxID != sb.ID || result.rule.Action != store.ActionAllow {
		t.Fatalf("builder network decision = %+v, %v", result.rule, result.err)
	}
}
