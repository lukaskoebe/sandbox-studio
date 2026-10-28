package gitreview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestPullRequestListShowsAnyonesPullRequests(t *testing.T) {
	svc, _, sb, ff := newReviewHarness(t)
	got, err := callReviewTool(t, svc.toolPullRequestList, sb, `{"repository":"acme/web"}`)
	if err != nil {
		t.Fatal(err)
	}
	prs := got["pullRequests"].([]any)
	first, second := prs[0].(map[string]any), prs[1].(map[string]any)
	if len(prs) != 2 || got["repository"] != "acme/web" || got["forge"] != "forge" ||
		first["number"] != float64(42) || first["author"] != "someone" || first["head"] != "feature/coupons" ||
		first["requestedReviewers"].([]any)[0] != "lukas" || first["labels"].([]any)[0] != "checkout" ||
		second["draft"] != true || second["author"] != "erin" {
		t.Fatalf("list: %v", got)
	}
	if ff.listed[0] != "limit=30&sort=recentupdate&state=open" {
		t.Fatalf("query: %q", ff.listed[0])
	}
	got, err = callReviewTool(t, svc.toolPullRequestList, sb, `{"repository":"acme/web","state":"all","limit":1}`)
	if err != nil || len(got["pullRequests"].([]any)) != 1 || ff.listed[1] != "limit=1&sort=recentupdate&state=all" {
		t.Fatalf("one of all: %v %v %q", got, err, ff.listed)
	}
	for args, want := range map[string]string{
		`{"repository":"acme/web","state":"merged"}`: "state is open, closed or all",
		`{"repository":"web"}`:                       "repository is owner/repo",
		`{"repository":"acme/web","forge":"nope"}`:   `no forge "nope"`,
	} {
		if _, err := callReviewTool(t, svc.toolPullRequestList, sb, args); !integrations.IsToolError(err) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q", args, err, want)
		}
	}
}

func TestPullRequestReadShowsTheReviewsLineComments(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	out, err := svc.toolPullRequestRead(context.Background(), sb, json.RawMessage(`{"repository":"acme/web","number":42}`))
	if err != nil {
		t.Fatal(err)
	}
	info := out.(PullRequestInfo)
	if len(info.Reviews) != 2 || len(info.LineComments) != 1 {
		t.Fatalf("reviews %+v, line comments %+v", info.Reviews, info.LineComments)
	}
	if c := info.LineComments[0]; c.Author != "carol" || c.Path != "web/coupon.ts" || c.Line != 21 || c.Side != "old" {
		t.Fatalf("line comment: %+v", c)
	}
}

func TestGitHubLineCommentsComeFromThePullRequest(t *testing.T) {
	var paths []string
	call := func(_ context.Context, _, _, p string, _, out any) error {
		paths = append(paths, p)
		var body string
		switch {
		case strings.HasSuffix(p, "/pulls/42"):
			body = `{"number":42,"user":{"login":"someone"}}`
		case strings.Contains(p, "/pulls/42/comments"):
			body = `[{"user":{"login":"carol"},"path":"a.go","line":null,"original_line":7,"side":"LEFT","body":"old"},
				{"user":{"login":"dave"},"path":"a.go","line":9,"original_line":8,"side":"RIGHT","body":"new"}]`
		default:
			body = `[]`
		}
		return json.Unmarshal([]byte(body), out)
	}
	info, err := readPullRequest(context.Background(), call, true, "t", "acme", "web", 42)
	if err != nil {
		t.Fatal(err)
	}
	want := []PRLineComment{{Author: "carol", Path: "a.go", Line: 7, Side: "old", Body: "old"}, {Author: "dave", Path: "a.go", Line: 9, Side: "new", Body: "new"}}
	if len(info.LineComments) != 2 || info.LineComments[0] != want[0] || info.LineComments[1] != want[1] {
		t.Fatalf("line comments %+v (%v)", info.LineComments, paths)
	}
}

func TestRepositoriesListsEachForge(t *testing.T) {
	svc, st, sb, _ := newReviewHarness(t)
	ctx := context.Background()
	if _, err := st.CreateForge(ctx, store.Forge{ID: store.NewID(), EnvironmentID: sb.EnvironmentID, Name: "plain", Kind: KindSSH,
		BaseURL: "ssh://git@git.example", Auth: store.ForgeAuthAgent}, store.Secret{}); err != nil {
		t.Fatal(err)
	}
	out, err := svc.toolRepositories(ctx, sb, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	forges := out.(map[string]any)["forges"].([]forgeView)
	byName := map[string]forgeView{}
	for _, f := range forges {
		byName[f.Name] = f
	}
	fj, plain := byName["forge"], byName["plain"]
	if len(forges) != 2 || !fj.Reviews || fj.Remote != "https://"+Host+"/forge/" || len(fj.Repositories) != 2 || fj.Repositories[1].Path != "acme/api" {
		t.Fatalf("forgejo: %+v", fj)
	}
	if plain.Reviews || len(plain.Repositories) != 0 || !strings.Contains(plain.Note, "no list") {
		t.Fatalf("ssh: %+v", plain)
	}
	out, err = svc.toolRepositories(ctx, sb, json.RawMessage(`{"forge":"forge","query":"SHOP"}`))
	if err != nil {
		t.Fatal(err)
	}
	if f := out.(map[string]any)["forges"].([]forgeView); len(f) != 1 || len(f[0].Repositories) != 1 || f[0].Repositories[0].Path != "acme/web" {
		t.Fatalf("query: %+v", f)
	}
	if _, err := svc.toolRepositories(ctx, sb, json.RawMessage(`{"forge":"nope"}`)); !integrations.IsToolError(err) {
		t.Fatalf("unknown forge: %v", err)
	}
}
