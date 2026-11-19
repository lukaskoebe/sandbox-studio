package gitreview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestRemoteOnFindsTheForgeARemotePointsAt(t *testing.T) {
	forges := []store.Forge{
		{ID: "1", Name: "plain", Kind: KindHTTPS, BaseURL: "https://plain.example"},
		{ID: "2", Name: "forgejo", Kind: KindForgejo, BaseURL: "https://forgejo.example.com"},
		{ID: "3", Name: "gitlab", Kind: KindGitLab, BaseURL: "https://example.org/gitlab"},
		{ID: "4", Name: "ssh", Kind: KindSSH, BaseURL: "ssh://git@git.example.net:2222", APIKind: KindForgejo,
			APIURL: "https://git.example.net", APISecretID: "s"},
		{ID: "5", Name: "local", Kind: KindLocal, BaseURL: "/home/me/git"},
	}
	for _, c := range []struct{ raw, forge, path string }{
		{"https://forgejo.example.com/acme/web.git", "forgejo", "acme/web"},
		{"https://Forgejo.example.com/acme/web/", "forgejo", "acme/web"},
		{"https://forgejo.example.com:443/acme/web", "forgejo", "acme/web"},
		{"git@forgejo.example.com:acme/web.git", "forgejo", "acme/web"},
		{"ssh://git@forgejo.example.com/acme/web", "forgejo", "acme/web"},
		{"https://forgejo.example.com:8443/acme/web.git", "", ""},
		{"https://forgejo.example.com/acme/web/sub.git", "", ""}, // Forgejo has no subgroups
		{"https://example.org/gitlab/group/sub/proj.git", "gitlab", "group/sub/proj"},
		{"https://example.org/other/proj.git", "", ""},
		{"ssh://git@git.example.net:2222/acme/api.git", "ssh", "acme/api"},
		{"git@git.example.net:acme/api.git", "", ""}, // port 22, not the forge's 2222
		{"https://git.example.net/acme/api.git", "ssh", "acme/api"},
		{"https://plain.example/acme/web.git", "", ""}, // no pull requests there
		{"/home/me/git/web", "", ""},
		{"file:///home/me/git/web", "", ""},
		{`C:\repos\web`, "", ""},
		{"../web", "", ""},
	} {
		f, path, ok := remoteOn(forges, c.raw)
		if ok != (c.forge != "") || f.Name != c.forge || path != c.path {
			t.Errorf("%s: %q %q %v, want %q %q", c.raw, f.Name, path, ok, c.forge, c.path)
		}
	}
}

// localWithRemote adds a local forge whose folder has the repository lukas/web, with the given
// remotes, to the harness's environment.
func localWithRemote(t *testing.T, st *store.Store, envID string, remotes map[string]string) store.Forge {
	t.Helper()
	run := gitRunner(t)
	folder := t.TempDir()
	repo := filepath.Join(folder, "lukas", "web")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := run(repo, "init", "-q"); err != nil {
		t.Fatal(out, err)
	}
	for name, url := range remotes {
		if out, err := run(repo, "remote", "add", name, url); err != nil {
			t.Fatal(out, err)
		}
	}
	f, err := st.CreateForge(context.Background(), store.Forge{ID: store.NewID(), EnvironmentID: envID, Name: "local", Kind: KindLocal,
		BaseURL: folder, Auth: store.ForgeAuthNone}, store.Secret{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A pull request tool named the folder on the user's computer, as `studio` does when it is the
// repository's origin, and answers from the forge the folder's repository pushes to.
func TestPullRequestToolsGoFromAFolderToItsForge(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	localWithRemote(t, st, sb.EnvironmentID, map[string]string{"forgejo": ff.srv.URL + "/acme/web.git"})
	got, err := callReviewTool(t, svc.toolPullRequestList, sb, `{"forge":"local","repository":"lukas/web"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got["forge"] != "forge" || got["repository"] != "acme/web" {
		t.Fatalf("listed %v", got)
	}
	read, err := callReviewTool(t, svc.toolPullRequestRead, sb, `{"forge":"local","repository":"lukas/web","number":42}`)
	if err != nil || read["title"] != "Apply coupon codes" {
		t.Fatalf("read %v %v", read, err)
	}

	// A repository of the folder that pushes nowhere Studio knows says why there's nothing.
	_, err = callReviewTool(t, svc.toolPullRequestList, sb, `{"forge":"local","repository":"lukas/other"}`)
	if err == nil || !strings.Contains(err.Error(), "folder on the user's computer, without pull requests") {
		t.Fatalf("err %v", err)
	}
}

// The sandbox's clone has origin on the folder and a second remote on the forge.
func TestPullRequestToolsGoFromAFolderToTheCloneOtherRemote(t *testing.T) {
	svc, st, sb, _ := newReviewHarness(t)
	localWithRemote(t, st, sb.EnvironmentID, nil)
	g := &forgeGuest{branch: "main", remotes: []agentproto.GitRemote{
		{Name: "origin", Forge: "local", Repository: "lukas/web"},
		{Name: "upstream", Forge: "forge", Repository: "acme/web"},
	}}
	svc.forgeState().guest = g
	read, err := callReviewTool(t, svc.toolPullRequestRead, sb, `{"forge":"local","repository":"lukas/web","number":42}`)
	if err != nil || read["title"] != "Apply coupon codes" {
		t.Fatalf("read %v %v", read, err)
	}

	// Two forges with pull requests: the agent names one.
	g.remotes = append(g.remotes, agentproto.GitRemote{Name: "fork", Forge: "forge", Repository: "ada/web"})
	_, err = callReviewTool(t, svc.toolPullRequestRead, sb, `{"forge":"local","repository":"lukas/web","number":42}`)
	if err == nil || !strings.Contains(err.Error(), "acme/web on forge, and ada/web on forge") {
		t.Fatalf("err %v", err)
	}
}

// ~/forge lists the pull requests of the forge the folder pushes to, and not the folder.
func TestForgeFilesGoFromAFolderToItsForge(t *testing.T) {
	svc, st, sb, ff := newReviewHarness(t)
	localWithRemote(t, st, sb.EnvironmentID, map[string]string{"origin": ff.srv.URL + "/acme/web.git"})
	g := &forgeGuest{branch: "feature/coupons", remotes: []agentproto.GitRemote{{Name: "origin", Forge: "local", Repository: "lukas/web"}}}
	svc.NoteCall(sb.ID, agentproto.MethodHook, json.RawMessage(`{"event":"session-start"}`))
	svc.syncForgeFiles(context.Background(), g)
	files := g.last(t)
	index := files["index.md"]
	if !strings.Contains(index, "[acme/web](") || !strings.Contains(index, ") on forge: 2 open") || strings.Contains(index, "on local") {
		t.Fatalf("index:\n%s", index)
	}
	var pr string
	for p, c := range files {
		if strings.HasSuffix(p, "/acme/web/pulls/42/pr.md") {
			pr = c
		}
	}
	if !strings.Contains(pr, "head: feature/coupons\n") {
		t.Fatalf("the clone's branch didn't carry over: %v", keys(files))
	}
}
