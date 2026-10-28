package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// KindReview is the approval a teammate's review draft raises: Studio posts it only once the
// user approves it (docs/git-review.md#pull-request-reviews).
const KindReview = "git.review"

// Bounds of a review draft.
const (
	maxReviewComments = 50
	maxReviewSummary  = 20000
	maxCommentBody    = 5000
)

// ReviewDraft is the payload of a git.review approval: the pull request as the teammate read
// it, its diff, and the review. Once posted, it holds the review as the user edited it.
type ReviewDraft struct {
	Teammate      string          `json:"teammate,omitempty" doc:"The teammate owning the sandbox; empty for unowned sandboxes"`
	Sandbox       string          `json:"sandbox" doc:"The sandbox's name"`
	Forge         string          `json:"forge"`
	ForgeHost     string          `json:"forgeHost" doc:"The forge's host, which the review posts to"`
	Owner         string          `json:"owner"`
	Repo          string          `json:"repo"`
	PullRequest   PullRequestInfo `json:"pullRequest"`
	Diff          string          `json:"diff" doc:"The pull request's unified diff at HeadSHA"`
	DiffTruncated bool            `json:"diffTruncated,omitempty"`
	Account       string          `json:"account,omitempty" doc:"The forge account the review posts as: the user's token's"`
	Own           bool            `json:"ownPullRequest,omitempty" doc:"The account opened the pull request, so the forge takes a verdict only as a comment"`
	Verdict       string          `json:"verdict" enum:"comment,approve,request_changes"`
	Summary       string          `json:"summary"`
	Comments      []ReviewComment `json:"comments"`
	Attribution   bool            `json:"attribution,omitempty" doc:"The posted review ends with the line saying the teammate wrote it"`
}

// ReviewDetail describes a git.review approval for the inbox.
type ReviewDetail struct {
	Draft  ReviewDraft     `json:"draft"`
	Review store.GitReview `json:"review"`
}

// ReviewDetail returns the draft and outcome of a git.review approval.
func (s *Service) ReviewDetail(ctx context.Context, a store.Approval) (*ReviewDetail, error) {
	var d ReviewDetail
	if err := json.Unmarshal(a.Payload, &d.Draft); err != nil {
		return nil, err
	}
	r, err := s.Store.GitReviewByApproval(ctx, a.EnvironmentID, a.ID)
	if err != nil {
		return nil, err
	}
	d.Review = r
	return &d, nil
}

// reviewForge finds the forge a review tool names, or the environment's one forge that can
// review when it names none.
func (s *Service) reviewForge(ctx context.Context, envID, name string) (store.Forge, Reviewer, error) {
	var f store.Forge
	if name != "" {
		var err error
		f, err = s.Store.ForgeByName(ctx, envID, name)
		if errors.Is(err, store.ErrNotFound) {
			return f, nil, integrations.ToolError("this environment has no forge %q", name)
		}
		if err != nil {
			return f, nil, err
		}
	} else {
		forges, err := s.Store.Forges(ctx, envID)
		if err != nil {
			return f, nil, err
		}
		var names []string
		for _, c := range forges {
			if hasAPI(c.Kind) {
				f = c
				names = append(names, c.Name)
			}
		}
		switch len(names) {
		case 0:
			return f, nil, integrations.ToolError("this environment has no forge with an API; the user adds one in Settings → Git forges")
		case 1:
		default:
			return f, nil, integrations.ToolError("name the forge: one of %s", strings.Join(names, ", "))
		}
	}
	if f.Kind == KindGitLab {
		return f, nil, integrations.ToolError("Studio can't review GitLab merge requests yet")
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return f, nil, integrations.ToolError("%s has no API to read pull requests with", f.Name)
	}
	rv, ok := ad.(Reviewer)
	if !ok {
		return f, nil, integrations.ToolError("%s has no API to read pull requests with", f.Name)
	}
	return f, rv, nil
}

// splitRepository splits a tool's repository argument, owner/repo, on a forge of kind.
func splitRepository(kind, repository string) (string, string, error) {
	owner, repo, ok := splitPath(strings.Trim(repository, "/"))
	if !ok || badOwner(kind, owner) {
		return "", "", integrations.ToolError("repository is owner/repo, not %q", repository)
	}
	return owner, repo, nil
}

// pullRequestArgs are the arguments that name a pull request.
type pullRequestArgs struct {
	Forge      string `json:"forge"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
}

func (in pullRequestArgs) split(kind string) (string, string, error) {
	owner, repo, err := splitRepository(kind, in.Repository)
	if err != nil {
		return "", "", err
	}
	if in.Number < 1 {
		return "", "", integrations.ToolError("give the pull request's number")
	}
	return owner, repo, nil
}

func (s *Service) toolPullRequestRead(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in pullRequestArgs
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	f, rv, err := s.reviewForge(ctx, sb.EnvironmentID, in.Forge)
	if err != nil {
		return nil, err
	}
	owner, repo, err := in.split(f.Kind)
	if err != nil {
		return nil, err
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("the forge's token: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	info, err := rv.ReadPullRequest(ctx, token, owner, repo, in.Number)
	if err != nil {
		return nil, integrations.ToolError("%s", err)
	}
	return info, nil
}

func (s *Service) toolPullRequestReview(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		pullRequestArgs
		Verdict  string          `json:"verdict"`
		Summary  string          `json:"summary"`
		Comments []ReviewComment `json:"comments"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	f, rv, err := s.reviewForge(ctx, sb.EnvironmentID, in.Forge)
	if err != nil {
		return nil, err
	}
	owner, repo, err := in.split(f.Kind)
	if err != nil {
		return nil, err
	}
	draft := ReviewDraft{Forge: f.Name, ForgeHost: forgeHost(f), Owner: owner, Repo: repo, Sandbox: sb.Name,
		Verdict: in.Verdict, Summary: strings.TrimSpace(in.Summary), Comments: in.Comments}
	if err := checkReview(draft.Verdict, draft.Summary, draft.Comments); err != nil {
		return nil, integrations.ToolError("%s", err)
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("the forge's token: %w", err)
	}
	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	info, err := rv.ReadPullRequest(rctx, token, owner, repo, in.Number)
	if err != nil {
		return nil, integrations.ToolError("%s", err)
	}
	draft.PullRequest = info
	draft.Diff, draft.DiffTruncated, err = rv.PullRequestDiff(rctx, token, owner, repo, in.Number, s.Limits.MaxDiff)
	if err != nil {
		return nil, integrations.ToolError("the pull request's diff: %s", err)
	}
	lines := parseDiffLines(draft.Diff)
	for i := range draft.Comments {
		c := &draft.Comments[i]
		if c.Side == "" {
			c.Side = "new"
		}
		if !lines.has(*c) {
			if draft.DiffTruncated && !lines.file(c.Path) {
				return nil, integrations.ToolError("comment %d: the diff is too large for Studio to show %s; comment on it in the summary", i+1, c.Path)
			}
			return nil, integrations.ToolError("comment %d: line %d of %s (side %s) is not in the pull request's diff", i+1, c.Line, c.Path, c.Side)
		}
	}
	if ad, ok := rv.(Adapter); ok {
		if acct, err := ad.Check(rctx, token); err == nil {
			draft.Account = acct.User
			draft.Own = acct.User != "" && strings.EqualFold(acct.User, info.Author)
		}
	}
	if sb.TeammateID != "" {
		if tm, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID); err == nil {
			draft.Teammate = tm.Name
		}
	}

	// A new draft replaces the ones still waiting for the same pull request.
	pending, err := s.Store.PendingGitReviews(ctx, sb.EnvironmentID, sb.ID, f.ID, owner, repo, in.Number)
	if err != nil {
		return nil, err
	}
	for _, old := range pending {
		if err := s.Store.DecideApproval(ctx, sb.EnvironmentID, old.ApprovalID, store.StatusDismissed, ""); err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if err := s.Store.SettleGitReview(ctx, sb.EnvironmentID, old.ID, store.ReviewSuperseded, "", "replaced by a newer draft", ""); err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		// The agent wrote the newer draft itself; it needs no telling.
		_ = s.Store.MarkGitReviewNotified(ctx, sb.EnvironmentID, old.ID)
		s.publish(sb.EnvironmentID, old.ApprovalID)
	}

	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: KindReview,
		Subject: fmt.Sprintf("%s/%s/%s#%d", f.Name, owner, repo, in.Number), Payload: payload})
	if err != nil {
		return nil, err
	}
	if _, err := s.Store.CreateGitReview(ctx, store.GitReview{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, ApprovalID: a.ID,
		ForgeID: f.ID, ForgeName: f.Name, Owner: owner, Repo: repo, Number: in.Number, HeadSHA: info.Head.SHA}); err != nil {
		return nil, err
	}
	s.publish(sb.EnvironmentID, a.ID)
	out := map[string]any{"status": "awaiting approval", "pullRequest": fmt.Sprintf("%s/%s#%d", owner, repo, in.Number)}
	if draft.Own && draft.Verdict != VerdictComment {
		out["note"] = "The forge's account is the pull request's author, so the verdict is posted as a comment."
	}
	return out, nil
}

// checkReview checks a review before it becomes a draft or is posted.
func checkReview(verdict, summary string, comments []ReviewComment) error {
	if !slices.Contains([]string{VerdictComment, VerdictApprove, VerdictRequestChanges}, verdict) {
		return fmt.Errorf("verdict is comment, approve or request_changes, not %q", verdict)
	}
	if summary == "" && verdict != VerdictApprove {
		return errors.New("write a summary")
	}
	if len(summary) > maxReviewSummary {
		return fmt.Errorf("the summary is longer than %d bytes", maxReviewSummary)
	}
	if len(comments) > maxReviewComments {
		return fmt.Errorf("a review has at most %d comments", maxReviewComments)
	}
	for i, c := range comments {
		switch {
		case strings.TrimSpace(c.Body) == "":
			return fmt.Errorf("comment %d is empty", i+1)
		case len(c.Body) > maxCommentBody:
			return fmt.Errorf("comment %d is longer than %d bytes", i+1, maxCommentBody)
		case c.Path == "" || c.Line < 1:
			return fmt.Errorf("comment %d needs a path and a line", i+1)
		case c.Side != "" && c.Side != "new" && c.Side != "old":
			return fmt.Errorf("comment %d: side is new or old", i+1)
		}
	}
	return nil
}

// forgeHost is the host a forge's base URL names.
func forgeHost(f store.Forge) string {
	if u, err := url.Parse(f.BaseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return f.BaseURL
}

func (s *Service) toolReviewStatus(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in pullRequestArgs
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	owner, repo, ok := splitPath(strings.Trim(in.Repository, "/"))
	if !ok || in.Number < 1 {
		return nil, integrations.ToolError("give the repository as owner/repo and the pull request's number")
	}
	r, err := s.Store.LatestGitReview(ctx, sb.EnvironmentID, sb.ID, in.Forge, owner, repo, in.Number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, integrations.ToolError("you have not drafted a review of %s#%d", in.Repository, in.Number)
	}
	if err != nil {
		return nil, err
	}
	return reviewView(r), nil
}

// reviewView is a review as git_pull_request_review_status shows it.
func reviewView(r store.GitReview) map[string]any {
	v := map[string]any{"forge": r.ForgeName, "pullRequest": fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number), "updated": r.UpdatedAt}
	switch r.State {
	case store.ReviewPending:
		v["status"] = "awaiting approval"
	case store.ReviewPosted:
		v["status"], v["url"] = "posted", r.URL
		if r.Result != "" {
			v["result"] = r.Result
		}
	case store.ReviewDiscarded:
		v["status"] = "discarded"
		if r.Note != "" {
			v["note"] = r.Note
		}
	case store.ReviewSuperseded:
		v["status"] = "replaced by a newer draft"
	default:
		v["status"], v["reason"] = "failed", r.Result
	}
	return v
}

// decideReview settles a git.review approval. Approving posts the review, with the user's
// edits, on the forge; denying discards it with the note.
func (s *Service) decideReview(ctx context.Context, a store.Approval, d integrations.Decision) error {
	s.httpClient()
	st, err := status(d.Action)
	if err != nil {
		return err
	}
	r, err := s.Store.GitReviewByApproval(ctx, a.EnvironmentID, a.ID)
	if err != nil {
		return err
	}
	var draft ReviewDraft
	if err := json.Unmarshal(a.Payload, &draft); err != nil {
		return err
	}
	if st == store.StatusApproved && d.Review != nil {
		if draft, err = editDraft(draft, *d.Review); err != nil {
			return fmt.Errorf("%w: %v", integrations.ErrAction, err)
		}
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	defer s.publish(a.EnvironmentID, a.ID)
	// Decided, the review settles even if the user's request goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if st != store.StatusApproved {
		result := ""
		if st == store.StatusDismissed {
			result = "dismissed without an answer"
		}
		return s.Store.SettleGitReview(ctx, a.EnvironmentID, r.ID, store.ReviewDiscarded, "", result, d.Note)
	}
	if payload, err := json.Marshal(draft); err == nil {
		if err := s.Store.SetApprovalPayload(ctx, a.EnvironmentID, a.ID, payload); err != nil {
			s.Log.Error("git remote: record the posted review", "approval", a.ID, "err", err)
		}
	}
	url, result, err := s.postDraft(ctx, r, draft)
	if err != nil {
		s.Log.Info("git remote: approved review failed", "approval", a.ID, "err", err)
		return s.Store.SettleGitReview(ctx, a.EnvironmentID, r.ID, store.ReviewFailed, "", err.Error(), d.Note)
	}
	return s.Store.SettleGitReview(ctx, a.EnvironmentID, r.ID, store.ReviewPosted, url, result, d.Note)
}

// editDraft takes the user's edits into a draft, checking the comments against its diff.
func editDraft(draft ReviewDraft, e integrations.ReviewEdit) (ReviewDraft, error) {
	comments := []ReviewComment{}
	for _, c := range e.Comments {
		side := c.Side
		if side == "" {
			side = "new"
		}
		comments = append(comments, ReviewComment{Path: c.Path, Line: c.Line, Side: side, Body: strings.TrimSpace(c.Body)})
	}
	summary := strings.TrimSpace(e.Summary)
	if err := checkReview(e.Verdict, summary, comments); err != nil {
		return draft, err
	}
	lines := parseDiffLines(draft.Diff)
	for i, c := range comments {
		if !lines.has(c) {
			return draft, fmt.Errorf("comment %d: line %d of %s is not in the diff", i+1, c.Line, c.Path)
		}
	}
	draft.Verdict, draft.Summary, draft.Comments, draft.Attribution = e.Verdict, summary, comments, e.Attribution
	return draft, nil
}

// postDraft posts a review on the forge with the user's token. A verdict on a pull request the
// token's account opened goes as a comment that says the verdict, since the forge takes no
// other from its author.
func (s *Service) postDraft(ctx context.Context, r store.GitReview, draft ReviewDraft) (string, string, error) {
	f, err := s.Store.Forge(ctx, r.EnvironmentID, r.ForgeID)
	if err != nil {
		return "", "", errors.New("the forge was removed from Sandbox Studio")
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return "", "", err
	}
	rv, ok := ad.(Reviewer)
	if !ok {
		return "", "", fmt.Errorf("%s has no API to post reviews with", f.Name)
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return "", "", fmt.Errorf("the forge's token: %w", err)
	}
	post, result := reviewPost(r, draft)
	url, err := rv.PostReview(ctx, token, r.Owner, r.Repo, r.Number, post)
	if err != nil {
		return "", "", err
	}
	if url == "" {
		url = draft.PullRequest.URL
	}
	return url, result, nil
}

// reviewPost is the review Studio posts for a draft, and what it says about how.
func reviewPost(r store.GitReview, draft ReviewDraft) (ReviewPost, string) {
	post := ReviewPost{Verdict: draft.Verdict, Body: draft.Summary, CommitID: r.HeadSHA, Comments: draft.Comments}
	result := ""
	if draft.Own && draft.Verdict != VerdictComment {
		words := map[string]string{VerdictApprove: "Approved.", VerdictRequestChanges: "Changes requested."}[draft.Verdict]
		post.Verdict, post.Body = VerdictComment, strings.TrimSpace("**"+words+"**\n\n"+draft.Summary)
		result = "posted as a comment: the account opened the pull request"
	}
	if draft.Attribution {
		name := draft.Teammate
		if name == "" {
			name = "an agent in " + draft.Sandbox
		} else {
			name += " (agent)"
		}
		post.Body = strings.TrimSpace(post.Body + "\n\n— Review by " + name)
	}
	return post, result
}

// ReviewFeedback is the message that tells the sandbox's agent what became of its review, with
// the user's note, for the review's "Send to" (docs/git-review.md#review-feedback). sent
// records that the agent heard about it.
func (s *Service) ReviewFeedback(ctx context.Context, envID, approvalID, note string) (string, func(), error) {
	a, err := s.Store.Approval(ctx, envID, approvalID)
	if err != nil {
		return "", nil, err
	}
	if a.Kind != KindReview {
		return "", nil, ErrNotReview
	}
	r, err := s.Store.GitReviewByApproval(ctx, envID, approvalID)
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Your review of %s/%s#%d on %s: ", r.Owner, r.Repo, r.Number, r.ForgeName)
	switch r.State {
	case store.ReviewPosted:
		fmt.Fprintf(&b, "I posted it; it is at %s.", r.URL)
	case store.ReviewFailed:
		fmt.Fprintf(&b, "I approved it, but posting failed: %s", oneLine(r.Result))
	case store.ReviewDiscarded:
		b.WriteString("I discarded it; nothing was posted.")
	case store.ReviewSuperseded:
		b.WriteString("a newer draft replaced it.")
	default:
		b.WriteString(statusWords(a.Status, "it"))
	}
	if note := strings.TrimSpace(note); note != "" {
		if len(note) > maxFeedbackNote {
			note = note[:maxFeedbackNote] + "…"
		}
		b.WriteString("\n\nMy comments:\n")
		b.WriteString(note)
	}
	sent := func() {
		if err := s.Store.MarkGitReviewNotified(context.WithoutCancel(ctx), envID, r.ID); err != nil {
			s.Log.Error("git remote: mark review notified", "review", r.ID, "err", err)
		}
	}
	return b.String(), sent, nil
}

// reviewNotes are the lines a chat prompt starts with about the sandbox's reviews that were
// decided since, and the reviews they cover.
func (s *Service) reviewNotes(ctx context.Context, envID, sandboxID string) ([]string, []store.GitReview) {
	list, err := s.Store.UnnotifiedGitReviews(ctx, envID, sandboxID)
	if err != nil || len(list) == 0 {
		return nil, nil
	}
	var lines []string
	for _, r := range list {
		pr := fmt.Sprintf("%s/%s#%d on %s", r.Owner, r.Repo, r.Number, r.ForgeName)
		// The user's note and the forge's answer are quoted: they are data, not instructions.
		switch r.State {
		case store.ReviewPosted:
			lines = append(lines, fmt.Sprintf("- your review of %s was posted: %s", pr, r.URL))
		case store.ReviewDiscarded:
			line := fmt.Sprintf("- your review of %s was discarded; nothing was posted", pr)
			if r.Note != "" {
				line += fmt.Sprintf(". The user's note: %q", noteText(r.Note))
			}
			lines = append(lines, line)
		default:
			lines = append(lines, fmt.Sprintf("- your review of %s was approved, but posting it failed; the forge answered %q", pr, oneLine(r.Result)))
		}
	}
	return lines, list
}

// ReviewHead is where a reviewed pull request's head is now, against the commit the draft
// was written for.
type ReviewHead struct {
	SHA        string `json:"sha" doc:"The pull request's head commit now"`
	NewCommits int    `json:"newCommits" doc:"Commits on top of the one the draft was written against, 0 when they could not be counted"`
	Rewritten  bool   `json:"rewritten,omitempty" doc:"The draft's commit is no longer in the pull request: its branch was rewritten"`
	State      string `json:"state" doc:"open or closed"`
	Merged     bool   `json:"merged,omitempty"`
}

// ReviewHead asks the forge where the pull request of a git.review approval is now.
func (s *Service) ReviewHead(ctx context.Context, envID, approvalID string) (ReviewHead, error) {
	var h ReviewHead
	r, err := s.Store.GitReviewByApproval(ctx, envID, approvalID)
	if err != nil {
		return h, err
	}
	f, err := s.Store.Forge(ctx, envID, r.ForgeID)
	if err != nil {
		return h, err
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return h, err
	}
	rv, ok := ad.(Reviewer)
	if !ok {
		return h, errNoAPI
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return h, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := rv.ReadPullRequest(ctx, token, r.Owner, r.Repo, r.Number)
	if err != nil {
		return h, err
	}
	h.SHA, h.State, h.Merged = info.Head.SHA, info.State, info.Merged
	if h.SHA == r.HeadSHA {
		return h, nil
	}
	// The forge lists a pull request's commits oldest first.
	at := slices.IndexFunc(info.Commits, func(c PRCommit) bool { return c.SHA == r.HeadSHA })
	if at < 0 {
		// Past the first page the commit may just not be listed.
		h.Rewritten = !info.MoreCommits
		return h, nil
	}
	h.NewCommits = len(info.Commits) - 1 - at
	return h, nil
}
