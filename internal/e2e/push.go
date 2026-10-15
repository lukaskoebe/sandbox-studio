//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Push is what the guest pushes: one commit that writes File on top of Branch of the
// repository at URL, usually https://git.studio.internal/<forge>/<path>.git.
type Push struct {
	URL     string `json:"url"`
	Branch  string `json:"branch"`
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
	g.mu.Lock()
	egress, cfg := g.egress, g.config
	g.mu.Unlock()
	var out Pushed
	fail := func(err error) Pushed {
		out.Error = err.Error()
		return out
	}
	if egress.Proxy == "" {
		return fail(errors.New("the sandbox has no gateway"))
	}
	if cfg == nil || cfg.CA == "" {
		return fail(errors.New("the guest has no CA yet"))
	}
	if p.Branch == "" {
		p.Branch = "main"
	}
	proxy := transport.ProxyOptions{URL: "socks5://" + egress.Proxy, Username: egress.User, Password: os.Getenv(egress.PasswordEnv)}
	ca := []byte(cfg.CA)
	var progress bytes.Buffer
	repo, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{
		URL: p.URL, ReferenceName: plumbing.NewBranchReferenceName(p.Branch), SingleBranch: true,
		CABundle: ca, ProxyOptions: proxy, Progress: &progress,
	})
	if err != nil {
		return fail(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fail(err)
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
	ref := config.RefSpec("refs/heads/" + p.Branch + ":refs/heads/" + p.Branch)
	err = repo.PushContext(ctx, &git.PushOptions{RefSpecs: []config.RefSpec{ref}, CABundle: ca, ProxyOptions: proxy, Progress: &progress})
	out.Output = progress.String()
	if err != nil {
		return fail(err)
	}
	return out
}
