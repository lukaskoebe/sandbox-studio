package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// CommitDiff is one commit of a push with its own changes: from its first parent, as a
// forge shows a commit.
type CommitDiff struct {
	Commit Commit `json:"commit"`
	TreeDiff
}

// CommitDiff reads one of the commits a push review lists from the push's staging
// repository. A commit the review doesn't list, or one the repository no longer has, is
// not found.
func (s *Service) CommitDiff(ctx context.Context, envID, approvalID, sha string) (CommitDiff, error) {
	a, err := s.Store.Approval(ctx, envID, approvalID)
	if err != nil {
		return CommitDiff{}, err
	}
	if a.Kind != KindPush {
		return CommitDiff{}, ErrNotReview
	}
	var rv PushReview
	if err := json.Unmarshal(a.Payload, &rv); err != nil {
		return CommitDiff{}, err
	}
	i := slices.IndexFunc(rv.Commits, func(c Commit) bool { return c.SHA == sha })
	if i < 0 {
		return CommitDiff{}, fmt.Errorf("the push has no commit %s: %w", sha, store.ErrNotFound)
	}
	p, err := s.Store.GitPushByApproval(ctx, envID, approvalID)
	if err != nil {
		return CommitDiff{}, err
	}
	t := target{sb: store.Sandbox{ID: p.SandboxID, EnvironmentID: envID}, forge: store.Forge{ID: p.ForgeID}, owner: p.Owner, repo: p.Repo}
	path := s.stagingPath(t)
	unlock := s.lock(path)
	defer unlock()
	r, err := git.PlainOpen(path)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return CommitDiff{}, fmt.Errorf("the push's repository is no longer kept: %w", store.ErrNotFound)
	}
	if err != nil {
		return CommitDiff{}, err
	}
	c, err := r.CommitObject(plumbing.NewHash(sha))
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		return CommitDiff{}, fmt.Errorf("the commit is no longer kept: %w", store.ErrNotFound)
	}
	if err != nil {
		return CommitDiff{}, err
	}
	to, err := c.Tree()
	if err != nil {
		return CommitDiff{}, err
	}
	from := &object.Tree{}
	if c.NumParents() > 0 {
		parent, err := c.Parent(0)
		if err != nil {
			return CommitDiff{}, err
		}
		if from, err = parent.Tree(); err != nil {
			return CommitDiff{}, err
		}
	}
	d, err := s.treeDiff(ctx, r, from, to)
	return CommitDiff{Commit: rv.Commits[i], TreeDiff: d}, err
}
