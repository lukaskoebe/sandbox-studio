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

// newResolveHarness is newReviewHarness with a GitLab forge, which resolves threads.
func newResolveHarness(t *testing.T) (*Service, *store.Store, store.Sandbox, *fakeGitLab) {
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
	g, fg := newFakeGitLab(t)
	fg.me = "lukas"
	sec := store.Secret{ID: store.NewID(), Name: "FORGE_TOKEN", Sealed: []byte("sealed"), Hosts: []string{"127.0.0.1"}}
	f, err := st.CreateForge(ctx, store.Forge{ID: store.NewID(), EnvironmentID: env.ID, Name: "lab", Kind: KindGitLab, BaseURL: g.BaseURL}, sec)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "webshop"})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, Secrets: fakeSecrets{env.ID + "/" + f.SecretID: forgeToken}, Dir: t.TempDir(), Client: g.Client}
	svc.httpClient()
	return svc, st, sb, fg
}

func TestGitLabLineCommentsNameTheirDiscussions(t *testing.T) {
	g, _ := newFakeGitLab(t)
	info, err := g.ReadPullRequest(context.Background(), "t", "group/sub", "web", 7)
	if err != nil {
		t.Fatal(err)
	}
	if lc := info.LineComments; len(lc) != 2 || lc[0].Thread != "d1" || lc[0].Resolved || lc[1].Thread != "d2" || !lc[1].Resolved {
		t.Fatalf("line comments %+v", lc)
	}
}

func TestResolvingThreadsWaitsForApproval(t *testing.T) {
	svc, st, sb, fg := newResolveHarness(t)
	ctx := context.Background()
	if _, err := callReviewTool(t, svc.toolPullRequestResolve, sb, `{"repository":"group/sub/web","number":7,"threads":[" "]}`); !integrations.IsToolError(err) {
		t.Fatalf("no threads: %v", err)
	}
	if _, err := callReviewTool(t, svc.toolPullRequestResolve, sb, `{"repository":"group/sub/web","number":7,"threads":["d9"]}`); !integrations.IsToolError(err) || !strings.Contains(err.Error(), "d9") {
		t.Fatalf("another pull request's thread: %v", err)
	}
	if got, err := callReviewTool(t, svc.toolPullRequestResolve, sb, `{"repository":"group/sub/web","number":7,"threads":["d2"]}`); err != nil || got["status"] != "resolved" {
		t.Fatalf("a resolved thread asks nothing: %v %v", got, err)
	}
	args := `{"repository":"group/sub/web","number":7,"threads":["d1","d2","d1"],"idempotency_key":"r1"}`
	got, err := callReviewTool(t, svc.toolPullRequestResolve, sb, args)
	if err != nil || got["status"] != "awaiting approval" || got["approval"] == "" {
		t.Fatalf("resolve: %v %v", got, err)
	}
	if already, _ := got["alreadyResolved"].([]any); len(already) != 1 || already[0] != "d2" {
		t.Fatalf("already resolved: %v", got)
	}
	if again, _ := callReviewTool(t, svc.toolPullRequestResolve, sb, args); again["approval"] != got["approval"] {
		t.Fatalf("again: %v", again)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if len(pending) != 1 || pending[0].Kind != KindResolve {
		t.Fatalf("pending: %v", pending)
	}
	d, err := ResolveDetail(pending[0])
	if err != nil || d.Title != "Fix x" || d.Account != "lukas" || len(d.Threads) != 1 {
		t.Fatalf("detail: %+v %v", d, err)
	}
	if th := d.Threads[0]; th.ID != "d1" || th.Path != "a.go" || th.Line != 2 || th.Author != "bob" || th.Body != "Why 2?" {
		t.Fatalf("thread: %+v", th)
	}
	if len(fg.posted) != 0 {
		t.Fatalf("resolved before approval: %v", fg.posted)
	}
	if err := svc.decideResolve(ctx, pending[0], integrations.Decision{Action: integrations.Allow}); err != nil {
		t.Fatal(err)
	}
	if len(fg.posted) != 1 || fg.posted[0] != "PUT /discussions/d1" || fg.bodies[0]["resolved"] != true {
		t.Fatalf("writes: %v %v", fg.posted, fg.bodies)
	}
	a, _ := st.Approval(ctx, sb.EnvironmentID, pending[0].ID)
	res, settled, err := svc.resolveOutcome(ctx, a)
	if err != nil || !settled || res["status"] != "resolved" {
		t.Fatalf("outcome: %v %v %v", res, settled, err)
	}
	if d, _ := ResolveDetail(a); !d.Threads[0].Done {
		t.Fatalf("thread not marked done: %+v", d.Threads)
	}
}

func TestADeniedResolveLeavesTheThreads(t *testing.T) {
	svc, st, sb, fg := newResolveHarness(t)
	ctx := context.Background()
	if _, err := callReviewTool(t, svc.toolPullRequestResolve, sb, `{"repository":"group/sub/web","number":7,"threads":["d1"]}`); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if err := svc.decideResolve(ctx, pending[0], integrations.Decision{Action: integrations.Deny, Note: "Bob should say"}); err != nil {
		t.Fatal(err)
	}
	if len(fg.posted) != 0 {
		t.Fatalf("resolved anyway: %v", fg.posted)
	}
	a, _ := st.Approval(ctx, sb.EnvironmentID, pending[0].ID)
	if res, _, _ := svc.resolveOutcome(ctx, a); res["status"] != "discarded" || res["note"] != "Bob should say" {
		t.Fatalf("outcome: %v", res)
	}
}

func TestForgejoCantResolveThreads(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	_, err := callReviewTool(t, svc.toolPullRequestResolve, sb, `{"repository":"acme/web","number":42,"threads":["1"]}`)
	if !integrations.IsToolError(err) || !strings.Contains(err.Error(), "no API to resolve") {
		t.Fatalf("forgejo: %v", err)
	}
}

func TestGitHubThreadsAndResolveGoThroughGraphQL(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" || r.Header.Get("Authorization") != "Bearer ghp_tok" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		queries = append(queries, in.Query)
		switch {
		case strings.Contains(in.Query, "reviewThreads"):
			if in.Variables["owner"] != "acme" || in.Variables["number"] != float64(7) {
				http.Error(w, "bad variables", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
				{"id":"PRRT_a","isResolved":false,"comments":{"nodes":[{"databaseId":11},{"databaseId":12}]}},
				{"id":"PRRT_b","isResolved":true,"comments":{"nodes":[{"databaseId":13}]}}]}}}}}`))
		case in.Variables["id"] == "PRRT_a":
			_, _ = w.Write([]byte(`{"data":{"resolveReviewThread":{"thread":{"isResolved":true}}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":null,"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a node with the global id of 'PRRT_x'"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	g := &GitHub{BaseURL: srv.URL, Client: srv.Client()}
	ctx := context.Background()
	comments := []PRLineComment{{id: 11}, {id: 12}, {id: 13}, {id: 14}}
	if err := g.threads(ctx, "ghp_tok", "acme", "web", 7, comments); err != nil {
		t.Fatal(err)
	}
	if comments[0].Thread != "PRRT_a" || comments[1].Thread != "PRRT_a" || comments[0].Resolved || comments[2].Thread != "PRRT_b" || !comments[2].Resolved || comments[3].Thread != "" {
		t.Fatalf("threads %+v", comments)
	}
	if err := g.ResolveThread(ctx, "ghp_tok", "acme", "web", 7, "PRRT_a"); err != nil {
		t.Fatal(err)
	}
	if err := g.ResolveThread(ctx, "ghp_tok", "acme", "web", 7, "PRRT_x"); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("a GraphQL error: %v", err)
	}
	if len(queries) != 3 || !strings.Contains(queries[1], "resolveReviewThread") {
		t.Fatalf("queries %q", queries)
	}
}
