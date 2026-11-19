package gitreview

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// What Recover records: whether the forge got it is unknown.
const (
	stoppedPushing = "Studio stopped before the push finished, so it may not have reached the forge; push again"
	stoppedOpening = "Studio stopped while opening it; check the forge, then open it again"
	stoppedPosting = "Studio stopped while posting it, so it may not have reached the forge; check the pull request"
)

// Recover settles what Studio stopped in the middle of: a push, pull request, review or
// comment that was approved but never recorded as done. Each fails with what to check. A push
// can then be pushed again and a pull request opened again. It runs at startup, before any
// decision can be under way.
func (s *Service) Recover(ctx context.Context) error {
	s.httpClient() // defaults the logger
	var errs []error
	pushes, err := s.Store.StrandedGitPushes(ctx)
	errs = append(errs, err)
	for _, p := range pushes {
		s.Log.Info("git remote: a push Studio stopped in the middle of failed", "push", p.ID)
		errs = append(errs, s.Store.SettleGitPush(ctx, p.EnvironmentID, p.ID, store.PushPending, store.PushFailed, stoppedPushing, p.Note))
	}
	prs, err := s.Store.StrandedGitPullRequests(ctx)
	errs = append(errs, err)
	for _, p := range prs {
		errs = append(errs, s.Store.SetGitPushPR(ctx, p.EnvironmentID, p.ID, p.PRApprovalID, "", "failed: "+stoppedOpening))
	}
	reviews, err := s.Store.StrandedGitReviews(ctx)
	errs = append(errs, err)
	for _, r := range reviews {
		errs = append(errs, s.Store.SettleGitReview(ctx, r.EnvironmentID, r.ID, store.ReviewFailed, "", stoppedPosting, r.Note))
	}
	comments, err := s.Store.ApprovedWithoutState(ctx, KindComment)
	errs = append(errs, err)
	for _, a := range comments {
		d, err := CommentDetail(a)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		d.State, d.Result = CommentFailed, stoppedPosting
		payload, err := json.Marshal(d)
		if err == nil {
			err = s.Store.SetApprovalPayload(ctx, a.EnvironmentID, a.ID, payload)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
