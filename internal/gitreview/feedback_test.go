package gitreview

import (
	"errors"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestFeedbackTellsTheOutcomeAndTheComments(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "x.txt", "x\n", "Add x")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	_, msg, err := h.svc.Feedback(h.ctx, h.env.ID, a.ID, "")
	if err != nil || !strings.HasPrefix(msg, "Review of your push of ") || !strings.HasSuffix(msg, "it is still waiting for my review.") {
		t.Fatalf("feedback while pending = %q, %v", msg, err)
	}
	h.decide(t, a, integrations.Deny, "")
	p, msg, err := h.svc.Feedback(h.ctx, h.env.ID, a.ID, "  please add tests\nand docs  ")
	if err != nil || !strings.Contains(msg, "to main in owner/repo") || !strings.HasSuffix(msg, "rejected; nothing went upstream.\n\nMy comments:\nplease add tests\nand docs") {
		t.Fatalf("feedback after rejection = %q, %v", msg, err)
	}
	h.svc.FeedbackSent(h.ctx, p)
	out, err := callTool(t, h, g.sb, "git_push_status", `{}`)
	if err != nil || !strings.Contains(out, `"state":"rejected"`) {
		t.Fatalf("push status after feedback = %s, %v", out, err)
	}

	other, _, err := h.svc.Store.RequestApproval(h.ctx, store.Approval{EnvironmentID: h.env.ID, Kind: "network", Subject: "example.com:443", Payload: []byte("{}"), Status: store.StatusPending})
	if err != nil {
		t.Fatalf("request a network approval: %v", err)
	}
	if _, _, err := h.svc.Feedback(h.ctx, h.env.ID, other.ID, "x"); !errors.Is(err, ErrNotReview) {
		t.Fatalf("feedback on a network approval = %v, want ErrNotReview", err)
	}
}

// Comments left on a push that waits reach any agent of the sandbox: git_push_status and the
// push's outcome show them, the next prompt tells them once, and approving without a note of
// its own keeps them; then the next prompt says the push went up with them.
func TestCommentsLeftOnAWaitingPushReachTheAgent(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "x.txt", "x\n", "Add x")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	k, _ := integrations.Set{h.svc}.Kind(a.Kind)
	if v, settled, err := k.Outcome(h.ctx, a); err != nil || v != nil || settled {
		t.Fatalf("outcome of a waiting push without comments = %v, %v, %v; want nothing", v, settled, err)
	}

	if err := h.svc.LeaveNote(h.ctx, h.env.ID, a.ID, "  rename x.txt\n- x.txt:1 `x` → say why  "); err != nil {
		t.Fatal(err)
	}
	out, err := callTool(t, h, g.sb, "git_push_status", `{}`)
	if err != nil || !strings.Contains(out, `"state":"pending"`) || !strings.Contains(out, `"note":"rename x.txt\n- x.txt:1`) {
		t.Fatalf("push status with comments = %s, %v", out, err)
	}
	if v, settled, err := k.Outcome(h.ctx, a); err != nil || settled || v["note"] != "rename x.txt\n- x.txt:1 `x` → say why" || v["status"] != store.PushPending {
		t.Fatalf("outcome of a waiting push with comments = %v, %v, %v", v, settled, err)
	}
	note, done, err := h.svc.PromptNote(h.ctx, h.env.ID, g.sb.ID)
	if err != nil || !strings.Contains(note, "[Studio] The reviewer commented on your pushes:") ||
		!strings.Contains(note, `which still waits for review: "rename x.txt\n- x.txt:1`) || strings.Contains(note, "did not go upstream") {
		t.Fatalf("prompt note with comments = %q, %v", note, err)
	}
	done()
	if note, _, _ := h.svc.PromptNote(h.ctx, h.env.ID, g.sb.ID); note != "" {
		t.Fatalf("the comments were told twice: %q", note)
	}

	// Approving without a note of its own keeps them, and tells nothing new: the prompt carried
	// them already and the push went up.
	h.decide(t, a, integrations.Allow, "")
	out, err = callTool(t, h, g.sb, "git_push_status", `{}`)
	if err != nil || !strings.Contains(out, `"state":"pushed"`) || !strings.Contains(out, `"note":"rename x.txt`) {
		t.Fatalf("push status after approving = %s, %v", out, err)
	}
	if note, _, _ := h.svc.PromptNote(h.ctx, h.env.ID, g.sb.ID); note != "" {
		t.Fatalf("the same comments were told again after approving: %q", note)
	}
	if err := h.svc.LeaveNote(h.ctx, h.env.ID, a.ID, "more"); !errors.Is(err, ErrDecided) {
		t.Fatalf("comments on a decided push = %v, want ErrDecided", err)
	}
}

// A push approved with comments of its own tells them at the next prompt; one rejected after
// its comments were told is told again, as the rejection is news.
func TestApprovedOrRejectedAfterCommentsIsTold(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "x.txt", "x\n", "Add x")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	h.decide(t, h.approvals(t, KindPush, store.StatusPending)[0], integrations.Allow, "nice; next time split the commit")
	note, done, err := h.svc.PromptNote(h.ctx, h.env.ID, g.sb.ID)
	if err != nil || !strings.Contains(note, `approved and pushed: "nice; next time split the commit"`) {
		t.Fatalf("prompt note after approving with comments = %q, %v", note, err)
	}
	done()

	commitFile(t, repo, "y.txt", "y\n", "Add y")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	if err := h.svc.LeaveNote(h.ctx, h.env.ID, a.ID, "y.txt is empty"); err != nil {
		t.Fatal(err)
	}
	_, done, _ = h.svc.PromptNote(h.ctx, h.env.ID, g.sb.ID)
	done()
	h.decide(t, a, integrations.Deny, "")
	note, _, err = h.svc.PromptNote(h.ctx, h.env.ID, g.sb.ID)
	if err != nil || !strings.Contains(note, `was rejected by the reviewer: "y.txt is empty"`) {
		t.Fatalf("prompt note after rejecting a push whose comments were told = %q, %v", note, err)
	}
}
