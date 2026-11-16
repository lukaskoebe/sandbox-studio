package sandboxinfo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ApprovalStatus is what approval_status returns (docs/agent-cli.md#waiting-for-the-user).
type ApprovalStatus struct {
	Approval string    `json:"approval"`
	Kind     string    `json:"kind"`
	Subject  string    `json:"subject"`
	Status   string    `json:"status"`
	Settled  bool      `json:"settled"`
	Decided  time.Time `json:"decided,omitzero"`
	// Result is what the decision did, once it is settled, for the kinds that do something;
	// while a push waits, the reviewer's comments left so far.
	Result map[string]any `json:"result,omitempty"`
}

// approvalStatus answers approval_status for an approval of the calling sandbox only: another
// sandbox's reads as unknown.
func (s *Service) approvalStatus(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Approval string `json:"approval"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, integrations.ToolError("invalid arguments: %v", err)
	}
	id := strings.TrimSpace(in.Approval)
	if id == "" {
		return nil, integrations.ToolError("give the approval's ID")
	}
	a, err := s.Store.Approval(ctx, sb.EnvironmentID, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && a.SandboxID != sb.ID) {
		return nil, integrations.ToolError("this sandbox has no approval %s", id)
	}
	if err != nil {
		return nil, err
	}
	out := ApprovalStatus{Approval: a.ID, Kind: a.Kind, Subject: a.Subject, Status: a.Status}
	var k integrations.ApprovalKind
	if s.Kinds != nil {
		k, _ = s.Kinds(a.Kind)
	}
	if a.Status == store.StatusPending {
		// A kind may say something while it waits, as a push does the reviewer's comments.
		if k.Outcome != nil {
			if result, _, err := k.Outcome(ctx, a); err == nil {
				out.Result = result
			}
		}
		return out, nil
	}
	out.Settled, out.Decided = true, a.UpdatedAt
	if k.Outcome != nil {
		result, settled, err := k.Outcome(ctx, a)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		out.Result, out.Settled = result, settled || errors.Is(err, store.ErrNotFound)
	}
	return out, nil
}
