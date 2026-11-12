package gitreview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
)

func TestPullRequestChecksByNumberOrBranch(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	for _, args := range []string{`{"repository":"acme/web","number":42}`, `{"repository":"acme/web","branch":"feature/coupons"}`} {
		got, err := callReviewTool(t, svc.toolPullRequestChecks, sb, args)
		if err != nil {
			t.Fatal(err)
		}
		checks := got["checks"].([]any)
		first := checks[0].(map[string]any)
		if got["number"] != float64(42) || got["state"] != "failure" || got["head"] != "abc123" || got["forge"] != "forge" ||
			len(checks) != 2 || first["name"] != "ci/test" || first["state"] != "failure" || first["summary"] != "2 tests failed" ||
			checks[1].(map[string]any)["state"] != "success" {
			t.Fatalf("%s: %v", args, got)
		}
	}
	for args, want := range map[string]string{
		`{"repository":"acme/web"}`:                   "give the pull request's number, or its branch",
		`{"repository":"acme/web","branch":"nope"}`:   `from branch "nope"`,
		`{"repository":"acme/web","number":42,"x":1}`: "x",
	} {
		if _, err := callReviewTool(t, svc.toolPullRequestChecks, sb, args); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q", args, err, want)
		}
	}
	if _, err := callReviewTool(t, svc.toolPullRequestChecks, sb, `{"repository":"acme/web"}`); !integrations.IsToolError(err) {
		t.Fatalf("a missing number is the agent's to fix: %v", err)
	}
}

// A token without the Checks permission still reads the commit statuses, and says so.
func TestGitHubChecksWithoutCheckRuns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/v3") {
		case "/repos/acme/web/pulls/7":
			_, _ = w.Write([]byte(`{"head":{"sha":"head7"}}`))
		case "/repos/acme/web/commits/head7/status":
			_, _ = w.Write([]byte(`{"statuses":[{"context":"ci","state":"success"}]}`))
		default:
			http.Error(w, `{"message":"Resource not accessible by personal access token"}`, http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	g := &GitHub{BaseURL: srv.URL, Client: srv.Client()}
	out, err := g.PullRequestChecks(context.Background(), "ghs_x", "acme", "web", 7)
	if err != nil || out.State != CheckSuccess || len(out.Checks) != 1 || !strings.Contains(out.Note, "Checks permission") {
		t.Fatalf("%+v, %v", out, err)
	}
}

func TestSumChecks(t *testing.T) {
	for want, states := range map[string][]string{
		"none":    nil,
		"pending": {CheckFailure, CheckPending},
		"failure": {CheckSuccess, CheckCancelled},
		"success": {CheckSuccess, CheckSkipped, CheckNeutral},
	} {
		var checks []PRCheck
		for _, s := range states {
			checks = append(checks, PRCheck{State: s})
		}
		if got := sumChecks(checks); got != want {
			t.Errorf("%v: %s, want %s", states, got, want)
		}
	}
}

// GitHub: check runs and commit statuses, and an Actions job's log through its redirect, which
// goes to storage without the token.
func TestGitHubChecksAndAFailedJobsLog(t *testing.T) {
	var storageAuth []string
	mux := http.NewServeMux()
	send := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/repos/acme/web/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"head": map[string]any{"sha": "head7"}})
	})
	mux.HandleFunc("/repos/acme/web/commits/head7/check-runs", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"check_runs": []map[string]any{
			{"id": 31, "name": "test", "status": "completed", "conclusion": "failure", "html_url": "https://github.com/acme/web/actions/runs/1/job/31",
				"output": map[string]any{"title": "Process completed with exit code 1."}, "app": map[string]any{"slug": "github-actions"}},
			{"id": 32, "name": "build", "status": "in_progress", "app": map[string]any{"slug": "github-actions"}},
			{"id": 33, "name": "codecov", "status": "completed", "conclusion": "neutral", "app": map[string]any{"slug": "codecov"}},
		}})
	})
	mux.HandleFunc("/repos/acme/web/commits/head7/status", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"statuses": []map[string]any{{"context": "deploy/preview", "state": "error", "description": "no quota"}}})
	})
	mux.HandleFunc("/repos/acme/web/actions/jobs/31/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghs_checks123" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/storage/31.log", http.StatusFound)
	})
	mux.HandleFunc("/storage/31.log", func(w http.ResponseWriter, r *http.Request) {
		storageAuth = append(storageAuth, r.Header.Get("Authorization"))
		var b strings.Builder
		for i := range 100 {
			b.WriteString("2026-11-12T09:00:00.1234567Z line " + string(rune('a'+i%26)) + "\n")
		}
		b.WriteString("2026-11-12T09:00:01.0000000Z \x1b[31mFAIL\x1b[0m coupon_test.go:12 token ghs_checks123 leaked\n")
		_, _ = w.Write([]byte(b.String()))
	})
	// A GitHub Enterprise's API is under /api/v3; its log storage is elsewhere.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.Path, "/api/v3"); ok {
			r.URL.Path = rest
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	g := &GitHub{BaseURL: srv.URL, Client: client}

	ctx := context.Background()
	out, err := g.PullRequestChecks(ctx, "ghs_checks123", "acme", "web", 7)
	if err != nil {
		t.Fatal(err)
	}
	if out.Head != "head7" || out.State != CheckPending || len(out.Checks) != 4 {
		t.Fatalf("checks: %+v", out)
	}
	byName := map[string]PRCheck{}
	for _, c := range out.Checks {
		byName[c.Name] = c
	}
	if byName["test"].State != CheckFailure || byName["build"].State != CheckPending || byName["codecov"].State != CheckNeutral ||
		byName["deploy/preview"].State != CheckFailure || byName["test"].Summary != "Process completed with exit code 1." {
		t.Fatalf("states: %+v", byName)
	}
	svc := &Service{}
	svc.fillCheckLogs(ctx, g, "ghs_checks123", "acme", "web", out.Checks, nil)
	log := out.Checks[0].Log
	if out.Checks[0].Name != "test" || !strings.HasSuffix(log, "FAIL coupon_test.go:12 token [token] leaked") ||
		strings.Count(log, "\n") != checkLogLines-1 || strings.Contains(log, "2026-11-12T") {
		t.Fatalf("log:\n%s", log)
	}
	if len(storageAuth) != 1 || storageAuth[0] != "" {
		t.Fatalf("the storage got the token: %q", storageAuth)
	}
	if out.Checks[1].Log != "" || out.Checks[3].Log != "" {
		t.Fatal("only a failed Actions job has a log to read")
	}
}

func TestGitLabChecksAreTheHeadsPipelineJobs(t *testing.T) {
	g, _ := newFakeGitLab(t)
	ctx := context.Background()
	out, err := g.PullRequestChecks(ctx, "tok", "group/sub", "web", 7)
	if err != nil {
		t.Fatal(err)
	}
	if out.Head != "head2" || out.State != CheckFailure || len(out.Checks) != 3 {
		t.Fatalf("checks: %+v", out)
	}
	if c := out.Checks[0]; c.Name != "test / unit" || c.State != CheckFailure || c.Summary != "script failure" || c.logID != 11 {
		t.Fatalf("unit: %+v", c)
	}
	if c := out.Checks[1]; c.State != CheckNeutral || !strings.Contains(c.Summary, "allowed to fail") {
		t.Fatalf("allowed to fail: %+v", c)
	}
	if c := out.Checks[2]; c.State != CheckNeutral || c.Summary != "waits to be started by hand" {
		t.Fatalf("manual: %+v", c)
	}
	log, err := g.CheckLog(ctx, "tok", "group/sub", "web", out.Checks[0])
	if err != nil || log != "$ go test ./...\nFAIL x_test.go:3\nERROR: Job failed: exit code 1" {
		t.Fatalf("trace %q, %v", log, err)
	}
}

func TestLogTailCleansAndKeepsTheEnd(t *testing.T) {
	in := "section_start:1700000000:step_script\r\x1b[0K\x1b[32;1m$ make test\x1b[0;m\n" +
		"progress 10%\rprogress 50%\rprogress 100%\n" +
		"ok\r\n"
	if got := logTail(in); got != "$ make test\nprogress 100%\nok" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat(strings.Repeat("x", 300)+"\n", 30)
	if got := logTail(long); len(got) > checkLogBytes || strings.HasPrefix(got, "\n") || !strings.HasSuffix(got, "x") {
		t.Fatalf("cut: %d bytes", len(got))
	}
}
