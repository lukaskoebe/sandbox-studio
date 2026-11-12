//go:build linux

package guest

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The screen's Changes tab: the repositories of /workspace as git sees them
// (agentproto.FilesGit), and one change's diff (agentproto.FilesGitDiff). Git runs as the
// agent user, without optional locks, so a look never gets in the way of the agent's git.

// gitStatusTimeout bounds one FilesGit or FilesGitDiff.
const gitStatusTimeout = 15 * time.Second

// maxRepoSearch bounds the folders searched for repositories, and maxRepoDepth how deep.
const (
	maxRepoSearch = 5000
	maxRepoDepth  = 4
)

// gitStatus lists the repositories of workspace with their branch and changes.
func gitStatus(ctx context.Context, workspace string, git gitFunc) agentproto.FilesReply {
	ctx, cancel := context.WithTimeout(ctx, gitStatusTimeout)
	defer cancel()
	reply := agentproto.FilesReply{Repos: []agentproto.GitRepo{}}
	for _, rel := range findRepos(workspace) {
		if len(reply.Repos) == agentproto.MaxGitRepos {
			reply.Truncated = true
			break
		}
		reply.Repos = append(reply.Repos, repoStatus(ctx, path.Join(workspace, rel), rel, git))
	}
	return reply
}

// gitRemotes lists the repositories of workspace with their branch and their remotes through
// agentproto.GitHost.
func gitRemotes(ctx context.Context, workspace string, git gitFunc) agentproto.FilesReply {
	ctx, cancel := context.WithTimeout(ctx, gitStatusTimeout)
	defer cancel()
	reply := agentproto.FilesReply{Repos: []agentproto.GitRepo{}}
	for _, rel := range findRepos(workspace) {
		if len(reply.Repos) == agentproto.MaxGitRepos {
			reply.Truncated = true
			break
		}
		dir := path.Join(workspace, rel)
		repo := agentproto.GitRepo{Path: rel}
		if out, err := git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
			repo.Branch = strings.TrimSpace(string(out))
		}
		// No remotes is git's exit status 1, an empty list.
		out, _ := git(ctx, dir, "config", "--get-regexp", `^remote\..*\.url$`)
		repo.Remotes = parseStudioRemotes(string(out))
		reply.Repos = append(reply.Repos, repo)
	}
	return reply
}

// parseStudioRemotes reads git config --get-regexp's "remote.<name>.url <url>" lines and keeps
// the remotes through agentproto.GitHost.
func parseStudioRemotes(out string) []agentproto.GitRemote {
	var remotes []agentproto.GitRemote
	for line := range strings.Lines(out) {
		key, raw, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		forge, repo, ok := agentproto.ParseGitRemote(raw)
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		remotes = append(remotes, agentproto.GitRemote{Name: name, Forge: forge, Repository: repo})
	}
	return remotes
}

// findRepos returns the folders of workspace, relative to it, that are the top of a git
// repository or worktree, breadth first, without looking into a repository or the folders
// the index skips.
func findRepos(workspace string) []string {
	root, err := unix.Open(workspace, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(root)
	var repos []string
	type dir struct {
		rel   string
		depth int
	}
	queue := []dir{{".", 0}}
	for seen := 0; len(queue) > 0 && seen < maxRepoSearch; seen++ {
		d := queue[0]
		queue = queue[1:]
		entries, repo, err := readDir(root, d.rel)
		if err != nil {
			continue
		}
		rel := d.rel
		if rel == "." {
			rel = ""
		}
		if repo {
			repos = append(repos, rel)
			continue
		}
		if d.depth == maxRepoDepth {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() && !slices.Contains(skippedDirs, name) && (rel != "" || name != lostFound) {
				queue = append(queue, dir{path.Join(rel, name), d.depth + 1})
			}
		}
	}
	return repos
}

// repoStatus reads one repository: git status for the branch and the changes, and git diff
// --numstat for the lines of each.
func repoStatus(ctx context.Context, dir, rel string, git gitFunc) agentproto.GitRepo {
	repo := agentproto.GitRepo{Path: rel, Changes: []agentproto.GitChange{}}
	out, err := git(ctx, dir, "status", "-z", "--porcelain=v2", "--branch", "--untracked-files=all", "--find-renames")
	if err != nil {
		repo.Error = gitError(ctx, err)
		return repo
	}
	parseStatus(out, &repo)
	for _, area := range []string{agentproto.GitStaged, agentproto.GitUnstaged} {
		args := []string{"diff", "--numstat", "-z", "--find-renames", "--no-ext-diff", "--no-textconv"}
		if area == agentproto.GitStaged {
			args = append(args, "--cached")
		}
		if out, err := git(ctx, dir, args...); err == nil {
			addLines(out, area, &repo)
		}
	}
	return repo
}

// parseStatus reads git status -z --porcelain=v2 --branch.
func parseStatus(out []byte, repo *agentproto.GitRepo) {
	var unstaged, untracked []agentproto.GitChange
	add := func(c agentproto.GitChange) {
		if len(repo.Changes)+len(unstaged)+len(untracked) >= agentproto.MaxGitChanges {
			repo.Truncated = true
			return
		}
		switch c.Area {
		case agentproto.GitUnstaged:
			unstaged = append(unstaged, c)
		case agentproto.GitUntracked:
			untracked = append(untracked, c)
		default:
			repo.Changes = append(repo.Changes, c)
		}
	}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		switch {
		case strings.HasPrefix(e, "# branch.oid "):
			if oid := strings.TrimPrefix(e, "# branch.oid "); oid != "(initial)" {
				repo.Head = oid
			}
		case strings.HasPrefix(e, "# branch.head "):
			if head := strings.TrimPrefix(e, "# branch.head "); head != "(detached)" {
				repo.Branch = head
			}
		case strings.HasPrefix(e, "# branch.upstream "):
			repo.Upstream = strings.TrimPrefix(e, "# branch.upstream ")
		case strings.HasPrefix(e, "# branch.ab "):
			for f := range strings.FieldsSeq(strings.TrimPrefix(e, "# branch.ab ")) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					repo.Ahead = n
				} else {
					repo.Behind = n
				}
			}
		case strings.HasPrefix(e, "? "):
			add(agentproto.GitChange{Path: e[2:], Area: agentproto.GitUntracked, Status: "added"})
		case strings.HasPrefix(e, "1 "), strings.HasPrefix(e, "2 "):
			// 1 XY sub mH mI mW hH hI path; 2 adds a score before the path, and the old path
			// follows as an entry of its own.
			n := 9
			if e[0] == '2' {
				n = 10
			}
			f := strings.SplitN(e, " ", n)
			if len(f) < n || len(f[1]) != 2 {
				continue
			}
			from := ""
			if e[0] == '2' && i+1 < len(entries) {
				i++
				from = entries[i]
			}
			p := f[n-1]
			if x := f[1][0]; x != '.' {
				c := agentproto.GitChange{Path: p, Area: agentproto.GitStaged, Status: changeStatus(x)}
				if x == 'R' || x == 'C' {
					c.From = from
				}
				add(c)
			}
			if y := f[1][1]; y != '.' {
				add(agentproto.GitChange{Path: p, Area: agentproto.GitUnstaged, Status: changeStatus(y)})
			}
		case strings.HasPrefix(e, "u "):
			// u XY sub m1 m2 m3 mW h1 h2 h3 path: both sides changed it.
			if f := strings.SplitN(e, " ", 11); len(f) == 11 {
				add(agentproto.GitChange{Path: f[10], Area: agentproto.GitUnstaged, Status: "conflicted"})
			}
		}
	}
	repo.Changes = append(append(repo.Changes, unstaged...), untracked...)
}

func changeStatus(code byte) string {
	switch code {
	case 'A', 'C':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	}
	return "modified"
}

// addLines reads git diff --numstat -z into the changes of area: "added\tremoved\tpath\0",
// or for a rename "added\tremoved\t\0from\0to\0"; a binary file counts "-".
func addLines(out []byte, area string, repo *agentproto.GitRepo) {
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		f := strings.SplitN(entries[i], "\t", 3)
		if len(f) != 3 {
			continue
		}
		p := f[2]
		if p == "" && i+2 < len(entries) {
			p = entries[i+2]
			i += 2
		}
		for j := range repo.Changes {
			c := &repo.Changes[j]
			if c.Area != area || c.Path != p {
				continue
			}
			if f[0] == "-" {
				c.Binary = true
			} else {
				c.Added, _ = strconv.Atoi(f[0])
				c.Removed, _ = strconv.Atoi(f[1])
			}
			break
		}
	}
}

// gitDiff is the diff of one change in the repository at req.Path.
func gitDiff(ctx context.Context, workspace string, req agentproto.FilesRequest, git gitFunc) (agentproto.FilesReply, error) {
	ctx, cancel := context.WithTimeout(ctx, gitStatusTimeout)
	defer cancel()
	dir := workspace
	if req.Path != "" {
		rel, err := cleanName(req.Path)
		if err != nil {
			return agentproto.FilesReply{}, errors.New("invalid repository")
		}
		dir = path.Join(workspace, rel)
	}
	file, err := cleanName(req.File)
	if err != nil {
		return agentproto.FilesReply{}, errors.New("invalid file")
	}
	paths := []string{file}
	if req.From != "" {
		from, err := cleanName(req.From)
		if err != nil {
			return agentproto.FilesReply{}, errors.New("invalid file")
		}
		paths = []string{from, file}
	}
	args := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--find-renames"}
	switch req.Area {
	case agentproto.GitStaged:
		args = append(args, "--cached")
	case agentproto.GitUnstaged:
	case agentproto.GitUntracked:
		// A file git doesn't track is all added; --no-index exits 1 when the files differ.
		args = append(args, "--no-index")
		paths = []string{"/dev/null", file}
	default:
		return agentproto.FilesReply{}, errors.New("unknown area")
	}
	out, err := git(ctx, dir, append(append(args, "--"), paths...)...)
	var exit *exec.ExitError
	differ := req.Area == agentproto.GitUntracked && errors.As(err, &exit) && exit.ExitCode() == 1
	if err != nil && !differ {
		return agentproto.FilesReply{}, errors.New(gitError(ctx, err))
	}
	reply := agentproto.FilesReply{Diff: string(out)}
	if len(out) > agentproto.MaxGitDiff {
		cut := out[:agentproto.MaxGitDiff]
		if i := bytes.LastIndexByte(cut, '\n'); i > 0 {
			cut = cut[:i+1]
		}
		reply.Diff, reply.Truncated = string(cut), true
	}
	return reply, nil
}

// gitError says why git failed, without its whole output.
func gitError(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "git took too long"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "git exited with " + strconv.Itoa(exit.ExitCode())
	}
	return err.Error()
}
