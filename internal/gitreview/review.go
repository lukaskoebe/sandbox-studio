package gitreview

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/revlist"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// PushReview is the payload of a git.push approval: what the reviewer sees.
type PushReview struct {
	Teammate    string   `json:"teammate,omitempty" doc:"The teammate owning the sandbox; empty for unowned sandboxes"`
	Sandbox     string   `json:"sandbox" doc:"The sandbox's name"`
	Forge       string   `json:"forge"`
	Owner       string   `json:"owner" doc:"The repository path before its last segment; may contain slashes or be empty"`
	Repo        string   `json:"repo"`
	Upstream    string   `json:"upstream,omitempty" doc:"How Studio pushes once approved, such as over SSH to ssh://git@host/repo with the pinned host key"`
	Branch      string   `json:"branch"`
	Old         string   `json:"old" doc:"The branch on the forge before the push; empty for a new branch"`
	New         string   `json:"new"`
	Commits     []Commit `json:"commits" doc:"New commits, newest first"`
	MoreCommits int      `json:"moreCommits" doc:"New commits not listed"`
	Files       int      `json:"files" doc:"Files changed"`
	Additions   int      `json:"additions"`
	Deletions   int      `json:"deletions"`
	Diff        string   `json:"diff" doc:"Unified diff from the branch's previous state (or where a new or rewritten branch forked) to the push"`
	Base        string   `json:"base,omitempty" doc:"For a new or rewritten branch, the forge's commit the diff starts from"`
	BaseBranch  string   `json:"baseBranch,omitempty" doc:"The forge's branch whose tip Base is, if any"`
	// A forced push rewrites a branch the forge has; it drops commits.
	Forced        bool     `json:"forced,omitempty" doc:"The push rewrites the branch: Old is not an ancestor of New. Approving it takes force"`
	Dropped       []Commit `json:"dropped,omitempty" doc:"For a forced push, the commits on the branch that New doesn't contain, newest first"`
	MoreDropped   int      `json:"moreDropped,omitempty" doc:"Dropped commits not listed"`
	TipFiles      []string `json:"tipFiles,omitempty" doc:"For a forced push, the files whose content differs between Old and New"`
	MoreTipFiles  int      `json:"moreTipFiles,omitempty" doc:"Files that differ between Old and New, not listed"`
	DiffTruncated bool     `json:"diffTruncated"`
	// The pull request approving can open along with the push, and the one it updates.
	PullRequest     *PullRequest `json:"pullRequest,omitempty" doc:"The pull request the push can open: set for a branch other than the repository's default, on a forge with an API, when none is open from it"`
	OpenPullRequest string       `json:"openPullRequest,omitempty" doc:"Web URL of the open pull request from the branch, which the push updates"`
}

// Commit is one commit of a push.
type Commit struct {
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	When    time.Time `json:"when"`
	Subject string    `json:"subject"`
	Body    string    `json:"body,omitempty"`
	// ReplacedBy, for a dropped commit, is the new commit with its subject and author.
	ReplacedBy string `json:"replacedBy,omitempty" doc:"For a dropped commit, the pushed commit with the same subject and author, as a rebase leaves it"`
}

// maxTipFiles is how many files differing between a rewritten branch's tips a review lists.
const maxTipFiles = 20

const truncMarker = "\n[... diff truncated by Sandbox Studio: %s ...]\n"

// review describes the commits a push adds on top of what the forge has, and their diff.
// newObjs are the objects reachable from the pushed commit but not from the forge's refs.
func (s *Service) review(ctx context.Context, r *git.Repository, old, newHash plumbing.Hash, newObjs []plumbing.Hash) (PushReview, error) {
	var rv PushReview
	inNew := make(map[plumbing.Hash]bool, len(newObjs))
	var commits []*object.Commit
	for _, h := range newObjs {
		c, err := r.CommitObject(h)
		if err != nil {
			continue // a tree or blob
		}
		inNew[h] = true
		commits = append(commits, c)
	}
	sort.SliceStable(commits, func(i, j int) bool { return commits[i].Committer.When.After(commits[j].Committer.When) })
	for i, c := range commits {
		if i >= s.Limits.MaxCommits {
			rv.MoreCommits = len(commits) - i
			break
		}
		rv.Commits = append(rv.Commits, commitOf(c))
	}

	// The diff base is the branch's previous state, or for a new branch the first commit
	// down the first-parent chain that the forge already has.
	newCommit, err := r.CommitObject(newHash)
	if err != nil {
		return rv, err
	}
	var base *object.Commit
	if !old.IsZero() {
		base, err = r.CommitObject(old)
		if err != nil {
			return rv, err
		}
	} else {
		c := newCommit
		for steps := 0; inNew[c.Hash] && steps < 100000; steps++ {
			if c.NumParents() == 0 {
				c = nil
				break
			}
			if c, err = c.Parent(0); err != nil {
				return rv, err
			}
		}
		base = c
		if base != nil {
			rv.Base = base.Hash.String()
		}
	}
	to, err := newCommit.Tree()
	if err != nil {
		return rv, err
	}
	from := &object.Tree{}
	if base != nil {
		if from, err = base.Tree(); err != nil {
			return rv, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	changes, err := object.DiffTreeWithOptions(ctx, from, to, object.DefaultDiffTreeOptions)
	if err != nil {
		return rv, err
	}
	rv.Files = len(changes)
	var diff strings.Builder
	for i, ch := range changes {
		if int64(diff.Len()) >= s.Limits.MaxDiff {
			rv.DiffTruncated = true
			fmt.Fprintf(&diff, truncMarker, fmt.Sprintf("%d more files not shown", len(changes)-i))
			break
		}
		text, add, del := s.changeDiff(ctx, r, ch)
		rv.Additions += add
		rv.Deletions += del
		if room := s.Limits.MaxDiff - int64(diff.Len()); int64(len(text)) > room {
			text = text[:room]
			if k := strings.LastIndexByte(text, '\n'); k > 0 {
				text = text[:k+1]
			}
			diff.WriteString(text)
			rv.DiffTruncated = true
			fmt.Fprintf(&diff, truncMarker, fmt.Sprintf("diff larger than %d KiB", s.Limits.MaxDiff>>10))
			if rest := len(changes) - i - 1; rest > 0 {
				fmt.Fprintf(&diff, "[%d more files not shown]\n", rest)
			}
			break
		}
		diff.WriteString(text)
	}
	rv.Diff = diff.String()
	return rv, nil
}

func commitOf(c *object.Commit) Commit {
	subject, body, _ := strings.Cut(strings.TrimSpace(c.Message), "\n")
	return Commit{
		SHA: c.Hash.String(), Author: c.Author.Name, Email: c.Author.Email, When: c.Author.When,
		Subject: subject, Body: truncate(strings.TrimSpace(body), 4<<10),
	}
}

// rewrite describes what a forced push drops: the commits of old that newHash doesn't
// contain, each with the pushed commit that replaces it where a rebase kept its subject and
// author, and the files whose content differs between the two tips.
func (s *Service) rewrite(ctx context.Context, r *git.Repository, rv *PushReview, old, newHash plumbing.Hash, newObjs []plumbing.Hash) error {
	rv.Forced = true
	dropped, err := revlist.Objects(r.Storer, []plumbing.Hash{old}, []plumbing.Hash{newHash})
	if err != nil {
		return err
	}
	type key struct{ subject, email string }
	replaced := map[key]string{}
	for _, h := range newObjs {
		if c, err := r.CommitObject(h); err == nil {
			cm := commitOf(c)
			replaced[key{cm.Subject, cm.Email}] = cm.SHA
		}
	}
	isDropped := map[plumbing.Hash]bool{}
	for _, h := range dropped {
		if _, err := r.Storer.EncodedObject(plumbing.CommitObject, h); err == nil {
			isDropped[h] = true
		}
	}
	// In the order git log lists them from the old tip, which keeps a parent after its child.
	want := min(len(isDropped), s.Limits.MaxCommits)
	iter, err := r.Log(&git.LogOptions{From: old})
	if err != nil {
		return err
	}
	err = iter.ForEach(func(c *object.Commit) error {
		if len(rv.Dropped) >= want {
			return storer.ErrStop
		}
		if isDropped[c.Hash] {
			cm := commitOf(c)
			cm.ReplacedBy = replaced[key{cm.Subject, cm.Email}]
			rv.Dropped = append(rv.Dropped, cm)
		}
		return nil
	})
	iter.Close()
	if err != nil {
		return err
	}
	rv.MoreDropped = len(isDropped) - len(rv.Dropped)

	oldCommit, err := r.CommitObject(old)
	if err != nil {
		return err
	}
	newCommit, err := r.CommitObject(newHash)
	if err != nil {
		return err
	}
	from, err := oldCommit.Tree()
	if err != nil {
		return err
	}
	to, err := newCommit.Tree()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	changes, err := object.DiffTreeWithOptions(ctx, from, to, object.DefaultDiffTreeOptions)
	if err != nil {
		return err
	}
	for i, ch := range changes {
		if i >= maxTipFiles {
			rv.MoreTipFiles = len(changes) - i
			break
		}
		name := ch.To.Name
		if name == "" {
			name = ch.From.Name
		}
		rv.TipFiles = append(rv.TipFiles, name)
	}
	return nil
}

// changeDiff renders one file's change, summarizing large files and submodules.
func (s *Service) changeDiff(ctx context.Context, r *git.Repository, ch *object.Change) (string, int, int) {
	name := ch.To.Name
	if name == "" {
		name = ch.From.Name
	}
	for _, e := range []object.ChangeEntry{ch.From, ch.To} {
		if e.Name == "" {
			continue
		}
		if e.TreeEntry.Mode == filemode.Submodule {
			return fmt.Sprintf("diff --git a/%s b/%s\n[submodule %s changed]\n", name, name, name), 0, 0
		}
		if size, err := r.Storer.EncodedObjectSize(e.TreeEntry.Hash); err == nil && size > s.Limits.MaxBlob {
			return fmt.Sprintf("diff --git a/%s b/%s\n[%s: %d bytes, larger than %d KiB; not shown]\n", name, name, name, size, s.Limits.MaxBlob>>10), 0, 0
		}
	}
	p, err := ch.PatchContext(ctx)
	if err != nil {
		return fmt.Sprintf("diff --git a/%s b/%s\n[could not diff: %v]\n", name, name, err), 0, 0
	}
	add, del := 0, 0
	for _, st := range p.Stats() {
		add += st.Addition
		del += st.Deletion
	}
	return p.String(), add, del
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// prProposal derives a pull request from a push's review.
func prProposal(rv PushReview, base string) PullRequest {
	pr := PullRequest{Head: rv.Branch, Base: base, Title: rv.Branch}
	if len(rv.Commits) == 1 && rv.MoreCommits == 0 {
		pr.Title = rv.Commits[0].Subject
		pr.Body = rv.Commits[0].Body
	} else {
		var b strings.Builder
		for i := len(rv.Commits) - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "- %s (%s)\n", rv.Commits[i].Subject, short(rv.Commits[i].SHA))
		}
		if rv.MoreCommits > 0 {
			fmt.Fprintf(&b, "- and %d earlier commits\n", rv.MoreCommits)
		}
		pr.Body = b.String()
	}
	if rv.Teammate != "" {
		pr.Body = strings.TrimSpace(pr.Body + "\n\nPushed by " + rv.Teammate + " from sandbox " + rv.Sandbox + " through Sandbox Studio.")
	}
	pr.Title = truncate(pr.Title, 200)
	return pr
}
