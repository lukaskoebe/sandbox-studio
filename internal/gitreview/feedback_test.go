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
