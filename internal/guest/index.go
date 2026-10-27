//go:build linux

package guest

import (
	"bytes"
	"context"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The index of /workspace that mentions in a chat pick from (agentproto.FilesIndex). A
// repository's files are the ones git knows, which leaves out what it ignores; elsewhere
// the walk skips the folders of dependencies, builds and caches. Symlinks are listed as
// files and never followed.

// skippedDirs are folders outside a repository that no one mentions a file of.
var skippedDirs = []string{
	".git", "node_modules", ".venv", "venv", "__pycache__", ".cache", ".next", ".turbo",
	".gradle", "target", ".mypy_cache", ".pytest_cache",
}

// maxChanged bounds the changed files an index notes, which only rank the list, so the reply
// stays within agentproto.MaxGuestReply.
const maxChanged = 2000

// indexTimeout bounds the git commands of one index.
const indexTimeout = 10 * time.Second

// gitFunc runs git with args in dir as the agent user and returns its output.
type gitFunc func(ctx context.Context, dir string, args ...string) ([]byte, error)

func (a *Agent) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := a.command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() })
	defer stop()
	err := cmd.Wait()
	return out.Bytes(), err
}

// index lists the files of workspace, relative to it, slash-separated.
func index(ctx context.Context, workspace string, git gitFunc) agentproto.FilesReply {
	ctx, cancel := context.WithTimeout(ctx, indexTimeout)
	defer cancel()
	var reply agentproto.FilesReply
	size := 0
	add := func(p string) bool {
		if len(reply.Paths) >= agentproto.MaxIndexPaths || size+len(p) > agentproto.MaxIndexBytes {
			reply.Truncated = true
			return false
		}
		reply.Paths = append(reply.Paths, p)
		size += len(p)
		return true
	}
	root, err := unix.Open(workspace, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return reply
	}
	defer unix.Close(root)
	// Folders go breadth first, so a cut keeps the files nearest the top.
	queue := []string{"."}
	for len(queue) > 0 && !reply.Truncated {
		rel := queue[0]
		queue = queue[1:]
		entries, repo, err := readDir(root, rel)
		if err != nil {
			continue
		}
		if rel == "." {
			rel = ""
		}
		if repo && repoFiles(ctx, path.Join(workspace, rel), rel, git, add, &reply) {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			p := path.Join(rel, name)
			switch {
			case e.IsDir():
				if !slices.Contains(skippedDirs, name) && (rel != "" || name != lostFound) {
					queue = append(queue, p)
				}
			case !add(p):
				return reply
			}
		}
	}
	return reply
}

// readDir lists the folder rel beneath root, which no symlink leads out of, and reports
// whether it is the top of a git repository or worktree.
func readDir(root int, rel string) ([]os.DirEntry, bool, error) {
	fd, err := unix.Openat2(root, rel, beneath)
	if err != nil {
		return nil, false, err
	}
	f := os.NewFile(uintptr(fd), rel)
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, false, err
	}
	repo := slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == ".git" })
	return entries, repo, nil
}

// repoFiles adds the files git knows in the repository at dir, whose path in the workspace
// is rel, and notes the changed ones. It reports false when git can't list them, so the
// repository is walked like any folder.
func repoFiles(ctx context.Context, dir, rel string, git gitFunc, add func(string) bool, reply *agentproto.FilesReply) bool {
	out, err := git(ctx, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate")
	if err != nil {
		return false
	}
	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" && !add(path.Join(rel, p)) {
			return true
		}
	}
	status, err := git(ctx, dir, "status", "-z", "--porcelain=v1", "--untracked-files=all", "--no-renames")
	if err != nil {
		return true
	}
	for entry := range strings.SplitSeq(string(status), "\x00") {
		// "XY path"; a deleted file can't be mentioned.
		if len(entry) < 4 || entry[0] == 'D' || entry[1] == 'D' {
			continue
		}
		if len(reply.Changed) < maxChanged {
			reply.Changed = append(reply.Changed, path.Join(rel, entry[3:]))
		}
	}
	return true
}
