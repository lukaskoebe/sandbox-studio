package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
)

// FeedbackOut says how review feedback went to the agent.
type FeedbackOut struct {
	Queued bool `json:"queued" doc:"The chat was busy: the feedback goes out when its running turn ends"`
}

func (s *Server) registerReviewFeedback(api huma.API) {
	type feedbackIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Approval ID of a git push or pull request"`
		Body struct {
			Note     string `json:"note,omitempty" maxLength:"8000" doc:"The reviewer's comments, line comments included"`
			Chat     string `json:"chat,omitempty" doc:"A chat of the approval's sandbox to send the feedback to"`
			Terminal string `json:"terminal,omitempty" pattern:"^[A-Za-z0-9_-]{1,64}$" doc:"Or an agent's terminal in that sandbox: a tmux session Studio started for claude, codex or opencode"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "sendReviewFeedback", Method: http.MethodPost, Path: "/api/environments/{env}/approvals/{id}/feedback", Tags: []string{"git"},
		Summary: "Send review feedback to the agent",
		Description: "Tells an agent in the approval's sandbox how its push or pull request was reviewed, with the note: as a prompt in one of " +
			"its chats (after the running turn, if one runs), or pasted into an agent's terminal and submitted. A plain shell takes no " +
			"feedback, since the text would run as commands. Once sent, the push's next fetch and next prompt don't repeat it.",
	}, func(ctx context.Context, in *feedbackIn) (*struct{ Body FeedbackOut }, error) {
		if (in.Body.Chat == "") == (in.Body.Terminal == "") {
			return nil, huma.Error422UnprocessableEntity("name a chat or a terminal")
		}
		if s.Git == nil {
			return nil, huma.Error503ServiceUnavailable("git review is not available")
		}
		approval, err := s.Store.Approval(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		push, text, err := s.Git.Feedback(ctx, in.Env, in.ID, in.Body.Note)
		if err != nil {
			return nil, apiError(err)
		}
		sent := func() { s.Git.FeedbackSent(context.WithoutCancel(ctx), push) }

		if in.Body.Terminal != "" {
			if err := s.Sandboxes.SendToAgent(ctx, in.Env, approval.SandboxID, in.Body.Terminal, text); err != nil {
				return nil, apiError(err)
			}
			sent()
			return &struct{ Body FeedbackOut }{}, nil
		}
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		c, err := s.Store.Chat(ctx, in.Env, in.Body.Chat)
		if err != nil {
			return nil, apiError(err)
		}
		if c.SandboxID != approval.SandboxID {
			return nil, huma.Error422UnprocessableEntity("the chat is in another sandbox than the push")
		}
		launch, err := s.chatLaunchFor(ctx, in.Env, c.ID)
		if err != nil {
			return nil, err
		}
		queued, err := chats.Queue(context.WithoutCancel(ctx), in.Env, c.ID, launch, chat.Prompt{Text: text}, sent)
		return &struct{ Body FeedbackOut }{FeedbackOut{Queued: queued}}, apiError(err)
	})
}
