package gitreview

import (
	"context"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestPushPullRequestReadsTheBranchsPullRequest(t *testing.T) {
	svc, st, sb, _ := newReviewHarness(t)
	ctx := context.Background()
	f, err := st.ForgeByName(ctx, sb.EnvironmentID, "forge")
	if err != nil {
		t.Fatal(err)
	}
	push := func(ref string) store.GitPush {
		p, err := st.CreateGitPush(ctx, store.GitPush{ID: store.NewID(), EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID,
			ForgeID: f.ID, ForgeName: f.Name, Owner: "acme", Repo: "web", Ref: ref, DefaultBranch: "main", ApprovalID: store.NewID()})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	waiting := push("refs/heads/feature/coupons")
	if got, err := svc.PushPullRequest(ctx, sb.EnvironmentID, waiting.ApprovalID); err != nil || got.Found || got.Note != "It wasn't pushed." {
		t.Fatalf("a push still waiting: %+v, %v", got, err)
	}

	p := push("refs/heads/feature/coupons")
	if err := st.SettleGitPush(ctx, sb.EnvironmentID, p.ID, store.PushPending, store.PushPushed, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := svc.PushPullRequest(ctx, sb.EnvironmentID, p.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	// One conversation comment, one line comment and one review with words; the checks failed.
	want := PushPR{Found: true, Number: 42, URL: "https://forge.example/acme/web/pulls/42", State: "open", Checks: CheckFailure, Comments: 3}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	main := push("refs/heads/main")
	if err := st.SettleGitPush(ctx, sb.EnvironmentID, main.ID, store.PushPending, store.PushPushed, "", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.PushPullRequest(ctx, sb.EnvironmentID, main.ApprovalID); err != nil || got.Found || got.Note != "It went straight to main." {
		t.Fatalf("a push to the default branch: %+v, %v", got, err)
	}
	if _, err := svc.PushPullRequest(ctx, sb.EnvironmentID, "nope"); err != store.ErrNotFound {
		t.Fatalf("an unknown approval: %v", err)
	}
}
