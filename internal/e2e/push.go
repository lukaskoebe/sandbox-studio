//go:build e2e

package e2e

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Push is what the guest pushes: one commit that writes File on top of Branch of the
// repository at URL, usually https://git.studio.internal/<forge>/<path>.git. From starts a
// new branch from another one, and Amend replaces the branch's last commit instead, which
// makes the push a forced one.
type Push struct {
	URL     string `json:"url"`
	Branch  string `json:"branch"`
	From    string `json:"from"`
	Amend   bool   `json:"amend"`
	File    string `json:"file"`
	Content string `json:"content"`
	Message string `json:"message"`
}

// Pushed is how a push went: the commit and what the remote said.
type Pushed struct {
	Commit string `json:"commit,omitempty"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// Push clones p.URL through the sandbox's gateway, as git in a sandbox would, commits one
// file and pushes the branch. The gateway serves git.studio.internal with the environment
// CA from the guest's config, so Studio's virtual git remote gets the push and holds it for
// review.
func (g *Guest) Push(ctx context.Context, p Push) Pushed {
	var out Pushed
	fail := func(err error) Pushed {
		out.Error = err.Error()
		return out
	}
	proxy, ca, err := g.gateway()
	if err != nil {
		return fail(err)
	}
	if p.Branch == "" {
		p.Branch = "main"
	}
	from := cmp.Or(p.From, p.Branch)
	var progress bytes.Buffer
	repo, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{
		URL: p.URL, ReferenceName: plumbing.NewBranchReferenceName(from), SingleBranch: true,
		CABundle: ca, ProxyOptions: proxy, Progress: &progress,
	})
	if err != nil {
		return fail(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fail(err)
	}
	if p.Amend {
		head, err := repo.Head()
		if err != nil {
			return fail(err)
		}
		tip, err := repo.CommitObject(head.Hash())
		if err != nil {
			return fail(err)
		}
		parent, err := tip.Parent(0)
		if err != nil {
			return fail(err)
		}
		if err := wt.Reset(&git.ResetOptions{Commit: parent.Hash, Mode: git.HardReset}); err != nil {
			return fail(err)
		}
	}
	f, err := wt.Filesystem.Create(p.File)
	if err != nil {
		return fail(err)
	}
	_, err = io.WriteString(f, p.Content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fail(err)
	}
	if _, err := wt.Add(p.File); err != nil {
		return fail(err)
	}
	sig := &object.Signature{Name: "Iris", Email: "iris@sandbox.test", When: time.Now()}
	commit, err := wt.Commit(p.Message, &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		return fail(err)
	}
	out.Commit = commit.String()
	progress.Reset()
	ref := config.RefSpec("refs/heads/" + from + ":refs/heads/" + p.Branch)
	if p.Amend {
		ref = "+" + ref
	}
	err = repo.PushContext(ctx, &git.PushOptions{RefSpecs: []config.RefSpec{ref}, Force: p.Amend, CABundle: ca, ProxyOptions: proxy, Progress: &progress})
	out.Output = progress.String()
	if err != nil {
		return fail(err)
	}
	return out
}

// gateway is how git in the guest reaches Studio: the sandbox's gateway as a SOCKS proxy, and
// the environment CA from the guest's config, which git.studio.internal is served with.
func (g *Guest) gateway() (transport.ProxyOptions, []byte, error) {
	g.mu.Lock()
	egress, cfg := g.egress, g.config
	g.mu.Unlock()
	if egress.Proxy == "" {
		return transport.ProxyOptions{}, nil, errors.New("the sandbox has no gateway")
	}
	if cfg == nil || cfg.CA == "" {
		return transport.ProxyOptions{}, nil, errors.New("the guest has no CA yet")
	}
	proxy := transport.ProxyOptions{URL: "socks5://" + egress.Proxy, Username: egress.User, Password: os.Getenv(egress.PasswordEnv)}
	return proxy, []byte(cfg.CA), nil
}

// runGit fakes the git clone Studio runs to clone a repository into the workspace
// (POST .../sandboxes/{id}/clone): it clones the URL through the gateway into memory, as git
// in a sandbox would, and records the folder the clone went to.
func (g *Guest) runGit(ctx context.Context, req agentproto.RunRequest) agentproto.RunResult {
	a := req.Args
	if len(a) != 5 || a[1] != "clone" || a[2] != "--" {
		return agentproto.RunResult{ExitCode: 127, Stderr: "the e2e guest doesn't run " + strings.Join(a, " ")}
	}
	url, dir := a[3], a[4]
	g.mu.Lock()
	taken := slices.Contains(g.clones, dir)
	g.mu.Unlock()
	if taken {
		return agentproto.RunResult{ExitCode: 128, Stderr: "fatal: destination path '" + dir + "' already exists and is not an empty directory.\n"}
	}
	proxy, ca, err := g.gateway()
	if err == nil {
		_, err = git.CloneContext(ctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: url, CABundle: ca, ProxyOptions: proxy})
	}
	if err != nil && !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return agentproto.RunResult{ExitCode: 128, Stderr: "Cloning into '" + dir + "'...\nfatal: " + err.Error() + "\n"}
	}
	g.mu.Lock()
	g.clones = append(g.clones, dir)
	g.mu.Unlock()
	return agentproto.RunResult{Stderr: "Cloning into '" + dir + "'...\n"}
}
