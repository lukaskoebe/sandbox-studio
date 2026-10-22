package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// prPayload is the payload of a git.pr approval.
type prPayload struct {
	PushID      string      `json:"pushId"`
	PullRequest PullRequest `json:"pullRequest"`
}

// GitDetail is what the inbox shows for a git approval besides the approval itself.
type GitDetail struct {
	Push        store.GitPush `json:"push"`
	Review      *PushReview   `json:"review,omitempty" doc:"Set for git.push approvals"`
	PullRequest *PullRequest  `json:"pullRequest,omitempty" doc:"Set for git.pr approvals"`
}

// ApprovalKinds are the approvals the git remote raises.
func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{
		{Kind: KindPush, Decide: s.decidePush},
		{Kind: KindPR, Decide: s.decidePR},
		{Kind: KindCredential, Decide: s.decideCredential},
	}
}

// GitDetail describes a git approval for the inbox.
func (s *Service) GitDetail(ctx context.Context, a store.Approval) (*GitDetail, error) {
	p, err := s.Store.GitPushByApproval(ctx, a.EnvironmentID, a.ID)
	if err != nil {
		return nil, err
	}
	d := &GitDetail{Push: p}
	switch a.Kind {
	case KindPush:
		var rv PushReview
		if err := json.Unmarshal(a.Payload, &rv); err != nil {
			return nil, err
		}
		d.Review = &rv
	case KindPR:
		var pp prPayload
		if err := json.Unmarshal(a.Payload, &pp); err != nil {
			return nil, err
		}
		d.PullRequest = &pp.PullRequest
	}
	return d, nil
}

func status(action string) (string, error) {
	switch action {
	case integrations.Allow:
		return store.StatusApproved, nil
	case integrations.Deny:
		return store.StatusDenied, nil
	case integrations.Dismiss:
		return store.StatusDismissed, nil
	}
	return "", fmt.Errorf("%w %q", integrations.ErrAction, action)
}

// decidePush settles a git.push approval. Approving pushes the staged commit upstream
// right away; the outcome lands on the push record either way.
func (s *Service) decidePush(ctx context.Context, a store.Approval, d integrations.Decision) error {
	s.httpClient()
	st, err := status(d.Action)
	if err != nil {
		return err
	}
	p, err := s.Store.GitPushByApproval(ctx, a.EnvironmentID, a.ID)
	if err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	defer s.publish(a.EnvironmentID, a.ID)
	if st != store.StatusApproved {
		result := ""
		if st == store.StatusDismissed {
			result = "dismissed without review"
		}
		return s.Store.SettleGitPush(ctx, p.EnvironmentID, p.ID, store.PushPending, store.PushRejected, result, d.Note)
	}

	// The push outlives the request that approved it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	result, err := s.pushApproved(ctx, p)
	if err != nil {
		s.Log.Info("git remote: approved push failed", "push", p.ID, "err", err)
		return s.Store.SettleGitPush(ctx, p.EnvironmentID, p.ID, store.PushPending, store.PushFailed, err.Error(), d.Note)
	}
	if err := s.Store.SettleGitPush(ctx, p.EnvironmentID, p.ID, store.PushPending, store.PushPushed, result, d.Note); err != nil {
		return err
	}
	s.pullRequestAfterPush(ctx, p, a, d.PullRequest)
	return nil
}

// pushApproved pushes p upstream.
func (s *Service) pushApproved(ctx context.Context, p store.GitPush) (string, error) {
	f, err := s.Store.Forge(ctx, p.EnvironmentID, p.ForgeID)
	if errors.Is(err, store.ErrNotFound) {
		return "", errors.New("the forge was removed from Sandbox Studio")
	}
	if err != nil {
		return "", err
	}
	t := target{sb: store.Sandbox{ID: p.SandboxID, EnvironmentID: p.EnvironmentID}, forge: f, owner: p.Owner, repo: p.Repo}
	token, err := s.token(ctx, f)
	if err != nil {
		return "", fmt.Errorf("the forge's credential: %w", err)
	}
	rm, err := s.remote(t, token)
	if err != nil {
		return "", err
	}
	path := s.stagingPath(t)
	unlock := s.lock(path)
	defer unlock()
	repo, err := openStaging(path)
	if err != nil {
		return "", fmt.Errorf("staging repository: %w", err)
	}
	result, err := rm.pushUpstream(ctx, repo, p)
	// Pushed or refused, the forge's branches may now be past every sandbox's mirror of the
	// repository, so the next push to it fetches again however recently the mirror did.
	s.fetches.forget(mirrorKey(t))
	if err != nil {
		return "", errors.New(mask(err.Error(), token))
	}
	return result, nil
}

// pullRequestFor fills in the pull request a push to rv.Branch comes with: the open one
// from the branch, which the push updates, or else a proposal, unless the branch is the
// repository's default. It returns the default branch, or "" when the forge has no API
// or did not say.
func (s *Service) pullRequestFor(ctx context.Context, f store.Forge, rv *PushReview) string {
	if !hasAPI(f.Kind) {
		return ""
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return ""
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	base, err := ad.DefaultBranch(ctx, token, rv.Owner, rv.Repo)
	if err != nil {
		s.Log.Info("git remote: default branch", "forge", f.Name, "err", err)
		return ""
	}
	if base == "" || base == rv.Branch {
		return base
	}
	open, err := ad.OpenPullRequestOf(ctx, token, rv.Owner, rv.Repo, rv.Branch)
	if err != nil {
		// Proposing one anyway: if one is open after all, opening it says so.
		s.Log.Info("git remote: open pull requests", "forge", f.Name, "err", err)
	}
	if open != "" {
		rv.OpenPullRequest = open
		return base
	}
	pr := prProposal(*rv, base)
	rv.PullRequest = &pr
	return base
}

// pushReview is the review a git.push approval was raised with. A push recorded before
// reviews carried their pull request, or whose forge didn't answer then, has it looked up.
func (s *Service) pushReview(ctx context.Context, p store.GitPush, a store.Approval) (PushReview, error) {
	var rv PushReview
	if err := json.Unmarshal(a.Payload, &rv); err != nil {
		return rv, err
	}
	if rv.PullRequest == nil && rv.OpenPullRequest == "" && p.DefaultBranch == "" {
		if f, err := s.Store.Forge(ctx, p.EnvironmentID, p.ForgeID); err == nil {
			s.pullRequestFor(ctx, f, &rv)
		}
	}
	return rv, nil
}

// pullRequestAfterPush does with the pull request of an approved push what the decision
// chose: open it, or not. A decision without the choice, from a client that doesn't offer
// it, raises a git.pr approval instead. A push to a branch with an open pull request
// records that it updates it.
func (s *Service) pullRequestAfterPush(ctx context.Context, p store.GitPush, a store.Approval, choice *integrations.PullRequestChoice) {
	rv, err := s.pushReview(ctx, p, a)
	if err != nil {
		return
	}
	switch {
	case rv.OpenPullRequest != "":
		if err := s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, "", rv.OpenPullRequest, PRUpdated); err != nil {
			s.Log.Error("git remote: record the pull request a push updates", "push", p.ID, "err", err)
		}
	case rv.PullRequest == nil:
	case choice == nil:
		s.proposePR(ctx, p, *rv.PullRequest)
	case choice.Open:
		pr := *rv.PullRequest
		if t := strings.TrimSpace(choice.Title); t != "" {
			pr.Title = truncate(t, 200)
		}
		_, _ = s.openPRNow(ctx, p, pr)
	}
}

// PRUpdated is the pull request result of a push to a branch with an open pull request.
const PRUpdated = "updated"

// openPRNow opens a pushed branch's pull request without a git.pr approval, since the
// reviewer chose to, and records how it went. The error is the forge's, if it refused.
func (s *Service) openPRNow(ctx context.Context, p store.GitPush, pr PullRequest) (store.GitPush, error) {
	url, openErr := s.openPR(ctx, p, pr)
	p.PRApprovalID, p.PRURL, p.PRResult = "", url, "opened"
	if openErr != nil {
		p.PRURL, p.PRResult = "", "failed: "+openErr.Error()
	}
	if err := s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, "", p.PRURL, p.PRResult); err != nil {
		s.Log.Error("git remote: record a pull request", "push", p.ID, "err", err)
	}
	return p, openErr
}

// openPullRequestOf is the web URL of the pull request open from head in p's repository,
// or "" if there is none or the forge did not say.
func (s *Service) openPullRequestOf(ctx context.Context, p store.GitPush, head string) string {
	f, err := s.Store.Forge(ctx, p.EnvironmentID, p.ForgeID)
	if err != nil {
		return ""
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return ""
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return ""
	}
	url, _ := ad.OpenPullRequestOf(ctx, token, p.Owner, p.Repo, head)
	return url
}

// Errors of OpenPullRequest.
var (
	ErrNoPullRequest  = errors.New("the push has no pull request to open: it went to the repository's default branch, or its forge has no API")
	ErrPullRequestSet = errors.New("the push's pull request is open, or waiting for its approval")
	ErrNotPushed      = errors.New("only a pushed branch can have a pull request opened")
	// ErrForge wraps the forge's refusal to open a pull request.
	ErrForge = errors.New("the forge did not open the pull request")
)

// OpenPullRequest opens the pull request of a push that was approved without one, or whose
// pull request failed to open or was not approved, under title if it is set. approvalID is
// the push's git.push approval. When one turns out to be open from the branch already, the
// push records that it updates it.
func (s *Service) OpenPullRequest(ctx context.Context, envID, approvalID, title string) (store.GitPush, error) {
	s.httpClient()
	a, err := s.Store.Approval(ctx, envID, approvalID)
	if err != nil {
		return store.GitPush{}, err
	}
	if a.Kind != KindPush {
		return store.GitPush{}, ErrNotReview
	}
	p, err := s.Store.GitPushByApproval(ctx, envID, approvalID)
	if err != nil {
		return p, err
	}
	if p.State != store.PushPushed {
		return p, ErrNotPushed
	}
	if p.PRURL != "" || (p.PRApprovalID != "" && p.PRResult == "") {
		return p, ErrPullRequestSet
	}
	rv, err := s.pushReview(ctx, p, a)
	if err != nil {
		return p, err
	}
	if rv.OpenPullRequest != "" {
		return p, ErrPullRequestSet
	}
	if rv.PullRequest == nil {
		return p, ErrNoPullRequest
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	defer s.publish(envID, approvalID)
	// Someone may have opened it on the forge since the push.
	if url := s.openPullRequestOf(ctx, p, rv.PullRequest.Head); url != "" {
		p.PRApprovalID, p.PRURL, p.PRResult = "", url, PRUpdated
		return p, s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, "", url, PRUpdated)
	}
	pr := *rv.PullRequest
	if t := strings.TrimSpace(title); t != "" {
		pr.Title = truncate(t, 200)
	}
	p, err = s.openPRNow(ctx, p, pr)
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrForge, err)
	}
	return p, nil
}

// proposePR raises the git.pr approval for an approved push.
func (s *Service) proposePR(ctx context.Context, p store.GitPush, pr PullRequest) {
	payload, err := json.Marshal(prPayload{PushID: p.ID, PullRequest: pr})
	if err != nil {
		return
	}
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: p.EnvironmentID, SandboxID: p.SandboxID, Kind: KindPR,
		Subject: fmt.Sprintf("%s/%s/%s:%s->%s", p.ForgeName, p.Owner, p.Repo, pr.Head, pr.Base), Payload: payload,
	})
	if err != nil {
		s.Log.Error("git remote: propose PR", "push", p.ID, "err", err)
		return
	}
	if err := s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, a.ID, "", ""); err != nil {
		s.Log.Error("git remote: record PR proposal", "push", p.ID, "err", err)
	}
	s.publish(p.EnvironmentID, a.ID)
}

// decidePR settles a git.pr approval. Approving opens the pull request on the forge.
func (s *Service) decidePR(ctx context.Context, a store.Approval, d integrations.Decision) error {
	s.httpClient()
	st, err := status(d.Action)
	if err != nil {
		return err
	}
	var pp prPayload
	if err := json.Unmarshal(a.Payload, &pp); err != nil {
		return err
	}
	p, err := s.Store.GitPush(ctx, a.EnvironmentID, pp.PushID)
	if err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	defer s.publish(a.EnvironmentID, a.ID)
	if st != store.StatusApproved {
		return s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, a.ID, "", "not opened: "+st)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	url, err := s.openPR(ctx, p, pp.PullRequest)
	if err != nil {
		return s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, a.ID, "", "failed: "+err.Error())
	}
	return s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, a.ID, url, "opened")
}

func (s *Service) openPR(ctx context.Context, p store.GitPush, pr PullRequest) (string, error) {
	f, err := s.Store.Forge(ctx, p.EnvironmentID, p.ForgeID)
	if err != nil {
		return "", errors.New("the forge was removed from Sandbox Studio")
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return "", err
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return "", fmt.Errorf("the forge's token: %w", err)
	}
	return ad.OpenPullRequest(ctx, token, p.Owner, p.Repo, pr)
}

// TestForge checks that a forge's token works and returns the account it belongs to.
func (s *Service) TestForge(ctx context.Context, f store.Forge, repo string) (Check, error) {
	var c Check
	if hasAPI(f.Kind) {
		ad, err := adapter(f, s.httpClient())
		if err != nil {
			return c, err
		}
		token, err := s.token(ctx, f)
		if err != nil {
			return c, fmt.Errorf("the forge's token: %w", err)
		}
		if c.Account, err = ad.Check(ctx, token); err != nil {
			return c, err
		}
	}
	if repo == "" && f.Kind == KindLocal {
		if _, err := CheckLocalPath(f.BaseURL); err != nil {
			return c, err
		}
		return c, nil
	}
	if repo == "" {
		if !hasAPI(f.Kind) {
			return c, fmt.Errorf("%w: name a repository to test with", ErrInvalid)
		}
		return c, nil
	}
	owner, name, ok := splitPath(strings.Trim(repo, "/"))
	if !ok || badOwner(f.Kind, owner) {
		return c, fmt.Errorf("%w: %q is not a repository path on this forge", ErrInvalid, repo)
	}
	t := target{forge: f, owner: owner, repo: name}
	n, err := s.branches(ctx, t)
	if err != nil {
		return c, err
	}
	c.Repo, c.Branches = t.path(), n
	return c, nil
}

// Check is the outcome of testing a forge.
type Check struct {
	Account
	Repo     string // the repository tested, if any
	Branches int    // the branches it has
}

// branches counts a repository's branches, reading its advertisement with Studio's
// credential.
func (s *Service) branches(ctx context.Context, t target) (int, error) {
	token, err := s.token(ctx, t.forge)
	if err != nil {
		return 0, fmt.Errorf("the forge's credential: %w", err)
	}
	rm, err := s.remote(t, token)
	if err != nil {
		return 0, err
	}
	sess, err := rm.tr.NewUploadPackSession(rm.ep, rm.auth)
	if err != nil {
		return 0, err
	}
	defer sess.Close()
	ar, err := sess.AdvertisedReferencesContext(ctx)
	switch {
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return 0, nil
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return 0, fmt.Errorf("the forge has no repository %s, or Studio's credential can't see it", t.path())
	case errors.Is(err, transport.ErrAuthenticationRequired), errors.Is(err, transport.ErrAuthorizationFailed):
		return 0, fmt.Errorf("the forge refused Studio's credential for %s", t.path())
	case err != nil:
		return 0, err
	}
	n := 0
	for name := range ar.References {
		if plumbing.ReferenceName(name).IsBranch() {
			n++
		}
	}
	return n, nil
}

// Forget removes the staging repositories of a forge that was removed from an environment.
func (s *Service) Forget(envID, forgeID string) {
	dirs, _ := filepath.Glob(filepath.Join(s.Dir, envID, "*", forgeID))
	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil && s.Log != nil {
			s.Log.Error("git remote: remove staging", "dir", d, "err", err)
		}
	}
}
