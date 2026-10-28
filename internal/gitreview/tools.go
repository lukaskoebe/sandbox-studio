package gitreview

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Tools are the MCP tools of the git remote. The push and pull request tools answer from
// Studio's records of the calling sandbox's pushes and never ask a forge; the list and review
// tools read repositories and pull requests from it.
func (s *Service) Tools() []integrations.Tool {
	return []integrations.Tool{
		{Def: agentcall.GitPushStatusTool, Handle: s.toolPushStatus},
		{Def: agentcall.GitPullRequestStatusTool, Handle: s.toolPullRequestStatus},
		{Def: agentcall.GitRepositoriesTool, Handle: s.toolRepositories},
		{Def: agentcall.GitPullRequestListTool, Handle: s.toolPullRequestList},
		{Def: agentcall.GitPullRequestReadTool, Handle: s.toolPullRequestRead},
		{Def: agentcall.GitPullRequestReviewTool, Handle: s.toolPullRequestReview},
		{Def: agentcall.GitReviewStatusTool, Handle: s.toolReviewStatus},
		// Not an MCP tool: the guest's git credential helper calls it.
		{Def: agentcall.GitCredentialCall, Handle: s.credential},
	}
}

// Jobs are the periodic jobs of the git remote: none.
func (s *Service) Jobs() []integrations.Job { return nil }

// pushView is a push as git_push_status shows it.
type pushView struct {
	Branch     string    `json:"branch"`
	Forge      string    `json:"forge"`
	Repository string    `json:"repository"`
	State      string    `json:"state"`
	Result     string    `json:"result,omitempty" doc:"What the forge answered, or why the push failed"`
	Note       string    `json:"note,omitempty" doc:"The reviewer's note"`
	Updated    time.Time `json:"updated"`
}

func (s *Service) toolPushStatus(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Limit int `json:"limit"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 20 {
		in.Limit = 10
	}
	ps, err := s.Store.SandboxGitPushes(ctx, sb.EnvironmentID, sb.ID, "", in.Limit)
	if err != nil {
		return nil, err
	}
	out := []pushView{}
	for _, p := range ps {
		out = append(out, pushView{Branch: strings.TrimPrefix(p.Ref, "refs/heads/"), Forge: p.ForgeName, Repository: repoPath(p),
			State: p.State, Result: p.Result, Note: p.Note, Updated: p.UpdatedAt})
	}
	return map[string]any{"pushes": out}, nil
}

func (s *Service) toolPullRequestStatus(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Branch     string `json:"branch"`
		Forge      string `json:"forge"`
		Repository string `json:"repository"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		return nil, integrations.ToolError("give the branch you pushed")
	}
	ps, err := s.Store.SandboxGitPushes(ctx, sb.EnvironmentID, sb.ID, "refs/heads/"+branch, 100)
	if err != nil {
		return nil, err
	}
	// The newest push of the branch that matches, and the newest pull request proposed for it.
	var newest, proposed *store.GitPush
	for i := range ps {
		p := &ps[i]
		if (in.Forge != "" && p.ForgeName != in.Forge) || (in.Repository != "" && repoPath(*p) != in.Repository) {
			continue
		}
		if newest == nil {
			newest = p
		}
		if proposed == nil && (p.PRApprovalID != "" || p.PRResult != "") && p.ForgeName == newest.ForgeName && repoPath(*p) == repoPath(*newest) {
			proposed = p
		}
	}
	if newest == nil {
		return nil, integrations.ToolError("you have not pushed %s%s", branch, pushedTo(in.Forge, in.Repository))
	}
	pr := map[string]any{"status": "none"}
	if proposed != nil {
		pr = pullRequestView(*proposed)
	}
	return map[string]any{"branch": branch, "forge": newest.ForgeName, "repository": repoPath(*newest),
		"pushState": newest.State, "pullRequest": pr}, nil
}

// pullRequestView is a push's pull request as the stored record says it.
func pullRequestView(p store.GitPush) map[string]any {
	switch {
	case p.PRResult == "":
		return map[string]any{"status": "awaiting approval"}
	case p.PRResult == "opened", p.PRResult == PRUpdated:
		return map[string]any{"status": "opened", "url": p.PRURL}
	case strings.HasPrefix(p.PRResult, "not opened"):
		return map[string]any{"status": "not opened"}
	default:
		return map[string]any{"status": "failed", "reason": strings.TrimPrefix(p.PRResult, "failed: ")}
	}
}

// repoPath is a push's repository on its forge: owner/repo, or repo when there is no owner.
func repoPath(p store.GitPush) string {
	if p.Owner == "" {
		return p.Repo
	}
	return p.Owner + "/" + p.Repo
}

func pushedTo(forge, repository string) string {
	switch {
	case forge != "" && repository != "":
		return " to " + repository + " on " + forge
	case forge != "":
		return " to " + forge
	case repository != "":
		return " to " + repository
	}
	return ""
}

// decodeArgs reads tool arguments strictly: an argument the tool doesn't name is refused.
func decodeArgs(params json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return integrations.ToolError("invalid arguments: %v", err)
	}
	return nil
}
