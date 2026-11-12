package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// KindComment is the approval a teammate's comment on a pull request's conversation raises:
// Studio posts it only once the user approves it (docs/git-review.md#conversation-comments).
const KindComment = "git.comment"

// maxComment bounds a conversation comment.
const maxComment = 20000

// Commenter posts a comment on a pull request's conversation.
type Commenter interface {
	// PostComment posts body and returns the comment's web URL.
	PostComment(ctx context.Context, token, owner, repo string, number int, body string) (string, error)
}

// CommentDraft is the payload of a git.comment approval: the pull request and the comment.
// Once decided, it also holds the comment as the user posted it and what became of it.
type CommentDraft struct {
	Teammate    string `json:"teammate,omitempty" doc:"The teammate owning the sandbox; empty for unowned sandboxes"`
	Sandbox     string `json:"sandbox" doc:"The sandbox's name"`
	Forge       string `json:"forge"`
	ForgeID     string `json:"forgeId"`
	ForgeHost   string `json:"forgeHost" doc:"The forge's host, which the comment posts to"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Number      int    `json:"number"`
	Title       string `json:"title" doc:"The pull request's title"`
	URL         string `json:"url,omitempty" doc:"The pull request's web URL"`
	Account     string `json:"account,omitempty" doc:"The forge account the comment posts as: the user's token's"`
	Body        string `json:"body" doc:"The comment, in Markdown"`
	Attribution bool   `json:"attribution,omitempty" doc:"The posted comment ends with the line saying the teammate wrote it"`
	// IdempotencyKey is the agent's key for the comment: asking again with it answers with this one.
	IdempotencyKey string `json:"idempotencyKey,omitempty" doc:"The agent's key for this comment, so that asking again doesn't ask twice"`
	State          string `json:"state,omitempty" enum:"posted,discarded,failed" doc:"Empty until it is decided and, once approved, posted"`
	PostedURL      string `json:"postedUrl,omitempty" doc:"The posted comment's web URL"`
	Result         string `json:"result,omitempty" doc:"Why posting failed"`
	Note           string `json:"note,omitempty" doc:"The user's note"`
}

// Comment states.
const (
	CommentPosted    = "posted"
	CommentDiscarded = "discarded"
	CommentFailed    = "failed"
)

// CommentDetail returns the draft of a git.comment approval.
func CommentDetail(a store.Approval) (*CommentDraft, error) {
	var d CommentDraft
	if err := json.Unmarshal(a.Payload, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Service) toolPullRequestComment(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		pullRequestArgs
		Body string `json:"body"`
		Key  string `json:"idempotency_key"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.Key)
	if key != "" {
		// Asked again, as after a timeout: the comment asked with the key answers.
		a, err := s.Store.ApprovalByKey(ctx, sb.EnvironmentID, sb.ID, KindComment, key)
		if err == nil {
			d, err := CommentDetail(a)
			if err != nil {
				return nil, err
			}
			return commentView(a, *d), nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	body := strings.TrimSpace(in.Body)
	if err := checkComment(body); err != nil {
		return nil, integrations.ToolError("%s", err)
	}
	f, rv, err := s.reviewForge(ctx, sb.EnvironmentID, in.Forge)
	if err != nil {
		return nil, err
	}
	if _, ok := rv.(Commenter); !ok {
		return nil, integrations.ToolError("%s has no API to comment with", f.Name)
	}
	owner, repo, err := in.split(f.Kind)
	if err != nil {
		return nil, err
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
	draft := CommentDraft{Forge: f.Name, ForgeID: f.ID, ForgeHost: forgeHost(f), Owner: owner, Repo: repo, Number: in.Number, Sandbox: sb.Name,
		Title: info.Title, URL: info.URL, Body: body, IdempotencyKey: key}
	if ad, ok := rv.(Adapter); ok {
		if acct, err := ad.Check(rctx, token); err == nil {
			draft.Account = acct.User
		}
	}
	if sb.TeammateID != "" {
		if tm, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID); err == nil {
			draft.Teammate = tm.Name
		}
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: KindComment,
		Subject: fmt.Sprintf("%s/%s/%s#%d", f.Name, owner, repo, in.Number), Payload: payload})
	if err != nil {
		return nil, err
	}
	s.publish(sb.EnvironmentID, a.ID)
	return commentView(a, draft), nil
}

func checkComment(body string) error {
	if body == "" {
		return errors.New("write the comment's body")
	}
	if len(body) > maxComment {
		return fmt.Errorf("the comment is longer than %d bytes", maxComment)
	}
	return nil
}

// commentView is a comment as git_pull_request_comment and approval_status show it.
func commentView(a store.Approval, d CommentDraft) map[string]any {
	v := map[string]any{"forge": d.Forge, "pullRequest": fmt.Sprintf("%s/%s#%d", d.Owner, d.Repo, d.Number)}
	switch {
	case d.State == CommentPosted:
		v["status"], v["url"] = "posted", d.PostedURL
	case d.State == CommentFailed:
		v["status"], v["reason"] = "failed", d.Result
	case d.State == CommentDiscarded:
		v["status"] = "discarded"
		if a.Status == store.StatusDismissed {
			v["status"] = "dismissed"
		}
		if d.Note != "" {
			v["note"] = d.Note
		}
	case a.Status == store.StatusPending:
		v["status"], v["approval"] = "awaiting approval", a.ID
	default:
		v["status"] = "posting"
	}
	return v
}

// commentOutcome is what became of a comment: posted with its URL, discarded, or failed.
func (s *Service) commentOutcome(ctx context.Context, a store.Approval) (map[string]any, bool, error) {
	d, err := CommentDetail(a)
	if err != nil {
		return nil, false, err
	}
	if d.State == "" {
		return nil, false, nil
	}
	return commentView(a, *d), true, nil
}

// decideComment settles a git.comment approval. Approving posts the comment, as the user
// edited it, on the forge; denying discards it with the note.
func (s *Service) decideComment(ctx context.Context, a store.Approval, d integrations.Decision) error {
	s.httpClient()
	st, err := status(d.Action)
	if err != nil {
		return err
	}
	draft, err := CommentDetail(a)
	if err != nil {
		return err
	}
	if st == store.StatusApproved && d.Comment != nil {
		body := strings.TrimSpace(d.Comment.Body)
		if err := checkComment(body); err != nil {
			return fmt.Errorf("%w: %v", integrations.ErrAction, err)
		}
		draft.Body, draft.Attribution = body, d.Comment.Attribution
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	defer s.publish(a.EnvironmentID, a.ID)
	// Decided, the comment settles even if the user's request goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	draft.Note = strings.TrimSpace(d.Note)
	if st != store.StatusApproved {
		draft.State = CommentDiscarded
	} else if u, err := s.postComment(ctx, a.EnvironmentID, *draft); err != nil {
		s.Log.Info("git remote: approved comment failed", "approval", a.ID, "err", err)
		draft.State, draft.Result = CommentFailed, err.Error()
	} else {
		draft.State, draft.PostedURL = CommentPosted, u
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	return s.Store.SetApprovalPayload(ctx, a.EnvironmentID, a.ID, payload)
}

// postComment posts a comment on the forge with the user's token.
func (s *Service) postComment(ctx context.Context, envID string, d CommentDraft) (string, error) {
	f, err := s.Store.Forge(ctx, envID, d.ForgeID)
	if err != nil {
		return "", errors.New("the forge was removed from Sandbox Studio")
	}
	f, ok := apiForge(f)
	if !ok {
		return "", errors.New(noAPI(f))
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return "", err
	}
	c, ok := ad.(Commenter)
	if !ok {
		return "", fmt.Errorf("%s has no API to comment with", f.Name)
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return "", fmt.Errorf("the forge's token: %w", err)
	}
	body := d.Body
	if d.Attribution {
		name := d.Teammate
		if name == "" {
			name = "an agent in " + d.Sandbox
		} else {
			name += " (agent)"
		}
		body += "\n\n— Comment by " + name
	}
	u, err := c.PostComment(ctx, token, d.Owner, d.Repo, d.Number, body)
	if err != nil {
		return "", err
	}
	if u == "" {
		u = d.URL
	}
	return u, nil
}

// postIssueComment posts a comment on the conversation of a Forgejo, Gitea or GitHub pull
// request, which is its issue's.
func postIssueComment(ctx context.Context, call restAPI, token, owner, repo string, number int, body string) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/issues/" + strconv.Itoa(number) + "/comments"
	if err := call(ctx, token, http.MethodPost, path, map[string]any{"body": body}, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

func (f *Forgejo) PostComment(ctx context.Context, token, owner, repo string, number int, body string) (string, error) {
	return postIssueComment(ctx, f.call, token, owner, repo, number, body)
}

func (g *GitHub) PostComment(ctx context.Context, token, owner, repo string, number int, body string) (string, error) {
	return postIssueComment(ctx, g.rest, token, owner, repo, number, body)
}

// PostComment posts a note on a merge request.
func (g *GitLab) PostComment(ctx context.Context, token, owner, repo string, number int, body string) (string, error) {
	mr := g.mr(owner, repo, number)
	var note struct {
		ID int64 `json:"id"`
	}
	if err := g.call(ctx, token, http.MethodPost, mr+"/notes", map[string]any{"body": body}, &note); err != nil {
		return "", err
	}
	var m struct {
		WebURL string `json:"web_url"`
	}
	if err := g.call(ctx, token, http.MethodGet, mr, nil, &m); err != nil || m.WebURL == "" {
		return "", nil
	}
	return fmt.Sprintf("%s#note_%d", m.WebURL, note.ID), nil
}
