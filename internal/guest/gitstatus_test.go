//go:build linux

package guest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// hostGit runs the host's git as the guest runs the agent user's, with a config of its own.
func hostGit(t *testing.T) gitFunc {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git binary")
	}
	home := t.TempDir()
	return func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com",
			"GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@example.com")
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		return out.Bytes(), err
	}
}

// TestGitStatus: each repository of the workspace with its branch, how far ahead it is, and
// its staged, unstaged and untracked changes with their lines, and each change's diff.
func TestGitStatus(t *testing.T) {
	git := hostGit(t)
	ws := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := git(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := filepath.Join(ws, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	run(app, "init", "-q", "-b", "main")
	write(filepath.Join(app, "store.go"), "a\nb\nc\n")
	write(filepath.Join(app, "old.md"), "old\n")
	write(filepath.Join(app, "moved.txt"), "one\ntwo\nthree\nfour\n")
	run(app, "add", ".")
	run(app, "commit", "-q", "-m", "First")
	run(ws, "clone", "-q", "--bare", app, filepath.Join(ws, "remote.git"))
	run(app, "remote", "add", "origin", filepath.Join(ws, "remote.git"))
	run(app, "fetch", "-q", "origin")
	run(app, "branch", "-q", "--set-upstream-to=origin/main")
	run(app, "checkout", "-q", "-b", "fix")
	run(app, "branch", "-q", "--set-upstream-to=origin/main")
	write(filepath.Join(app, "cron.go"), "x\n")
	run(app, "add", "cron.go")
	run(app, "commit", "-q", "-m", "Second")
	// Staged: a new file and a rename; then changed again, and an unstaged deletion.
	write(filepath.Join(app, "test.go"), "t1\nt2\n")
	run(app, "add", "test.go")
	run(app, "mv", "moved.txt", "renamed.txt")
	write(filepath.Join(app, "test.go"), "t1\nt2\nt3\n")
	write(filepath.Join(app, "store.go"), "a\nB\nc\nd\n")
	if err := os.Remove(filepath.Join(app, "old.md")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(app, "notes.txt"), "n\n")
	// A second repository deeper down, and node_modules isn't searched.
	lib := filepath.Join(ws, "libs", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	run(lib, "init", "-q", "-b", "trunk")
	if err := os.MkdirAll(filepath.Join(ws, "node_modules", "dep", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	reply := gitStatus(context.Background(), ws, git)
	if len(reply.Repos) != 2 || reply.Repos[0].Path != "app" || reply.Repos[1].Path != "libs/lib" {
		t.Fatalf("repos = %+v", reply.Repos)
	}
	r := reply.Repos[0]
	if r.Branch != "fix" || r.Upstream != "origin/main" || r.Ahead != 1 || r.Behind != 0 || len(r.Head) != 40 || r.Error != "" {
		t.Fatalf("app: %+v", r)
	}
	var got []string
	for _, c := range r.Changes {
		s := c.Area + " " + c.Status + " " + c.Path
		if c.From != "" {
			s += " from " + c.From
		}
		got = append(got, s+" +"+strconv.Itoa(c.Added)+" -"+strconv.Itoa(c.Removed))
	}
	want := []string{
		"staged renamed renamed.txt from moved.txt +0 -0",
		"staged added test.go +2 -0",
		"unstaged deleted old.md +0 -1",
		"unstaged modified store.go +2 -1",
		"unstaged modified test.go +1 -0",
		"untracked added notes.txt +0 -0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if l := reply.Repos[1]; l.Branch != "trunk" || l.Head != "" || len(l.Changes) != 0 {
		t.Fatalf("lib: %+v", l)
	}

	for _, c := range []struct {
		req  agentproto.FilesRequest
		want string
	}{
		{agentproto.FilesRequest{Path: "app", File: "store.go", Area: agentproto.GitUnstaged}, "+B\n"},
		{agentproto.FilesRequest{Path: "app", File: "test.go", Area: agentproto.GitStaged}, "+t2\n"},
		{agentproto.FilesRequest{Path: "app", File: "renamed.txt", From: "moved.txt", Area: agentproto.GitStaged}, "rename to renamed.txt"},
		{agentproto.FilesRequest{Path: "app", File: "notes.txt", Area: agentproto.GitUntracked}, "+n\n"},
	} {
		d, err := gitDiff(context.Background(), ws, c.req, git)
		if err != nil || !strings.Contains(d.Diff, c.want) {
			t.Errorf("%+v: %v\n%s", c.req, err, d.Diff)
		}
	}
	for _, bad := range []agentproto.FilesRequest{
		{Path: "../x", File: "a", Area: agentproto.GitUnstaged},
		{Path: "app", File: "../../etc/passwd", Area: agentproto.GitUntracked},
		{Path: "app", File: "a", Area: "elsewhere"},
	} {
		if _, err := gitDiff(context.Background(), ws, bad, git); err == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
}
