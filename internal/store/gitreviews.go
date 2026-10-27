package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GitReview is what became of a teammate's review of a pull request. Its draft is the payload
// of the git.review approval it was raised with.
type GitReview struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	SandboxID     string    `json:"sandboxId"`
	ApprovalID    string    `json:"approvalId"`
	ForgeID       string    `json:"forgeId"`
	ForgeName     string    `json:"forgeName"`
	Owner         string    `json:"owner"`
	Repo          string    `json:"repo"`
	Number        int       `json:"number"`
	HeadSHA       string    `json:"headSha" doc:"The pull request's head commit the draft was written against"`
	State         string    `json:"state" enum:"pending,posted,discarded,failed,superseded"`
	URL           string    `json:"url,omitempty" doc:"The posted review's web URL"`
	Result        string    `json:"result,omitempty" doc:"Why posting failed, or how it was posted"`
	Note          string    `json:"note,omitempty" doc:"The user's note on discarding it"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Review states.
const (
	ReviewPending    = "pending"
	ReviewPosted     = "posted"
	ReviewDiscarded  = "discarded"
	ReviewFailed     = "failed"
	ReviewSuperseded = "superseded"
)

const reviewCols = `id, environment_id, sandbox_id, approval_id, forge_id, forge_name, owner, repo, number, head_sha,
	state, url, result, note, created_at, updated_at`

func scanReview(row interface{ Scan(...any) error }) (GitReview, error) {
	var r GitReview
	var created, updated int64
	err := row.Scan(&r.ID, &r.EnvironmentID, &r.SandboxID, &r.ApprovalID, &r.ForgeID, &r.ForgeName, &r.Owner, &r.Repo, &r.Number,
		&r.HeadSHA, &r.State, &r.URL, &r.Result, &r.Note, &created, &updated)
	r.CreatedAt, r.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return r, err
}

// CreateGitReview records a review raised with r.ApprovalID, pending.
func (s *Store) CreateGitReview(ctx context.Context, r GitReview) (GitReview, error) {
	t := now()
	r.ID, r.State = NewID(), ReviewPending
	r.CreatedAt, r.UpdatedAt = time.Unix(t, 0), time.Unix(t, 0)
	_, err := s.db.ExecContext(ctx, `INSERT INTO git_reviews (id, environment_id, sandbox_id, approval_id, forge_id, forge_name, owner, repo,
		number, head_sha, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.EnvironmentID, r.SandboxID, r.ApprovalID, r.ForgeID, r.ForgeName, r.Owner, r.Repo, r.Number, r.HeadSHA, r.State, t, t)
	return r, err
}

// GitReviewByApproval returns the review raised with an approval.
func (s *Store) GitReviewByApproval(ctx context.Context, envID, approvalID string) (GitReview, error) {
	r, err := scanReview(s.db.QueryRowContext(ctx, "SELECT "+reviewCols+" FROM git_reviews WHERE environment_id = ? AND approval_id = ?",
		envID, approvalID))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// LatestGitReview returns a sandbox's newest review of a pull request, of a forge given by
// name ("" for any).
func (s *Store) LatestGitReview(ctx context.Context, envID, sandboxID, forgeName, owner, repo string, number int) (GitReview, error) {
	r, err := scanReview(s.db.QueryRowContext(ctx, "SELECT "+reviewCols+` FROM git_reviews
		WHERE environment_id = ? AND sandbox_id = ? AND (? = '' OR forge_name = ?) AND owner = ? AND repo = ? AND number = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, envID, sandboxID, forgeName, forgeName, owner, repo, number))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// PendingGitReviews returns a sandbox's reviews of a pull request still waiting for the user.
func (s *Store) PendingGitReviews(ctx context.Context, envID, sandboxID, forgeID, owner, repo string, number int) ([]GitReview, error) {
	return s.gitReviews(ctx, `environment_id = ? AND sandbox_id = ? AND forge_id = ? AND owner = ? AND repo = ? AND number = ?
		AND state = 'pending'`, envID, sandboxID, forgeID, owner, repo, number)
}

// UnnotifiedGitReviews returns a sandbox's decided reviews that no chat prompt has told it of.
func (s *Store) UnnotifiedGitReviews(ctx context.Context, envID, sandboxID string) ([]GitReview, error) {
	return s.gitReviews(ctx, `environment_id = ? AND sandbox_id = ? AND state IN ('posted', 'discarded', 'failed')
		AND notified_at IS NULL`, envID, sandboxID)
}

func (s *Store) gitReviews(ctx context.Context, where string, args ...any) ([]GitReview, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+reviewCols+" FROM git_reviews WHERE "+where+" ORDER BY created_at, rowid", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GitReview{}
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SettleGitReview moves a pending review to state to, with its URL, result and note. It
// returns ErrNotFound unless the review was pending, so only one caller settles it.
func (s *Store) SettleGitReview(ctx context.Context, envID, id, to, url, result, note string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE git_reviews SET state = ?, url = ?, result = ?, note = ?, updated_at = ?
		WHERE environment_id = ? AND id = ? AND state = 'pending'`, to, url, result, note, now(), envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkGitReviewNotified records that the agent was told what became of a review.
func (s *Store) MarkGitReviewNotified(ctx context.Context, envID, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE git_reviews SET notified_at = ? WHERE environment_id = ? AND id = ? AND notified_at IS NULL",
		now(), envID, id)
	return err
}

// SetApprovalPayload replaces an approval's payload, such as a review draft with the user's
// edits once it is posted.
func (s *Store) SetApprovalPayload(ctx context.Context, envID, id string, payload []byte) error {
	res, err := s.db.ExecContext(ctx, "UPDATE approvals SET payload = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		string(payload), now(), envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
