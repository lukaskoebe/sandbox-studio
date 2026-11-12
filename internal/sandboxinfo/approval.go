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
	// Result is what the decision did, once it is settled, for the kinds that do something.
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
	if a.Status == store.StatusPending {
		return out, nil
	}
	out.Settled, out.Decided = true, a.UpdatedAt
	if s.Kinds == nil {
		return out, nil
	}
	if k, ok := s.Kinds(a.Kind); ok && k.Outcome != nil {
		result, settled, err := k.Outcome(ctx, a)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		out.Result, out.Settled = result, settled || errors.Is(err, store.ErrNotFound)
	}
	return out, nil
}
