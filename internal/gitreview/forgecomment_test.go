package gitreview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestACommentWaitsForApprovalAndPostsTheEdit(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	ctx := context.Background()
	if _, err := callReviewTool(t, svc.toolPullRequestComment, sb, `{"repository":"acme/web","number":42,"body":"  "}`); !integrations.IsToolError(err) {
		t.Fatalf("an empty comment: %v", err)
	}
	args := `{"repository":"acme/web","number":42,"body":"The tests fail on main too.","idempotency_key":"c1"}`
	got, err := callReviewTool(t, svc.toolPullRequestComment, sb, args)
	if err != nil || got["status"] != "awaiting approval" || got["approval"] == "" {
		t.Fatalf("comment: %v %v", got, err)
	}
	// Asked again with its key, it answers with the same approval.
	if again, _ := callReviewTool(t, svc.toolPullRequestComment, sb, args); again["approval"] != got["approval"] {
		t.Fatalf("again: %v", again)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if len(pending) != 1 || pending[0].Kind != KindComment {
		t.Fatalf("pending: %v", pending)
	}
	a := pending[0]
	d, err := CommentDetail(a)
	if err != nil || d.Title != "Apply coupon codes" || d.Account != "lukas" || d.ForgeHost == "" {
		t.Fatalf("detail: %+v %v", d, err)
	}
	if res, settled, _ := svc.commentOutcome(ctx, a); settled || res != nil {
		t.Fatalf("outcome while pending: %v", res)
	}
	edit := &integrations.CommentEdit{Body: "The tests fail on main too; see #40.", Attribution: true}
	if err := svc.decideComment(ctx, a, integrations.Decision{Action: integrations.Allow, Comment: edit}); err != nil {
		t.Fatal(err)
	}
	if len(ff.comments) != 1 || ff.comments[0] != "The tests fail on main too; see #40.\n\n— Comment by an agent in webshop" {
		t.Fatalf("posted: %q", ff.comments)
	}
	a, _ = st.Approval(ctx, sb.EnvironmentID, a.ID)
	res, settled, err := svc.commentOutcome(ctx, a)
	if err != nil || !settled || res["status"] != "posted" || res["url"] != "https://forge.example/acme/web/pulls/42#issuecomment-9" {
		t.Fatalf("outcome: %v %v %v", res, settled, err)
	}
	if again, _ := callReviewTool(t, svc.toolPullRequestComment, sb, args); again["status"] != "posted" {
		t.Fatalf("again once posted: %v", again)
	}
}

func TestADiscardedCommentIsNotPosted(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	ctx := context.Background()
	if _, err := callReviewTool(t, svc.toolPullRequestComment, sb, `{"repository":"acme/web","number":42,"body":"Ping"}`); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if err := svc.decideComment(ctx, pending[0], integrations.Decision{Action: integrations.Deny, Note: "Not needed"}); err != nil {
		t.Fatal(err)
	}
	if len(ff.comments) != 0 {
		t.Fatalf("a discarded comment was posted: %q", ff.comments)
	}
	a, _ := st.Approval(ctx, sb.EnvironmentID, pending[0].ID)
	res, settled, _ := svc.commentOutcome(ctx, a)
	if !settled || res["status"] != "discarded" || !strings.Contains(res["note"].(string), "Not needed") {
		t.Fatalf("outcome: %v", res)
	}
}

func TestPostCommentOnGitHubAndGitLab(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, r.Method+" "+r.URL.EscapedPath()+" "+body["body"])
		switch r.URL.EscapedPath() {
		case "/api/v3/repos/acme/web/issues/7/comments":
			_, _ = w.Write([]byte(`{"html_url":"https://github.example/acme/web/pull/7#issuecomment-1"}`))
		case "/api/v4/projects/group%2Fweb/merge_requests/7/notes":
			_, _ = w.Write([]byte(`{"id":5}`))
		case "/api/v4/projects/group%2Fweb/merge_requests/7":
			_, _ = w.Write([]byte(`{"web_url":"https://gitlab.example/group/web/-/merge_requests/7"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	u, err := (&GitHub{BaseURL: srv.URL, Client: srv.Client()}).PostComment(ctx, "t", "acme", "web", 7, "Hi")
	if err != nil || u != "https://github.example/acme/web/pull/7#issuecomment-1" {
		t.Fatalf("github: %q %v", u, err)
	}
	u, err = (&GitLab{BaseURL: srv.URL, Client: srv.Client()}).PostComment(ctx, "t", "group", "web", 7, "Hi")
	if err != nil || u != "https://gitlab.example/group/web/-/merge_requests/7#note_5" {
		t.Fatalf("gitlab: %q %v %v", u, err, got)
	}
}
