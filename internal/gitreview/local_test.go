package gitreview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// gitRunner runs the host's git with a config of its own, as an agent in a sandbox would.
func gitRunner(t *testing.T) func(dir string, args ...string) (string, error) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git binary")
	}
	home := t.TempDir()
	return func(dir string, args ...string) (string, error) {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "LC_ALL=C",
			"GIT_AUTHOR_NAME=Agent", "GIT_AUTHOR_EMAIL=agent@example.com",
			"GIT_COMMITTER_NAME=Agent", "GIT_COMMITTER_EMAIL=agent@example.com")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
}

// TestLocalForgeEndToEnd: a sandbox clones a working copy and a bare repository from a
// folder on this computer, over protocol v2 and v0, and its pushes reach them once approved.
// A push to the branch the working copy has checked out is refused by git, as a push to the
// folder would be.
func TestLocalForgeEndToEnd(t *testing.T) {
	run := gitRunner(t)
	h := newHarness(t)
	folder := t.TempDir()
	proj := filepath.Join(folder, "proj")
	must := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return out
	}
	must(run(folder, "init", "-q", "-b", "main", proj))
	if err := os.WriteFile(filepath.Join(proj, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(run(proj, "add", "README"))
	must(run(proj, "commit", "-q", "-m", "First"))
	must(run(folder, "clone", "-q", "--bare", proj, filepath.Join(folder, "bare.git")))

	u, err := CheckForge("local", KindLocal, folder, "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := h.st.CreateForge(h.ctx, store.Forge{ID: store.NewID(), EnvironmentID: h.env.ID, Name: "local", Kind: KindLocal, BaseURL: u.Base, Auth: store.ForgeAuthNone}, store.Secret{})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := h.svc.TestForge(h.ctx, f, "proj"); err != nil || c.Branches != 1 {
		t.Fatalf("test forge: %+v %v", c, err)
	}
	if _, err := h.svc.TestForge(h.ctx, f, "missing"); err == nil {
		t.Fatal("a missing repository passed the test")
	}
	g := h.guest(t, h.env.ID, "one")
	home := t.TempDir()

	for _, c := range []struct{ repo, protocol, branch string }{
		{"proj", "2", "agent"},
		{"bare", "0", "agent-v0"},
	} {
		work := filepath.Join(home, c.repo)
		must(run(home, "-c", "protocol.version="+c.protocol, "clone", "-q", g.srv.URL+"/local/"+c.repo+".git", work))
		if err := os.WriteFile(filepath.Join(work, "agent.txt"), []byte("from the agent\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		must(run(work, "add", "agent.txt"))
		must(run(work, "commit", "-q", "-m", "Agent change"))
		if out := must(run(work, "push", "origin", "HEAD:refs/heads/"+c.branch)); !strings.Contains(out, "queued for review") {
			t.Fatalf("push: %s", out)
		}
		pending := h.approvals(t, KindPush, store.StatusPending)
		if len(pending) != 1 {
			t.Fatalf("%s: approvals = %d", c.repo, len(pending))
		}
		h.decide(t, pending[0], integrations.Allow, "")
		dir := proj
		if c.repo == "bare" {
			dir = filepath.Join(folder, "bare.git")
		}
		got := strings.TrimSpace(must(run(dir, "rev-parse", "refs/heads/"+c.branch)))
		if want := strings.TrimSpace(must(run(work, "rev-parse", "HEAD"))); got != want {
			t.Fatalf("%s: %s is %s, want %s", c.repo, c.branch, got, want)
		}
		// A fetch sees the approved branch.
		must(run(work, "-c", "protocol.version="+c.protocol, "fetch", "-q", "origin"))
		if out := must(run(work, "branch", "-r")); !strings.Contains(out, "origin/"+c.branch) {
			t.Fatalf("%s: fetched branches: %s", c.repo, out)
		}
	}

	work := filepath.Join(home, "proj")
	must(run(work, "push", "-q", "origin", "HEAD:refs/heads/main"))
	pending := h.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 1 {
		t.Fatalf("approvals = %d", len(pending))
	}
	h.decide(t, pending[0], integrations.Allow, "")
	p, err := h.st.GitPushByApproval(h.ctx, h.env.ID, pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != store.PushFailed || !strings.Contains(p.Result, "checked out") {
		t.Fatalf("push to the checked-out branch: %s %q", p.State, p.Result)
	}
	if out, err := run(home, "clone", "-q", g.srv.URL+"/local/missing.git", filepath.Join(home, "missing")); err == nil || !strings.Contains(out, "no repository missing") {
		t.Fatalf("clone of a missing repository: %v\n%s", err, out)
	}
}

func TestCheckLocalPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if u, err := CheckLocalPath(" " + dir + "/sub/.. "); err != nil || u.Base != dir {
		t.Fatalf("%v %+v", err, u)
	}
	if u, err := CheckLocalPath("file://" + dir); err != nil || u.Base != dir {
		t.Fatalf("file URL: %v %+v", err, u)
	}
	home, _ := os.UserHomeDir()
	if u, err := CheckLocalPath("~"); err != nil || u.Base != filepath.Clean(home) {
		t.Fatalf("~: %v %+v", err, u)
	}
	for _, bad := range []string{"", "git", "./git", "~other/git", "/", filepath.Join(dir, "missing"), file} {
		if _, err := CheckLocalPath(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseLocalRepoURL(t *testing.T) {
	folder := t.TempDir()
	if err := os.Mkdir(filepath.Join(folder, "proj.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := ParseRepoURL(filepath.Join(folder, "proj.git"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (RepoURL{Kind: KindLocal, BaseURL: folder, Name: "local", Path: "proj", Auth: store.ForgeAuthNone}); r != want {
		t.Fatalf("%+v, want %+v", r, want)
	}
	if _, err := ParseRepoURL(filepath.Join(folder, "missing")); err == nil {
		t.Fatal("a missing folder was parsed")
	}
}
