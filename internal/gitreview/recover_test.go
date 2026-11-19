package gitreview

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A push approved while Studio stopped before pushing it, and the pull request it then
// proposes, fail at the next start with what to do, and can be pushed and opened again. A
// later push to the branch meanwhile doesn't replace the approved one: it waits beside it.
func TestRecoverFailsAPushStudioStoppedInTheMiddleOf(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	c1 := commitFile(t, repo, "a.txt", "a\n", "Add a")
	if err := push(repo, "refs/heads/main:refs/heads/feature", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	if err := h.st.DecideApproval(h.ctx, h.env.ID, a.ID, store.StatusApproved, ""); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "b.txt", "b\n", "Add b")
	if err := push(repo, "+refs/heads/main:refs/heads/feature", true); err != nil {
		t.Fatalf("later push: %v", err)
	}
	if pending := h.approvals(t, KindPush, store.StatusPending); len(pending) != 1 || pending[0].ID == a.ID {
		t.Fatalf("pending = %+v", pending)
	}
	if p, _ := h.st.GitPushByApproval(h.ctx, h.env.ID, a.ID); p.State != store.PushPending {
		t.Fatalf("a later push replaced the approved one: %s", p.State)
	}

	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := h.st.GitPushByApproval(h.ctx, h.env.ID, a.ID)
	if p.State != store.PushFailed || p.Result != stoppedPushing {
		t.Fatalf("push after recovery = %+v", p)
	}
	if p, err := h.svc.PushAgain(h.ctx, h.env.ID, a.ID); err != nil || p.State != store.PushPushed || h.forge.head(t, "feature") != c1 {
		t.Fatalf("push again = %+v, %v", p, err)
	}

	prs := h.approvals(t, KindPR, store.StatusPending)
	if len(prs) != 1 {
		t.Fatalf("git.pr approvals = %d", len(prs))
	}
	if err := h.st.DecideApproval(h.ctx, h.env.ID, prs[0].ID, store.StatusApproved, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ = h.st.GitPushByApproval(h.ctx, h.env.ID, a.ID); p.PRURL != "" || p.PRResult != "failed: "+stoppedOpening {
		t.Fatalf("pull request after recovery = %+v", p)
	}
	if p, err := h.svc.OpenPullRequest(h.ctx, h.env.ID, a.ID, "", false); err != nil || p.PRResult != "opened" || len(h.forge.pulls) != 1 {
		t.Fatalf("open it again = %+v, %v", p, err)
	}
	// Run again, it finds nothing.
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ = h.st.GitPushByApproval(h.ctx, h.env.ID, a.ID); p.State != store.PushPushed || p.PRResult != "opened" {
		t.Fatalf("push after a second recovery = %+v", p)
	}
}

// A push replaced by a later one before the two were settled together can't be approved: its
// card goes, and the decision says which to review.
func TestDecidingAReplacedPushDismissesIt(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "a.txt", "a\n", "Add a")
	if err := push(repo, "refs/heads/main:refs/heads/feature", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	p, _ := h.st.GitPushByApproval(h.ctx, h.env.ID, a.ID)
	if err := h.st.SettleGitPush(h.ctx, h.env.ID, p.ID, store.PushPending, store.PushSuperseded, "replaced by a later push", ""); err != nil {
		t.Fatal(err)
	}
	k, _ := integrations.Set{h.svc}.Kind(a.Kind)
	if err := k.Decide(h.ctx, a, integrations.Decision{Action: integrations.Allow}); !errors.Is(err, ErrReplaced) {
		t.Fatalf("approve a replaced push = %v", err)
	}
	if got, _ := h.st.Approval(h.ctx, h.env.ID, a.ID); got.Status != store.StatusDismissed {
		t.Fatalf("its card = %s", got.Status)
	}
	if !h.forge.head(t, "feature").IsZero() {
		t.Fatal("a replaced push went upstream")
	}
}

// A review or comment approved while Studio stopped before posting it fails at the next start,
// so the agent's wait ends. A review approved meanwhile isn't replaced by a newer draft.
func TestRecoverFailsAReviewAndCommentStudioStoppedPosting(t *testing.T) {
	svc, st, sb, _ := newReviewHarness(t)
	ctx := context.Background()
	draft := `{"repository":"acme/web","number":42,"verdict":"comment","summary":"Looks fine."}`
	first, err := callReviewTool(t, svc.toolPullRequestReview, sb, draft)
	if err != nil {
		t.Fatal(err)
	}
	review := first["approval"].(string)
	if err := st.DecideApproval(ctx, sb.EnvironmentID, review, store.StatusApproved, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := callReviewTool(t, svc.toolPullRequestReview, sb, `{"repository":"acme/web","number":42,"verdict":"comment","summary":"Second thoughts."}`); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.GitReviewByApproval(ctx, sb.EnvironmentID, review); r.State != store.ReviewPending {
		t.Fatalf("a newer draft replaced the approved review: %s", r.State)
	}
	got, err := callReviewTool(t, svc.toolPullRequestComment, sb, `{"repository":"acme/web","number":42,"body":"Thanks."}`)
	if err != nil {
		t.Fatal(err)
	}
	comment := got["approval"].(string)
	if err := st.DecideApproval(ctx, sb.EnvironmentID, comment, store.StatusApproved, ""); err != nil {
		t.Fatal(err)
	}

	if err := svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.GitReviewByApproval(ctx, sb.EnvironmentID, review); r.State != store.ReviewFailed || r.Result != stoppedPosting {
		t.Fatalf("review after recovery = %+v", r)
	}
	a, _ := st.Approval(ctx, sb.EnvironmentID, comment)
	res, settled, err := svc.commentOutcome(ctx, a)
	if err != nil || !settled || res["status"] != CommentFailed {
		t.Fatalf("comment after recovery = %v, %v, %v", res, settled, err)
	}
	// The newer draft still waits for its answer.
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	if len(pending) != 1 || pending[0].Kind != KindReview {
		t.Fatalf("pending = %+v", pending)
	}
}
