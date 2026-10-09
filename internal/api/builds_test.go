package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

type apiBuildService struct {
	store        *store.Store
	submitErr    error
	prewarmName  string
	prewarmToken string
	baseRef      string
}

func (b *apiBuildService) Submit(ctx context.Context, envID, source string) (store.BuildJob, bool, error) {
	if b.submitErr != nil {
		return store.BuildJob{}, false, b.submitErr
	}
	hash := sha256.Sum256([]byte(source))
	baseRef := b.baseRef
	if baseRef == "" {
		baseRef = "test-base:latest"
	}
	job, created, err := b.store.CreateBuildJob(ctx, store.BuildJob{
		EnvironmentID: envID, RequestKey: "sha256:" + hex.EncodeToString(hash[:]),
		Source: source, Spec: `{"formatVersion":1}`, BaseRef: baseRef,
		TargetPlatform: "linux/amd64", ExporterVersion: "test",
	})
	job.PrewarmName, job.PrewarmToken = b.prewarmName, b.prewarmToken
	return job, created, err
}

func (b *apiBuildService) Cancel(ctx context.Context, envID, id string) (store.BuildJob, error) {
	job, _, err := b.store.CancelBuildJob(ctx, envID, id)
	return job, err
}

func buildPath(envID string) string {
	return testOrigin + "/api/environments/" + envID + "/builds"
}

func submitBuild(t *testing.T, h http.Handler, url, source string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"source": source})
	if err != nil {
		t.Fatal(err)
	}
	return do(h, http.MethodPost, url, string(body))
}

func newStoredBuild(t *testing.T, s *Server, env store.Environment, key, source string) store.BuildJob {
	t.Helper()
	hash := sha256.Sum256([]byte(key))
	job, _, err := s.Store.CreateBuildJob(context.Background(), store.BuildJob{
		EnvironmentID: env.ID, RequestKey: "sha256:" + hex.EncodeToString(hash[:]),
		Source: source, Spec: `{"formatVersion":1}`, BaseRef: "test-base:latest",
		TargetPlatform: "linux/amd64", ExporterVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestBuildSubmitIsAcceptedAndDeduplicatedWithoutInternalFields(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "builds")
	s.Builds = &apiBuildService{
		store: s.Store, prewarmName: "private-prewarm-name", prewarmToken: "private-prewarm-token",
		baseRef: "https://registry-user:registry-password@registry.example/base:latest",
	}
	url := buildPath(env.ID)
	source := "setup: echo hello"

	first := submitBuild(t, h, url, source)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit: got %d, want 202: %s", first.Code, first.Body)
	}
	created := decode[BuildJobView](t, first)
	if created.ID == "" || created.EnvironmentID != env.ID || created.Source != source || created.Status != store.BuildQueued {
		t.Fatalf("unexpected created job: %+v", created)
	}
	second := submitBuild(t, h, url, source)
	if second.Code != http.StatusAccepted {
		t.Fatalf("deduplicated submit: got %d, want 202: %s", second.Code, second.Body)
	}
	if got := decode[BuildJobView](t, second); got.ID != created.ID {
		t.Fatalf("deduplicated job ID = %q; want %q", got.ID, created.ID)
	}
	for _, internal := range []string{"private-prewarm-name", "private-prewarm-token", "registry-password", "baseRef", "sandboxId", "prewarmName", "prewarmToken"} {
		if strings.Contains(first.Body.String(), internal) {
			t.Errorf("submit response exposed %q: %s", internal, first.Body)
		}
	}
}

func TestBuildSubmitValidationAndMissingBackend(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "build validation")
	url := buildPath(env.ID)

	if rec := submitBuild(t, h, url, strings.Repeat("x", (64<<10)+1)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("oversized source: got %d, want 422 (%s)", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, url, `{"source":"setup: echo hi","host":"example.com","image":"evil"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unsupported host/image fields: got %d, want 422 (%s)", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, url, `{"source":""}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty source: got %d, want 422 (%s)", rec.Code, rec.Body)
	}
	if rec := submitBuild(t, h, buildPath("missing-environment"), "setup: echo hi"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown environment: got %d, want 404 (%s)", rec.Code, rec.Body)
	}
	if rec := submitBuild(t, h, url, "setup: echo hi"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("missing build backend: got %d, want 503 (%s)", rec.Code, rec.Body)
	}
}

func TestBuildInvalidSpecPreservesParserDiagnostic(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "invalid spec")
	s.Builds = &apiBuildService{
		store:     s.Store,
		submitErr: fmt.Errorf("line 3, field setup: unsupported expression: %w", templatespec.ErrInvalid),
	}
	rec := submitBuild(t, h, buildPath(env.ID), "setup: echo private-output")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed spec: got %d, want 422 (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "line 3, field setup: unsupported expression") {
		t.Fatalf("invalid-spec response omitted parser diagnostics: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "private-output") {
		t.Fatalf("invalid-spec response exposed setup output: %s", rec.Body)
	}
}

func TestBuildJobEnvironmentBoundaryAndScopedLog(t *testing.T) {
	h, s := newTestServer(t)
	owner := newEnvironment(t, s, "build owner")
	other := newEnvironment(t, s, "other environment")
	job := newStoredBuild(t, s, owner, "owner-job", "setup: echo hello")
	if err := s.Store.PutBuildLog(context.Background(), owner.ID, job.ID, "bounded log", true); err != nil {
		t.Fatal(err)
	}
	s.Builds = &apiBuildService{store: s.Store}

	ownerBase := buildPath(owner.ID)
	jobRec := do(h, http.MethodGet, ownerBase+"/"+job.ID, "")
	if jobRec.Code != http.StatusOK {
		t.Fatalf("owner job: got %d (%s)", jobRec.Code, jobRec.Body)
	}
	if got := decode[BuildJobView](t, jobRec); got.ID != job.ID || got.EnvironmentID != owner.ID || got.Source != job.Source {
		t.Fatalf("owner job response: %+v", got)
	}
	logRec := do(h, http.MethodGet, ownerBase+"/"+job.ID+"/log", "")
	if logRec.Code != http.StatusOK {
		t.Fatalf("owner log: got %d (%s)", logRec.Code, logRec.Body)
	}
	logBody := decode[BuildJobLog](t, logRec)
	if logBody.Text != "bounded log" || !logBody.Truncated {
		t.Fatalf("owner log response: %+v", logBody)
	}

	otherBase := buildPath(other.ID)
	for _, route := range []string{otherBase + "/" + job.ID, otherBase + "/" + job.ID + "/log"} {
		if rec := do(h, http.MethodGet, route, ""); rec.Code != http.StatusNotFound {
			t.Errorf("cross-environment read %s: got %d, want 404 (%s)", route, rec.Code, rec.Body)
		}
	}
	if rec := do(h, http.MethodPost, otherBase+"/"+job.ID+"/cancel", ""); rec.Code != http.StatusNotFound {
		t.Errorf("cross-environment cancel: got %d, want 404 (%s)", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, buildPath("missing-environment"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown environment list: got %d, want 404 (%s)", rec.Code, rec.Body)
	}
}

func TestBuildCancelIsIdempotentAndBackendRequired(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "cancel")
	job := newStoredBuild(t, s, env, "cancel-job", "setup: echo hello")
	url := buildPath(env.ID) + "/" + job.ID + "/cancel"
	if rec := do(h, http.MethodPost, url, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("cancel without backend: got %d, want 503 (%s)", rec.Code, rec.Body)
	}
	s.Builds = &apiBuildService{store: s.Store}
	for attempt := 0; attempt < 2; attempt++ {
		rec := do(h, http.MethodPost, url, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("cancel attempt %d: got %d, want 200 (%s)", attempt+1, rec.Code, rec.Body)
		}
		if got := decode[BuildJobView](t, rec); got.Status != store.BuildCancelled {
			t.Fatalf("cancel attempt %d returned status %q", attempt+1, got.Status)
		}
	}
}

func TestBuildListLimits(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "list limits")
	s.Builds = &apiBuildService{store: s.Store}
	for _, source := range []string{"setup: echo one", "setup: echo two", "setup: echo three"} {
		if rec := submitBuild(t, h, buildPath(env.ID), source); rec.Code != http.StatusAccepted {
			t.Fatalf("submit list fixture: got %d (%s)", rec.Code, rec.Body)
		}
	}
	for _, tc := range []struct {
		query string
		count int
		code  int
	}{
		{query: "", count: 3, code: http.StatusOK},
		{query: "?limit=1", count: 1, code: http.StatusOK},
		{query: "?limit=100", count: 3, code: http.StatusOK},
		{query: "?limit=0", code: http.StatusUnprocessableEntity},
		{query: "?limit=101", code: http.StatusUnprocessableEntity},
	} {
		rec := do(h, http.MethodGet, buildPath(env.ID)+tc.query, "")
		if rec.Code != tc.code {
			t.Errorf("list %s: got %d, want %d (%s)", tc.query, rec.Code, tc.code, rec.Body)
			continue
		}
		if tc.code == http.StatusOK {
			if got := decode[[]BuildJobSummary](t, rec); len(got) != tc.count {
				t.Errorf("list %s returned %d jobs; want %d", tc.query, len(got), tc.count)
			}
			if strings.Contains(rec.Body.String(), `"source"`) {
				t.Errorf("list %s returned source text: %s", tc.query, rec.Body)
			}
		}
	}
}

func TestBuildRuntimeOutputIsNotReturnedAsAPIError(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "sanitized error")
	s.Builds = &apiBuildService{store: s.Store, submitErr: errors.New("setup runtime output: private-runtime-output")}
	rec := submitBuild(t, h, buildPath(env.ID), "setup: echo private-runtime-output")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("submit failure: got %d, want 500 (%s)", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "private-runtime-output") {
		t.Fatalf("build error response leaked backend details: %s", rec.Body)
	}
}
