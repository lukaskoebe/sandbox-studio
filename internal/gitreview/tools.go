package gitreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		{Def: agentcall.GitPullRequestCreateTool, Handle: s.toolPullRequestCreate},
		{Def: agentcall.GitRepositoriesTool, Handle: s.toolRepositories},
		{Def: agentcall.GitPullRequestListTool, Handle: s.toolPullRequestList},
		{Def: agentcall.GitPullRequestReadTool, Handle: s.toolPullRequestRead},
		{Def: agentcall.GitPullRequestReviewTool, Handle: s.toolPullRequestReview},
		{Def: agentcall.GitReviewStatusTool, Handle: s.toolReviewStatus},
		{Def: agentcall.GitPullRequestChecksTool, Handle: s.toolPullRequestChecks},
		// Not MCP tools: `studio forge sync`, and the guest's git credential helper.
		{Def: agentcall.ForgeSyncCall, Handle: s.toolForgeSync},
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
	Approval   string    `json:"approval,omitempty" doc:"A pending push's approval, for approval_status"`
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
		if p.State == store.PushPending {
			out[len(out)-1].Approval = p.ApprovalID
		}
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

// toolPullRequestCreate proposes the pull request of the newest push of a branch, which went
// to the forge, as a git.pr approval: the card a push's pull request is proposed on.
func (s *Service) toolPullRequestCreate(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	s.httpClient()
	var in struct {
		Branch     string `json:"branch"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		Base       string `json:"base"`
		Forge      string `json:"forge"`
		Repository string `json:"repository"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	branch, title := strings.TrimSpace(in.Branch), strings.TrimSpace(in.Title)
	if branch == "" || title == "" {
		return nil, integrations.ToolError("give the branch you pushed and the pull request's title")
	}
	ps, err := s.Store.SandboxGitPushes(ctx, sb.EnvironmentID, sb.ID, "refs/heads/"+branch, 100)
	if err != nil {
		return nil, err
	}
	var newest *store.GitPush
	for i := range ps {
		p := &ps[i]
		if (in.Forge == "" || p.ForgeName == in.Forge) && (in.Repository == "" || repoPath(*p) == in.Repository) && p.State != store.PushSuperseded {
			newest = p
			break
		}
	}
	if newest == nil {
		return nil, integrations.ToolError("you have not pushed %s%s", branch, pushedTo(in.Forge, in.Repository))
	}
	p := *newest
	answer := func(pr map[string]any) map[string]any {
		return map[string]any{"branch": branch, "forge": p.ForgeName, "repository": repoPath(p), "pullRequest": pr}
	}
	switch p.State {
	case store.PushPending:
		return nil, integrations.ToolError("your push of %s waits for the user's review; the reviewer can open its pull request while approving it, or call this again once git_push_status says pushed", branch)
	case store.PushPushed:
	default:
		return nil, integrations.ToolError("your newest push of %s is %s, not on the forge; push it again first", branch, p.State)
	}
	if p.PRURL != "" || (p.PRApprovalID != "" && p.PRResult == "") {
		return answer(pullRequestView(p)), nil
	}
	f, err := s.Store.Forge(ctx, p.EnvironmentID, p.ForgeID)
	if err != nil {
		return nil, integrations.ToolError("the forge %s was removed from Sandbox Studio", p.ForgeName)
	}
	if _, ok := apiForge(f); !ok {
		return nil, integrations.ToolError("%s", noAPI(f))
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if url := s.openPullRequestOf(ctx, p, branch); url != "" {
		if err := s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, "", url, PRUpdated); err != nil {
			return nil, err
		}
		return answer(map[string]any{"status": "opened", "url": url}), nil
	}
	base := strings.TrimSpace(in.Base)
	if base == "" {
		base = p.DefaultBranch
	}
	if base == "" {
		base = s.defaultBranch(ctx, f, p.Owner, p.Repo)
	}
	if base == "" {
		return nil, integrations.ToolError("the forge did not say which branch is the default; give base")
	}
	if base == branch {
		return nil, integrations.ToolError("%s is the branch to merge into; push your changes to another branch", branch)
	}
	body := strings.TrimSpace(in.Body)
	if p.TeammateName != "" {
		body = strings.TrimSpace(body + "\n\nPushed by " + p.TeammateName + " from sandbox " + sb.Name + " through Sandbox Studio.")
	}
	s.proposePR(ctx, p, PullRequest{Head: branch, Base: base, Title: truncate(title, 200), Body: body})
	p, err = s.Store.GitPush(ctx, p.EnvironmentID, p.ID)
	if err != nil {
		return nil, err
	}
	if p.PRApprovalID == "" {
		return nil, errors.New("Studio could not propose the pull request")
	}
	return answer(pullRequestView(p)), nil
}

// pullRequestView is a push's pull request as the stored record says it. One awaiting approval
// names its approval, for approval_status (`studio wait`).
func pullRequestView(p store.GitPush) map[string]any {
	switch {
	case p.PRResult == "":
		return map[string]any{"status": "awaiting approval", "approval": p.PRApprovalID}
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
