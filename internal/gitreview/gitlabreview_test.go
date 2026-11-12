package gitreview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGitLab answers the merge request endpoints of project group/sub/web, MR !7.
type fakeGitLab struct {
	mu     sync.Mutex
	posted []string         // method path of each write
	bodies []map[string]any // their bodies
	query  []string         // the merge request list's queries
	diffs  []gitlabDiffEntry
	me     string // the token's user; when set, /approve answers 401 as GitLab does for a second approval

	drafts        int // the token's user's pending draft comments
	publishStatus int // what /draft_notes/bulk_publish answers instead of succeeding
}

const gitlabMR = "/api/v4/projects/group%2Fsub%2Fweb/merge_requests/7"

func newFakeGitLab(t *testing.T) (*GitLab, *fakeGitLab) {
	f := &fakeGitLab{diffs: []gitlabDiffEntry{
		{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1,3 +1,4 @@\n package a\n-var x = 1\n+var x = 2\n+var y = 3\n func f() {}\n"},
		{OldPath: "new.go", NewPath: "new.go", NewFile: true, Diff: "@@ -0,0 +1,1 @@\n+package a\n"},
		{OldPath: "old.go", NewPath: "renamed.go", RenamedFile: true},
	}}
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/api/v4/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		if path == "/api/v4/user" {
			write(w, map[string]any{"username": f.me})
			return
		}
		if r.Method != http.MethodGet {
			if f.me != "" && strings.HasSuffix(path, "/approve") {
				w.WriteHeader(http.StatusUnauthorized)
				write(w, map[string]any{"message": "401 Unauthorized"})
				return
			}
			if f.publishStatus != 0 && strings.HasSuffix(path, "/bulk_publish") {
				w.WriteHeader(f.publishStatus)
				write(w, map[string]any{"message": "refused"})
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.posted = append(f.posted, r.Method+" "+strings.TrimPrefix(path, gitlabMR))
			f.bodies = append(f.bodies, body)
			f.mu.Unlock()
			write(w, map[string]any{"id": 99})
			return
		}
		switch strings.TrimPrefix(path, gitlabMR) {
		case "/draft_notes":
			write(w, make([]map[string]any, f.drafts))
		case "":
			write(w, map[string]any{"iid": 7, "title": "Fix x", "description": "Body", "state": "merged", "web_url": "https://gitlab.example/group/sub/web/-/merge_requests/7",
				"author": map[string]any{"username": "ada"}, "source_branch": "fix", "target_branch": "main", "sha": "head2",
				"updated_at": "2026-11-01T10:00:00Z", "diff_refs": map[string]any{"start_sha": "start", "base_sha": "base", "head_sha": "head2"}})
		case "/commits":
			write(w, []map[string]any{{"id": "head2", "title": "Second", "author_name": "Ada"}, {"id": "head1", "title": "First", "author_name": "Ada"}})
		case "/diffs":
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("X-Next-Page", "2")
				write(w, f.diffs[:2])
				return
			}
			write(w, f.diffs[2:])
		case "/notes":
			write(w, []map[string]any{
				{"body": "approved this merge request", "system": true, "author": map[string]any{"username": "bob"}, "created_at": "2026-11-01T09:00:00Z"},
				{"body": "added 1 commit", "system": true, "author": map[string]any{"username": "ada"}},
				{"body": "Why 2?", "type": "DiffNote", "author": map[string]any{"username": "bob"}, "position": map[string]any{"new_path": "a.go", "old_path": "a.go", "new_line": 2}},
				{"body": "Gone", "type": "DiffNote", "author": map[string]any{"username": "bob"}, "position": map[string]any{"new_path": "a.go", "old_path": "a.go", "old_line": 2}},
				{"body": "Thanks!", "author": map[string]any{"username": "cy"}},
			})
		case "/pipelines":
			write(w, []map[string]any{{"id": 6, "sha": "head3"}, {"id": 5, "sha": "head2"}, {"id": 4, "sha": "head2"}})
		case "/approvals":
			write(w, map[string]any{"approved_by": []map[string]any{{"user": map[string]any{"username": "bob"}}, {"user": map[string]any{"username": "dee"}}}})
		case "/versions":
			write(w, []map[string]any{{"id": 2, "head_commit_sha": "head2", "base_commit_sha": "base", "start_commit_sha": "start"},
				{"id": 1, "head_commit_sha": "head1", "base_commit_sha": "base", "start_commit_sha": "start0"}})
		case "/versions/1", "/versions/2":
			write(w, map[string]any{"diffs": f.diffs})
		default:
			if path == "/api/v4/projects/group%2Fsub%2Fweb/pipelines/5/jobs" {
				write(w, []map[string]any{
					{"id": 11, "name": "unit", "stage": "test", "status": "failed", "failure_reason": "script_failure", "web_url": "https://gitlab.example/j/11"},
					{"id": 12, "name": "flaky", "stage": "test", "status": "failed", "allow_failure": true},
					{"id": 13, "name": "deploy", "stage": "deploy", "status": "manual"},
				})
				return
			}
			if path == "/api/v4/projects/group%2Fsub%2Fweb/jobs/11/trace" {
				_, _ = w.Write([]byte("\x1b[0Ksection_start:1:step_script\r\x1b[0K$ go test ./...\nFAIL x_test.go:3\n\x1b[31;1mERROR: Job failed: exit code 1\x1b[0;m\n"))
				return
			}
			if path == "/api/v4/projects/group%2Fsub%2Fweb/merge_requests" {
				f.mu.Lock()
				f.query = append(f.query, r.URL.RawQuery)
				f.mu.Unlock()
				write(w, []map[string]any{
					{"iid": 9, "title": "Open", "state": "opened", "draft": true, "author": map[string]any{"username": "ada"}, "source_branch": "b", "target_branch": "main",
						"reviewers": []map[string]any{{"username": "bob"}}, "labels": []string{"bug"}},
					{"iid": 8, "title": "Merged", "state": "merged"},
					{"iid": 6, "title": "Closed", "state": "closed"},
				})
				return
			}
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &GitLab{BaseURL: srv.URL, Client: srv.Client()}, f
}

func TestGitLabReadsAMergeRequestAsAPullRequest(t *testing.T) {
	g, _ := newFakeGitLab(t)
	info, err := g.ReadPullRequest(context.Background(), "t", "group/sub", "web", 7)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "closed" || !info.Merged || info.Author != "ada" || info.Head != (PRBranch{"fix", "head2"}) || info.Base != (PRBranch{"main", "start"}) {
		t.Fatalf("%+v", info)
	}
	if len(info.Commits) != 2 || info.Commits[0].SHA != "head1" || info.Commits[1].Subject != "Second" {
		t.Errorf("commits oldest first: %+v", info.Commits)
	}
	if len(info.Files) != 3 || info.Files[0] != (PRFile{"a.go", "modified", 2, 1}) || info.Files[1].Status != "added" || info.Files[2].Status != "renamed" {
		t.Errorf("files %+v", info.Files)
	}
	if len(info.Reviews) != 2 || info.Reviews[0].Author != "bob" || info.Reviews[0].When.IsZero() || info.Reviews[1].Author != "dee" {
		t.Errorf("reviews %+v", info.Reviews)
	}
	if len(info.LineComments) != 2 || info.LineComments[0].Side != "new" || info.LineComments[0].Line != 2 || info.LineComments[1].Side != "old" {
		t.Errorf("line comments %+v", info.LineComments)
	}
	if len(info.Comments) != 1 || info.Comments[0].Body != "Thanks!" {
		t.Errorf("comments %+v", info.Comments)
	}
}

func TestGitLabDiffIsUnified(t *testing.T) {
	g, _ := newFakeGitLab(t)
	diff, truncated, err := g.PullRequestDiff(context.Background(), "t", "group/sub", "web", 7, 1<<20)
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	for _, want := range []string{"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,4 @@", "--- /dev/null\n+++ b/new.go", "diff --git a/old.go b/renamed.go\n"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, diff)
		}
	}
	lines := parseDiffLines(diff)
	if !lines.has(ReviewComment{Path: "a.go", Line: 3, Side: "new"}) || !lines.has(ReviewComment{Path: "a.go", Line: 2, Side: "old"}) || !lines.has(ReviewComment{Path: "new.go", Line: 1}) {
		t.Errorf("lines %+v", lines)
	}
	cut, truncated, err := g.PullRequestDiff(context.Background(), "t", "group/sub", "web", 7, 60)
	if err != nil || !truncated || len(cut) > 60 || !strings.HasSuffix(cut, "\n") {
		t.Errorf("cut %q, %v, %v", cut, truncated, err)
	}
}

func TestGitLabListsMergeRequests(t *testing.T) {
	g, f := newFakeGitLab(t)
	ctx := context.Background()
	open, err := g.ListPullRequests(ctx, "t", "group/sub", "web", "open", 30)
	if err != nil || len(open) != 3 || open[0].Number != 9 || !open[0].Draft || open[0].Reviewers[0] != "bob" || open[0].Labels[0] != "bug" || open[0].Head != "b" {
		t.Fatalf("%+v %v", open, err)
	}
	closed, err := g.ListPullRequests(ctx, "t", "group/sub", "web", "closed", 30)
	if err != nil || len(closed) != 2 || !closed[0].Merged || closed[1].Merged {
		t.Fatalf("closed keeps merged and closed: %+v %v", closed, err)
	}
	if !strings.Contains(f.query[0], "state=opened") || !strings.Contains(f.query[0], "order_by=updated_at") || !strings.Contains(f.query[1], "state=all") {
		t.Errorf("queries %v", f.query)
	}
}

func TestGitLabPostsAReviewAsApprovalDiscussionsAndANote(t *testing.T) {
	g, f := newFakeGitLab(t)
	url, err := g.PostReview(context.Background(), "t", "group/sub", "web", 7, ReviewPost{Verdict: VerdictApprove, Body: "Nice", CommitID: "head1",
		Comments: []ReviewComment{
			{Path: "a.go", Line: 1, Side: "new", Body: "kept"},
			{Path: "a.go", Line: 3, Side: "new", Body: "added"},
			{Path: "a.go", Line: 2, Side: "old", Body: "removed"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /approve", "POST /discussions", "POST /discussions", "POST /discussions", "POST /notes"}
	if fmt.Sprint(f.posted) != fmt.Sprint(want) {
		t.Fatalf("posted %v", f.posted)
	}
	if f.bodies[0]["sha"] != "head1" {
		t.Errorf("approval %v", f.bodies[0])
	}
	pos := func(i int) map[string]any { return f.bodies[i]["position"].(map[string]any) }
	// The draft was written against version 1, whose start commit is start0.
	if p := pos(1); p["new_line"] != float64(1) || p["old_line"] != float64(1) || p["head_sha"] != "head1" || p["start_sha"] != "start0" || p["position_type"] != "text" {
		t.Errorf("a kept line has both numbers: %v", p)
	}
	if p := pos(2); p["new_line"] != float64(3) || p["old_line"] != nil {
		t.Errorf("an added line has only its new number: %v", p)
	}
	if p := pos(3); p["old_line"] != float64(2) || p["new_line"] != nil {
		t.Errorf("a removed line has only its old number: %v", p)
	}
	if f.bodies[4]["body"] != "Nice" || url != "https://gitlab.example/group/sub/web/-/merge_requests/7#note_99" {
		t.Errorf("note %v, url %s", f.bodies[4], url)
	}
}

func TestGitLabRequestsChangesInANote(t *testing.T) {
	g, f := newFakeGitLab(t)
	if _, err := g.PostReview(context.Background(), "t", "group/sub", "web", 7, ReviewPost{Verdict: VerdictRequestChanges, Body: "Fix x"}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.posted) != "[POST /draft_notes/bulk_publish POST /notes]" || f.bodies[0]["reviewer_state"] != "requested_changes" ||
		f.bodies[1]["body"] != "**Changes requested.**\n\nFix x" {
		t.Fatalf("%v %v", f.posted, f.bodies)
	}
}

// Publishing the state would publish the user's pending draft comments too, so nothing is posted.
func TestGitLabRequestingChangesLeavesTheUsersDraftsAlone(t *testing.T) {
	g, f := newFakeGitLab(t)
	f.drafts = 2
	_, err := g.PostReview(context.Background(), "t", "group/sub", "web", 7, ReviewPost{Verdict: VerdictRequestChanges, Body: "Fix x"})
	if err == nil || !strings.Contains(err.Error(), "2 pending draft comments") {
		t.Fatalf("err %v", err)
	}
	if len(f.posted) != 0 {
		t.Fatalf("posted %v", f.posted)
	}
}

// A GitLab without the endpoint has the note that says so; one that refuses posts nothing, so
// that posting again doesn't repeat the comments.
func TestGitLabRequestingChangesWithoutTheState(t *testing.T) {
	g, f := newFakeGitLab(t)
	f.publishStatus = http.StatusNotFound
	if url, err := g.PostReview(context.Background(), "t", "group/sub", "web", 7, ReviewPost{Verdict: VerdictRequestChanges, Body: "Fix x"}); err != nil || url == "" {
		t.Fatalf("url %q, err %v", url, err)
	}
	f.posted = nil
	f.publishStatus = http.StatusForbidden
	_, err := g.PostReview(context.Background(), "t", "group/sub", "web", 7, ReviewPost{Verdict: VerdictRequestChanges, Body: "Fix x"})
	if err == nil || !strings.Contains(err.Error(), "didn't record") || len(f.posted) != 0 {
		t.Fatalf("posted %v, err %v", f.posted, err)
	}
}

// GitLab refuses a second approval by the same user; the review posts on, since the approval
// stands, while a refused approval by anyone else fails it.
func TestGitLabApprovesAgain(t *testing.T) {
	g, f := newFakeGitLab(t)
	f.me = "bob"
	if _, err := g.PostReview(context.Background(), "t", "group/sub", "web", 7, ReviewPost{Verdict: VerdictApprove, Body: "Still nice"}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.posted) != "[POST /notes]" {
		t.Fatalf("posted %v", f.posted)
	}
	f.me = "eve"
	if _, err := g.PostReview(context.Background(), "t", "group/sub", "web", 7, ReviewPost{Verdict: VerdictApprove, Body: "x"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
}
