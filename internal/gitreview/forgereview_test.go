package gitreview

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const prDiff = `diff --git a/web/coupon.ts b/web/coupon.ts
--- a/web/coupon.ts
+++ b/web/coupon.ts
@@ -20,4 +20,5 @@ export async function applyCoupon(cart: Cart, code: string) {
 export async function applyCoupon(cart: Cart, code: string) {
-  const total = await finalTotal(cart)
+  const total = cart.subtotal
+  const coupon = await validateCoupon(code, total)
   if (!coupon) throw new CouponError("unknown code")
 }
`

// reviewForge is a Forgejo stand-in for the pull request API: pull request 42 of acme/web,
// opened by author, and the reviews posted to it.
type reviewForge struct {
	srv    *httptest.Server
	author string
	head   string
	mu     sync.Mutex
	posted []map[string]any
}

func newReviewForge(t *testing.T) *reviewForge {
	f := &reviewForge{author: "someone", head: "abc123"}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token "+forgeToken {
			http.Error(w, `{"message":"bad token"}`, http.StatusUnauthorized)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/v1")
		send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch {
		case p == "/user":
			send(map[string]string{"login": "lukas"})
		case p == "/repos/acme/web/pulls/42" && r.Method == http.MethodGet:
			f.mu.Lock()
			send(map[string]any{"number": 42, "title": "Apply coupon codes", "state": "open", "html_url": "https://forge.example/acme/web/pulls/42",
				"user": map[string]string{"login": f.author}, "base": map[string]string{"ref": "main", "sha": "base1"},
				"head": map[string]string{"ref": "feature/coupons", "sha": f.head}})
			f.mu.Unlock()
		case p == "/repos/acme/web/pulls/42.diff":
			_, _ = io.WriteString(w, prDiff)
		case p == "/repos/acme/web/pulls/42/commits":
			commits := []map[string]any{{"sha": "abc123", "commit": map[string]any{"message": "Apply coupons\n\nbody", "author": map[string]string{"name": "Ada"}}}}
			f.mu.Lock()
			switch f.head {
			case "def456":
				commits = append(commits, map[string]any{"sha": "def456", "commit": map[string]any{"message": "Round the total"}})
			case "fff000":
				commits = []map[string]any{{"sha": "fff000", "commit": map[string]any{"message": "Apply coupons, squashed"}}}
			}
			f.mu.Unlock()
			send(commits)
		case p == "/repos/acme/web/pulls/42/files":
			send([]map[string]any{{"filename": "web/coupon.ts", "status": "modified", "additions": 2, "deletions": 1}})
		case p == "/repos/acme/web/pulls/42/reviews" && r.Method == http.MethodGet:
			send([]any{})
		case p == "/repos/acme/web/pulls/42/reviews" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.posted = append(f.posted, body)
			f.mu.Unlock()
			send(map[string]any{"id": 7, "html_url": "https://forge.example/acme/web/pulls/42#review-7"})
		case p == "/repos/acme/web/issues/42/comments":
			send([]map[string]any{{"user": map[string]string{"login": "bob"}, "body": "LGTM?"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newReviewHarness(t *testing.T) (*Service, *store.Store, store.Sandbox, *reviewForge) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	ff := newReviewForge(t)
	sec := store.Secret{ID: store.NewID(), Name: "FORGE_TOKEN", Sealed: []byte("sealed"), Hosts: []string{"127.0.0.1"}}
	f, err := st.CreateForge(ctx, store.Forge{ID: store.NewID(), EnvironmentID: env.ID, Name: "forge", Kind: KindForgejo, BaseURL: ff.srv.URL}, sec)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "webshop"})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, Secrets: fakeSecrets{env.ID + "/" + f.SecretID: forgeToken}, Dir: t.TempDir(), Client: ff.srv.Client()}
	svc.httpClient()
	return svc, st, sb, ff
}

func callReviewTool(t *testing.T, handle func(context.Context, store.Sandbox, json.RawMessage) (any, error), sb store.Sandbox, args string) (map[string]any, error) {
	t.Helper()
	out, err := handle(context.Background(), sb, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(out)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m, nil
}

func TestPullRequestReadAnswersWithoutTheDiff(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	got, err := callReviewTool(t, svc.toolPullRequestRead, sb, `{"repository":"acme/web","number":42}`)
	if err != nil {
		t.Fatal(err)
	}
	if got["title"] != "Apply coupon codes" || got["author"] != "someone" {
		t.Fatalf("read: %v", got)
	}
	commits := got["commits"].([]any)
	if len(commits) != 1 || commits[0].(map[string]any)["subject"] != "Apply coupons" {
		t.Fatalf("commits: %v", commits)
	}
	if files := got["files"].([]any); len(files) != 1 || got["diff"] != nil {
		t.Fatalf("files %v, diff %v", files, got["diff"])
	}
	if c := got["comments"].([]any); len(c) != 1 {
		t.Fatalf("comments: %v", c)
	}
}

func TestReviewDraftWaitsForApprovalAndPostsTheEdits(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	ctx := context.Background()
	// A line outside the diff is refused before the card exists.
	_, err := callReviewTool(t, svc.toolPullRequestReview, sb, `{"repository":"acme/web","number":42,"verdict":"request_changes","summary":"x",
		"comments":[{"path":"web/coupon.ts","line":90,"body":"here"}]}`)
	if !integrations.IsToolError(err) || !strings.Contains(err.Error(), "not in the pull request's diff") {
		t.Fatalf("outside the diff: %v", err)
	}
	draft := `{"repository":"acme/web","number":42,"verdict":"request_changes","summary":"Validate against the final total.",
		"comments":[{"path":"web/coupon.ts","line":21,"body":"Use finalTotal(cart)"},{"path":"web/coupon.ts","line":21,"side":"old","body":"This was right"}]}`
	got, err := callReviewTool(t, svc.toolPullRequestReview, sb, draft)
	if err != nil || got["status"] != "awaiting approval" {
		t.Fatalf("draft: %v %v", got, err)
	}
	// A second draft replaces the first.
	if _, err := callReviewTool(t, svc.toolPullRequestReview, sb, draft); err != nil {
		t.Fatal(err)
	}
	pending, err := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if err != nil || len(pending) != 1 || pending[0].Kind != KindReview {
		t.Fatalf("pending: %v %v", pending, err)
	}
	a := pending[0]
	detail, err := svc.ReviewDetail(ctx, a)
	if err != nil || detail.Draft.Account != "lukas" || detail.Draft.Own || len(detail.Draft.Comments) != 2 || detail.Review.HeadSHA != "abc123" {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	status, _ := callReviewTool(t, svc.toolReviewStatus, sb, `{"repository":"acme/web","number":42}`)
	if status["status"] != "awaiting approval" {
		t.Fatalf("status: %v", status)
	}

	// The user drops a comment, rewords the summary and keeps the attribution line.
	edit := &integrations.ReviewEdit{Verdict: VerdictRequestChanges, Summary: "Check the final total.", Attribution: true,
		Comments: []integrations.ReviewEditComment{{Path: "web/coupon.ts", Line: 21, Body: "Use finalTotal(cart)"}}}
	if err := svc.decideReview(ctx, a, integrations.Decision{Action: integrations.Allow, Review: edit}); err != nil {
		t.Fatal(err)
	}
	if len(ff.posted) != 1 {
		t.Fatalf("posted: %v", ff.posted)
	}
	p := ff.posted[0]
	if p["event"] != "REQUEST_CHANGES" || p["commit_id"] != "abc123" || !strings.HasPrefix(p["body"].(string), "Check the final total.") ||
		!strings.HasSuffix(p["body"].(string), "— Review by an agent in webshop") {
		t.Fatalf("post: %v", p)
	}
	comments := p["comments"].([]any)
	if len(comments) != 1 || comments[0].(map[string]any)["new_position"] != float64(21) {
		t.Fatalf("comments: %v", comments)
	}
	status, _ = callReviewTool(t, svc.toolReviewStatus, sb, `{"repository":"acme/web","number":42}`)
	if status["status"] != "posted" || status["url"] != "https://forge.example/acme/web/pulls/42#review-7" {
		t.Fatalf("status: %v", status)
	}
	note, done, err := svc.PromptNote(ctx, sb.EnvironmentID, sb.ID)
	if err != nil || !strings.Contains(note, "your review of acme/web#42 on forge was posted: https://forge.example/acme/web/pulls/42#review-7") {
		t.Fatalf("prompt note: %q %v", note, err)
	}
	done()
	if note, _, _ := svc.PromptNote(ctx, sb.EnvironmentID, sb.ID); note != "" {
		t.Fatalf("the note repeats: %q", note)
	}
}

func TestAVerdictOnYourOwnPullRequestPostsAsAComment(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	ff.author = "Lukas"
	ctx := context.Background()
	got, err := callReviewTool(t, svc.toolPullRequestReview, sb, `{"forge":"forge","repository":"acme/web","number":42,"verdict":"approve","summary":"Looks good."}`)
	if err != nil || got["note"] == nil {
		t.Fatalf("draft: %v %v", got, err)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if err := svc.decideReview(ctx, pending[0], integrations.Decision{Action: integrations.Allow}); err != nil {
		t.Fatal(err)
	}
	if p := ff.posted[0]; p["event"] != "COMMENT" || p["body"] != "**Approved.**\n\nLooks good." {
		t.Fatalf("post: %v", p)
	}
}

func TestADiscardedReviewTellsTheAgentWithTheNote(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	ctx := context.Background()
	if _, err := callReviewTool(t, svc.toolPullRequestReview, sb, `{"repository":"acme/web","number":42,"verdict":"comment","summary":"Nits."}`); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if err := svc.decideReview(ctx, pending[0], integrations.Decision{Action: integrations.Deny, Note: "Too picky"}); err != nil {
		t.Fatal(err)
	}
	if len(ff.posted) != 0 {
		t.Fatalf("a discarded review was posted: %v", ff.posted)
	}
	text, sent, err := svc.ReviewFeedback(ctx, sb.EnvironmentID, pending[0].ID, "Too picky")
	if err != nil || !strings.Contains(text, "I discarded it") || !strings.HasSuffix(text, "Too picky") {
		t.Fatalf("feedback: %q %v", text, err)
	}
	sent()
	if note, _, _ := svc.PromptNote(ctx, sb.EnvironmentID, sb.ID); note != "" {
		t.Fatalf("the prompt repeats what the feedback said: %q", note)
	}
}

func TestReviewDiffLines(t *testing.T) {
	d := parseDiffLines(prDiff)
	for _, c := range []struct {
		c    ReviewComment
		want bool
	}{
		{ReviewComment{Path: "web/coupon.ts", Line: 20, Side: "new"}, true},
		{ReviewComment{Path: "web/coupon.ts", Line: 22, Side: "new"}, true},
		{ReviewComment{Path: "web/coupon.ts", Line: 24, Side: "new"}, true},
		{ReviewComment{Path: "web/coupon.ts", Line: 25, Side: "new"}, false},
		{ReviewComment{Path: "web/coupon.ts", Line: 21, Side: "old"}, true},
		{ReviewComment{Path: "web/coupon.ts", Line: 22, Side: "old"}, false},
		{ReviewComment{Path: "other.ts", Line: 20, Side: "new"}, false},
	} {
		if got := d.has(c.c); got != c.want {
			t.Errorf("%+v: %v, want %v", c.c, got, c.want)
		}
	}
}

func TestGitHubReviewsNameTheirSides(t *testing.T) {
	var got map[string]any
	var path string
	call := func(_ context.Context, _, _, p string, in, out any) error {
		path = p
		b, _ := json.Marshal(in)
		_ = json.Unmarshal(b, &got)
		return nil
	}
	_, err := postReview(context.Background(), call, true, "t", "acme", "web", 42, ReviewPost{Verdict: VerdictApprove, Body: "ok", CommitID: "abc",
		Comments: []ReviewComment{{Path: "a.go", Line: 3, Side: "old", Body: "x"}, {Path: "a.go", Line: 4, Side: "new", Body: "y"}}})
	if err != nil {
		t.Fatal(err)
	}
	c := got["comments"].([]any)
	if path != "/repos/acme/web/pulls/42/reviews" || got["event"] != "APPROVE" ||
		c[0].(map[string]any)["side"] != "LEFT" || c[1].(map[string]any)["side"] != "RIGHT" || c[1].(map[string]any)["line"] != float64(4) {
		t.Fatalf("%s %v", path, got)
	}
}

func TestReviewHeadCountsTheCommitsSinceTheDraft(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	ctx := context.Background()
	got, err := callReviewTool(t, svc.toolPullRequestReview, sb, `{"repository":"acme/web","number":42,"verdict":"comment","summary":"Looks fine."}`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.LatestGitReview(ctx, sb.EnvironmentID, sb.ID, "forge", "acme", "web", 42)
	pending := []store.GitReview{r}
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v, %v (answer %v)", pending, err, got)
	}
	id := pending[0].ApprovalID
	for _, c := range []struct {
		head      string
		commits   int
		rewritten bool
	}{{"abc123", 0, false}, {"def456", 1, false}, {"fff000", 0, true}} {
		ff.mu.Lock()
		ff.head = c.head
		ff.mu.Unlock()
		h, err := svc.ReviewHead(ctx, sb.EnvironmentID, id)
		if err != nil {
			t.Fatal(err)
		}
		if h.SHA != c.head || h.NewCommits != c.commits || h.Rewritten != c.rewritten {
			t.Errorf("head %s: %+v", c.head, h)
		}
	}
}
