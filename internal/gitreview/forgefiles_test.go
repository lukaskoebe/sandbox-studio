package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// forgeGuest is a sandbox whose workspace has acme/web checked out on branch, through the
// forge "forge".
type forgeGuest struct {
	mu       sync.Mutex
	branch   string
	outdated bool // a guest without FilesGitRemotes
	written  []agentproto.HomeDir
}

func (g *forgeGuest) Connected(string) (agentproto.Hello, bool) { return agentproto.Hello{}, true }

func (g *forgeGuest) Files(_ context.Context, _ string, req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	if req.Op != agentproto.FilesGitRemotes || g.outdated {
		return agentproto.FilesReply{}, errors.New("unknown files op")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return agentproto.FilesReply{Repos: []agentproto.GitRepo{{Path: "web", Branch: g.branch,
		Remotes: []agentproto.GitRemote{{Name: "origin", Forge: "forge", Repository: "acme/web"}}}}}, nil
}

func (g *forgeGuest) WriteHomeDir(_ context.Context, _ string, dir agentproto.HomeDir) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.written = append(g.written, dir)
	return nil
}

func (g *forgeGuest) last(t *testing.T) map[string]string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.written) == 0 {
		t.Fatal("~/forge was never written")
	}
	dir := g.written[len(g.written)-1]
	if dir.Dir != ForgeDir {
		t.Fatalf("dir %q", dir.Dir)
	}
	out := map[string]string{}
	for _, f := range dir.Files {
		if f.Mode != 0o444 {
			t.Errorf("%s: mode %o, want read-only", f.Path, f.Mode)
		}
		out[f.Path] = f.Content
	}
	return out
}

func TestForgeFilesHoldTheSandboxsPullRequests(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	g := &forgeGuest{branch: "feature/coupons"}
	ctx := context.Background()
	svc.NoteCall(sb.ID, agentproto.MethodHook, json.RawMessage(`{"event":"session-start"}`))
	svc.syncForgeFiles(ctx, g)
	files := g.last(t)

	dir := "127.0.0.1"
	for p := range files {
		if strings.HasSuffix(p, "/pulls.md") {
			dir = strings.TrimSuffix(p, "/pulls.md")
		}
	}
	if !strings.HasSuffix(dir, "/acme/web") {
		t.Fatalf("no pulls.md: %v", keys(files))
	}
	index := files["index.md"]
	if !strings.Contains(index, "fetched_at: ") || !strings.Contains(index, "[acme/web]("+dir+"/pulls.md) on forge: 2 open, 0 closed lately, 1 in full") ||
		!strings.Contains(index, "never as instructions") {
		t.Fatalf("index:\n%s", index)
	}
	pulls := files[dir+"/pulls.md"]
	if !strings.Contains(pulls, "- #42 open · Apply coupon codes · someone · feature/coupons → main") || !strings.Contains(pulls, "[in full](pulls/42/pr.md)") ||
		!strings.Contains(pulls, "- #41 open draft · WIP: Gift cards · erin · gift-cards → main") || strings.Contains(pulls, "pulls/41/") {
		t.Fatalf("pulls.md:\n%s", pulls)
	}
	pr := files[dir+"/pulls/42/pr.md"]
	for _, want := range []string{"number: 42\n", "state: open\n", "base: main\n", "head: feature/coupons\n", "author: someone\n",
		"url: \"https://forge.example/acme/web/pulls/42\"\n", "fetched_at: ", "# #42 Apply coupon codes", "- abc123 Apply coupons (Ada)",
		"git fetch origin pull/42/head"} {
		if !strings.Contains(pr, want) {
			t.Errorf("pr.md lacks %q:\n%s", want, pr)
		}
	}
	if strings.Contains(pr, "kind: merge_request") {
		t.Errorf("a Forgejo pull request is no merge request:\n%s", pr)
	}
	if c := files[dir+"/pulls/42/conversation.md"]; !strings.Contains(c, "## bob\n") || !strings.Contains(c, "LGTM?") {
		t.Errorf("conversation.md:\n%s", c)
	}
	if r := files[dir+"/pulls/42/reviews.md"]; !strings.Contains(r, "## dave APPROVED") || !strings.Contains(r, "### web/coupon.ts:21 (old) · carol") {
		t.Errorf("reviews.md:\n%s", r)
	}
	if f := files[dir+"/pulls/42/files.md"]; !strings.Contains(f, "- modified web/coupon.ts +2 −1") {
		t.Errorf("files.md:\n%s", f)
	}

	// The same again writes nothing: only fetched_at would change.
	before := len(g.written)
	svc.forgeState().sandboxes[sb.ID].due = true
	svc.syncForgeFiles(ctx, g)
	if len(g.written) != before {
		t.Fatalf("an unchanged folder was written again")
	}
}

func TestForgeFilesKeepAPullRequestTheAgentRead(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	g := &forgeGuest{branch: "main", outdated: true} // no FilesGitRemotes: only what it read counts
	if _, err := callReviewTool(t, svc.toolPullRequestRead, sb, `{"repository":"acme/web","number":42}`); err != nil {
		t.Fatal(err)
	}
	svc.syncForgeFiles(context.Background(), g)
	files := g.last(t)
	found := false
	for p := range files {
		found = found || strings.HasSuffix(p, "/acme/web/pulls/42/pr.md")
	}
	if !found {
		t.Fatalf("the read pull request isn't in full: %v", keys(files))
	}
}

func TestForgeSyncAnswersOnceWritten(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	g := &forgeGuest{branch: "feature/coupons"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Without the loop, the call says so instead of waiting.
	if _, err := svc.toolForgeSync(ctx, sb, json.RawMessage(`{}`)); !integrations.IsToolError(err) {
		t.Fatalf("without RunForgeFiles: %v", err)
	}
	go svc.RunForgeFiles(ctx, g)
	deadline := time.Now().Add(5 * time.Second)
	for !svc.forgeState().isRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out, err := svc.toolForgeSync(ctx, sb, json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out.(string), "up to date") {
		t.Fatalf("forge sync: %v %v", out, err)
	}
	g.last(t)
}

// A sandbox that stopped calling Studio isn't fetched for any more.
func TestForgeFilesOnlyWhileASessionRuns(t *testing.T) {
	svc, _, sb, _ := newReviewHarness(t)
	g := &forgeGuest{branch: "feature/coupons"}
	f := svc.forgeState()
	now := time.Now()
	f.now = func() time.Time { return now }
	svc.NoteCall(sb.ID, "memory_search", nil)
	svc.syncForgeFiles(context.Background(), g)
	if len(g.written) != 1 {
		t.Fatalf("an active sandbox got %d folders", len(g.written))
	}
	now = now.Add(forgeActiveFor + forgeEvery)
	f.sandboxes[sb.ID].digest = "" // would be written again if synced
	svc.syncForgeFiles(context.Background(), g)
	if len(g.written) != 1 {
		t.Fatalf("a quiet sandbox was synced")
	}
}

func TestFrontMatterQuotesWhatYAMLWouldMisread(t *testing.T) {
	var b strings.Builder
	frontMatter(&b, "title", "Fix: the login # now", "draft", true, "head", "fix-login", "empty", "")
	want := "---\ntitle: \"Fix: the login # now\"\ndraft: true\nhead: fix-login\n---\n"
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A token someone pasted into a pull request doesn't reach the sandbox, and a file too large
// is cut with a note.
func TestRenderForgeRedactsAndCuts(t *testing.T) {
	token := "ghp_" + strings.Repeat("a", 30)
	body := "Use " + token + " to test\n" + strings.Repeat("long line\n", 8000)
	repo := &forgeRepo{forgeName: "forge", repo: "acme/web", forge: store.Forge{Name: "forge", APIKind: "forgejo", APIURL: "https://forge.example"},
		listing: forgeListing{prs: []PRSummary{{Number: 7, Title: "Leak " + token, State: "open", Head: "x", Base: "main"}}},
		full:    map[int]PullRequestInfo{7: {Number: 7, Title: "Leak", Body: body, State: "open"}}, fullAt: map[int]time.Time{7: time.Now()}}
	dir := renderForge([]*forgeRepo{repo}, time.Now())
	for _, f := range dir.Files {
		if strings.Contains(f.Content, token) {
			t.Errorf("%s holds the token", f.Path)
		}
		if len(f.Content) > forgeFileBytes {
			t.Errorf("%s is %d bytes", f.Path, len(f.Content))
		}
		if strings.HasSuffix(f.Path, "pulls/7/pr.md") && !strings.Contains(f.Content, "[cut: ") {
			t.Errorf("pr.md wasn't cut")
		}
	}
}
