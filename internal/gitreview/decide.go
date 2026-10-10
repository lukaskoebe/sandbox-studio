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

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// prPayload is the payload of a git.pr approval.
type prPayload struct {
	PushID      string      `json:"pushId"`
	PullRequest PullRequest `json:"pullRequest"`
}

// Detail is what the inbox shows for a git approval besides the approval itself.
type Detail struct {
	Push        store.GitPush `json:"push"`
	Review      *PushReview   `json:"review,omitempty" doc:"Set for git.push approvals"`
	PullRequest *PullRequest  `json:"pullRequest,omitempty" doc:"Set for git.pr approvals"`
}

// ApprovalKinds are the approvals the git remote raises.
func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{
		{Kind: KindPush, Decide: s.decidePush},
		{Kind: KindPR, Decide: s.decidePR},
	}
}

// Detail describes a git approval for the inbox.
func (s *Service) Detail(ctx context.Context, a store.Approval) (*Detail, error) {
	p, err := s.Store.GitPushByApproval(ctx, a.EnvironmentID, a.ID)
	if err != nil {
		return nil, err
	}
	d := &Detail{Push: p}
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
	result, pr, err := s.pushApproved(ctx, p)
	if err != nil {
		s.Log.Info("git remote: approved push failed", "push", p.ID, "err", err)
		return s.Store.SettleGitPush(ctx, p.EnvironmentID, p.ID, store.PushPending, store.PushFailed, err.Error(), d.Note)
	}
	if err := s.Store.SettleGitPush(ctx, p.EnvironmentID, p.ID, store.PushPending, store.PushPushed, result, d.Note); err != nil {
		return err
	}
	if pr != nil {
		s.proposePR(ctx, p, *pr)
	}
	return nil
}

// pushApproved pushes p upstream and, for a Forgejo branch other than the default one,
// returns the pull request to propose.
func (s *Service) pushApproved(ctx context.Context, p store.GitPush) (string, *PullRequest, error) {
	f, err := s.Store.Forge(ctx, p.EnvironmentID, p.ForgeID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil, errors.New("the forge was removed from Sandbox Studio")
	}
	if err != nil {
		return "", nil, err
	}
	t := target{sb: store.Sandbox{ID: p.SandboxID, EnvironmentID: p.EnvironmentID}, forge: f, owner: p.Owner, repo: p.Repo}
	token, err := s.token(ctx, f)
	if err != nil {
		return "", nil, fmt.Errorf("the forge's token: %w", err)
	}
	rm, err := s.remote(t, token)
	if err != nil {
		return "", nil, err
	}
	path := s.stagingPath(t)
	unlock := s.lock(path)
	defer unlock()
	repo, err := openStaging(path)
	if err != nil {
		return "", nil, fmt.Errorf("staging repository: %w", err)
	}
	result, err := rm.pushUpstream(ctx, repo, p)
	if err != nil {
		return "", nil, errors.New(mask(err.Error(), token))
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil || f.Kind != KindForgejo {
		return result, nil, nil
	}
	base, err := ad.DefaultBranch(ctx, token, p.Owner, p.Repo)
	branch := strings.TrimPrefix(p.Ref, "refs/heads/")
	if err != nil {
		s.Log.Info("git remote: default branch", "err", err)
		return result, nil, nil
	}
	if base == "" || base == branch {
		return result, nil, nil
	}
	a, err := s.Store.Approval(ctx, p.EnvironmentID, p.ApprovalID)
	if err != nil {
		return result, nil, nil
	}
	var rv PushReview
	if json.Unmarshal(a.Payload, &rv) != nil {
		return result, nil, nil
	}
	rv.Branch = branch
	pr := prProposal(rv, base)
	return result, &pr, nil
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

// TestForge checks that a forge's token works and returns the user it belongs to.
func (s *Service) TestForge(ctx context.Context, f store.Forge) (string, error) {
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return "", err
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return "", fmt.Errorf("the forge's token: %w", err)
	}
	return ad.Check(ctx, token)
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
