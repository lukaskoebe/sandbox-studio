package gitreview

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ErrNotReview means review feedback was asked for an approval that is no git push or pull
// request.
var ErrNotReview = errors.New("the approval is not a git push or pull request")

// maxFeedbackNote bounds the reviewer's comments in a feedback message.
const maxFeedbackNote = 8000

// Feedback is the message that tells a sandbox's agent how its push or pull request was
// reviewed, with note, the reviewer's comments on it: what the review sends to one of the
// sandbox's chats or agent terminals (docs/git-review.md#review-feedback). It is written as
// the reviewer's own message, since the reviewer is the user. The push is returned so the
// caller can record, with FeedbackSent, that the agent heard about it.
func (s *Service) Feedback(ctx context.Context, envID, approvalID, note string) (store.GitPush, string, error) {
	a, err := s.Store.Approval(ctx, envID, approvalID)
	if err != nil {
		return store.GitPush{}, "", err
	}
	if a.Kind != KindPush && a.Kind != KindPR {
		return store.GitPush{}, "", ErrNotReview
	}
	p, err := s.Store.GitPushByApproval(ctx, envID, approvalID)
	if err != nil {
		return store.GitPush{}, "", err
	}
	branch := strings.TrimPrefix(p.Ref, "refs/heads/")
	var b strings.Builder
	if a.Kind == KindPR {
		fmt.Fprintf(&b, "Review of your pull request from %s in %s on %s: ", branch, repoPath(p), p.ForgeName)
		switch {
		case p.PRURL != "":
			fmt.Fprintf(&b, "approved; it is open at %s.", p.PRURL)
		case p.PRResult != "":
			fmt.Fprintf(&b, "approved, but opening it failed: %s", oneLine(p.PRResult))
		default:
			b.WriteString(statusWords(a.Status, "it"))
		}
	} else {
		fmt.Fprintf(&b, "Review of your push of %s to %s in %s on %s: ", short(p.NewSHA), branch, repoPath(p), p.ForgeName)
		switch p.State {
		case store.PushPushed:
			switch {
			case p.PRResult == PRUpdated:
				fmt.Fprintf(&b, "approved and pushed; it updates the pull request %s.", p.PRURL)
			case p.PRURL != "":
				fmt.Fprintf(&b, "approved and pushed, and the pull request is open at %s.", p.PRURL)
			case strings.HasPrefix(p.PRResult, "failed: ") && p.PRApprovalID == "":
				fmt.Fprintf(&b, "approved and pushed, but opening the pull request failed: %s", oneLine(strings.TrimPrefix(p.PRResult, "failed: ")))
			default:
				b.WriteString("approved and pushed.")
			}
		case store.PushFailed:
			fmt.Fprintf(&b, "approved, but the push failed: %s", oneLine(p.Result))
		case store.PushRejected:
			b.WriteString("rejected; nothing went upstream.")
		case store.PushSuperseded:
			b.WriteString("replaced by a later push to the same branch.")
		default:
			b.WriteString(statusWords(a.Status, "it"))
		}
	}
	if note := strings.TrimSpace(note); note != "" {
		if len(note) > maxFeedbackNote {
			note = note[:maxFeedbackNote] + "…"
		}
		b.WriteString("\n\nMy comments:\n")
		b.WriteString(note)
	}
	return p, b.String(), nil
}

// ErrDecided means comments were left on a push that is no longer waiting: once decided, the
// decision's note is the push's, and feedback goes to a chat or terminal.
var ErrDecided = errors.New("the push was decided already; send the comments to a chat or terminal")

// LeaveNote records note, the reviewer's comments, on a push that still waits, for any agent
// in its sandbox to read: git_push_status and approval_status show it, and the sandbox's next
// chat prompt tells it once (docs/git-review.md#review-feedback). An empty note takes them
// back.
func (s *Service) LeaveNote(ctx context.Context, envID, approvalID, note string) error {
	a, err := s.Store.Approval(ctx, envID, approvalID)
	if err != nil {
		return err
	}
	if a.Kind != KindPush {
		return ErrNotReview
	}
	p, err := s.Store.GitPushByApproval(ctx, envID, approvalID)
	if err != nil {
		return err
	}
	note = strings.TrimSpace(note)
	if len(note) > maxFeedbackNote {
		note = note[:maxFeedbackNote] + "…"
	}
	if err := s.Store.LeaveGitPushNote(ctx, envID, p.ID, note); errors.Is(err, store.ErrNotFound) {
		return ErrDecided
	} else if err != nil {
		return err
	}
	s.publish(envID, approvalID)
	return nil
}

func statusWords(status, it string) string {
	switch status {
	case store.StatusPending:
		return it + " is still waiting for my review."
	case store.StatusDenied:
		return "rejected."
	case store.StatusDismissed:
		return "ignored."
	default:
		return status + "."
	}
}

// FeedbackSent records that the agent was told about p, so neither its next fetch nor its
// next prompt tells it again.
func (s *Service) FeedbackSent(ctx context.Context, p store.GitPush) {
	ctx = context.WithoutCancel(ctx)
	if err := s.Store.MarkGitPushDelivered(ctx, p.EnvironmentID, p.ID); err != nil {
		s.Log.Error("git remote: mark delivered", "push", p.ID, "err", err)
	}
	if err := s.Store.MarkGitPushNotified(ctx, p.EnvironmentID, p.ID); err != nil {
		s.Log.Error("git remote: mark notified", "push", p.ID, "err", err)
	}
}
