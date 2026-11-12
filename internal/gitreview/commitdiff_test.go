package gitreview

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// TestCommitDiff: each commit of a push shows its own changes, before and after approval;
// a commit the review doesn't list isn't shown.
func TestCommitDiff(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "a.txt", "a\n", "Add a")
	commitFile(t, repo, "b.txt", "b\n", "Add b")
	if err := push(repo, "refs/heads/main:refs/heads/feature", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	d, _ := h.svc.GitDetail(h.ctx, a)
	if len(d.Review.Commits) != 2 {
		t.Fatalf("commits = %+v", d.Review.Commits)
	}
	check := func() {
		t.Helper()
		for _, c := range []struct {
			i          int
			has, lacks string
		}{{0, "b.txt", "a.txt"}, {1, "a.txt", "b.txt"}} {
			sha := d.Review.Commits[c.i].SHA
			cd, err := h.svc.CommitDiff(h.ctx, h.env.ID, a.ID, sha)
			if err != nil {
				t.Fatal(err)
			}
			if cd.Commit.SHA != sha || cd.Files != 1 || cd.Additions != 1 || !strings.Contains(cd.Diff, "+++ b/"+c.has) || strings.Contains(cd.Diff, c.lacks) {
				t.Fatalf("commit %d: %+v", c.i, cd)
			}
		}
	}
	check()
	if _, err := h.svc.CommitDiff(h.ctx, h.env.ID, a.ID, strings.Repeat("0", 40)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a commit the review doesn't list: %v", err)
	}
	h.decide(t, a, integrations.Allow, "")
	check()
}

// TestNewestFirst: commits made in the same second still list each before its parents, and
// otherwise the newest first.
func TestNewestFirst(t *testing.T) {
	at := time.Date(2026, 11, 12, 13, 44, 29, 0, time.UTC)
	mk := func(name string, when time.Time, parents ...*object.Commit) *object.Commit {
		c := &object.Commit{Hash: plumbing.NewHash(strings.Repeat(name, 40)), Message: name}
		c.Committer.When = when
		for _, p := range parents {
			c.ParentHashes = append(c.ParentHashes, p.Hash)
		}
		return c
	}
	base := mk("0", at.Add(-time.Hour)) // the forge has it: not in the list
	a := mk("a", at, base)
	b := mk("b", at, a)
	c := mk("c", at, b)
	side := mk("d", at.Add(-time.Minute), base)
	merge := mk("e", at, c, side)
	for _, in := range [][]*object.Commit{{a, b, c, side, merge}, {side, a, merge, c, b}, {c, side, b, merge, a}} {
		var got []string
		for _, x := range newestFirst(in) {
			got = append(got, x.Message)
		}
		if want := []string{"e", "c", "b", "a", "d"}; !slices.Equal(got, want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
