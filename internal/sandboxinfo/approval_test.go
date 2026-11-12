package sandboxinfo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// approval_status answers for the calling sandbox's approvals only, and settles a decided one
// once its kind says what the decision did.
func TestApprovalStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := st.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: "git.review", Subject: "gh/o/r#9", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	theirs, _, err := st.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, SandboxID: other.ID, Kind: "git.review", Subject: "gh/o/r#9", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	posted := false
	s := &Service{Store: st, Kinds: func(kind string) (integrations.ApprovalKind, bool) {
		return integrations.ApprovalKind{Kind: kind, Outcome: func(context.Context, store.Approval) (map[string]any, bool, error) {
			if !posted {
				return nil, false, nil
			}
			return map[string]any{"status": "posted"}, true, nil
		}}, true
	}}
	ask := func(id string) (ApprovalStatus, error) {
		out, err := s.approvalStatus(ctx, sb, json.RawMessage(`{"approval":"`+id+`"}`))
		if err != nil {
			return ApprovalStatus{}, err
		}
		return out.(ApprovalStatus), nil
	}
	if _, err := ask(theirs.ID); !integrations.IsToolError(err) {
		t.Fatalf("another sandbox's approval: %v", err)
	}
	if got, err := ask(a.ID); err != nil || got.Status != store.StatusPending || got.Settled {
		t.Fatalf("pending: %+v %v", got, err)
	}
	if err := st.DecideApproval(ctx, env.ID, a.ID, store.StatusApproved, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := ask(a.ID); err != nil || got.Status != store.StatusApproved || got.Settled || got.Result != nil {
		t.Fatalf("still posting: %+v %v", got, err)
	}
	posted = true
	if got, err := ask(a.ID); err != nil || !got.Settled || got.Result["status"] != "posted" {
		t.Fatalf("posted: %+v %v", got, err)
	}
}
