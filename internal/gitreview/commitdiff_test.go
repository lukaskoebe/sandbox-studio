package gitreview

import (
	"errors"
	"strings"
	"testing"

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
