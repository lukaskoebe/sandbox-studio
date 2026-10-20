package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
)

// FeedbackOut says how review feedback went to the agent.
type FeedbackOut struct {
	Queued bool   `json:"queued" doc:"The chat was busy: the feedback goes out when its running turn ends"`
	Chat   string `json:"chat,omitempty" doc:"The chat started for it, when sent to a new chat"`
}

func (s *Server) registerReviewFeedback(api huma.API) {
	type feedbackIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Approval ID of a git push or pull request"`
		Body struct {
			Note     string `json:"note,omitempty" maxLength:"8000" doc:"The reviewer's comments, line comments included"`
			Chat     string `json:"chat,omitempty" doc:"A chat of the approval's sandbox to send the feedback to"`
			Terminal string `json:"terminal,omitempty" pattern:"^[A-Za-z0-9_-]{1,64}$" doc:"Or an agent's terminal in that sandbox: a tmux session Studio started for claude, codex or opencode"`
			NewChat  bool   `json:"newChat,omitempty" doc:"Or a new chat in that sandbox, with the feedback as its first prompt"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "sendReviewFeedback", Method: http.MethodPost, Path: "/api/environments/{env}/approvals/{id}/feedback", Tags: []string{"git"},
		Summary: "Send review feedback to the agent",
		Description: "Tells an agent in the approval's sandbox how its push or pull request was reviewed, with the note: as a prompt in one of " +
			"its chats (after the running turn, if one runs), as the first prompt of a new chat, or pasted into an agent's terminal and " +
			"submitted. A plain shell takes no " +
			"feedback, since the text would run as commands. Once sent, the push's next fetch and next prompt don't repeat it.",
	}, func(ctx context.Context, in *feedbackIn) (*struct{ Body FeedbackOut }, error) {
		if n := btoi(in.Body.Chat != "") + btoi(in.Body.Terminal != "") + btoi(in.Body.NewChat); n != 1 {
			return nil, huma.Error422UnprocessableEntity("name a chat or a terminal, or ask for a new chat")
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
		if in.Body.NewChat {
			sb, err := s.sessionSandbox(ctx, in.Env, approval.SandboxID)
			if err != nil {
				return nil, err
			}
			launch, err := s.chatLaunch(ctx, sb)
			if err != nil {
				return nil, err
			}
			c, err := chats.Create(ctx, sb, launch.Harness.Name())
			if err != nil {
				return nil, apiError(err)
			}
			if _, err := chats.Send(context.WithoutCancel(ctx), in.Env, c.ID, launch, chat.Prompt{Text: text}); err != nil {
				_ = chats.Delete(context.WithoutCancel(ctx), in.Env, c.ID)
				return nil, apiError(err)
			}
			sent()
			return &struct{ Body FeedbackOut }{FeedbackOut{Chat: c.ID}}, nil
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

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Server) registerOpenPullRequest(api huma.API) {
	type openIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Approval ID of a git push"`
		Body *struct {
			Title string `json:"title,omitempty" maxLength:"200" doc:"The pull request's title; empty keeps the proposed one"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "openPullRequest", Method: http.MethodPost, Path: "/api/environments/{env}/approvals/{id}/pull-request", Tags: []string{"git"},
		Summary: "Open a pushed branch's pull request",
		Description: "Opens the pull request of a push approved without one, or whose pull request failed to open or was not approved. " +
			"409 when the push isn't pushed, went to the default branch, or its pull request is open or waiting for approval; " +
			"502 when the forge refuses it. If one turns out to be open from the branch already, the push records that it updates it.",
	}, func(ctx context.Context, in *openIn) (*struct{ Body ApprovalView }, error) {
		if s.Git == nil {
			return nil, huma.Error503ServiceUnavailable("git review is not available")
		}
		title := ""
		if in.Body != nil {
			title = in.Body.Title
		}
		if _, err := s.Git.OpenPullRequest(ctx, in.Env, in.ID, title); err != nil {
			if errors.Is(err, gitreview.ErrForge) {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return nil, apiError(err)
		}
		a, err := s.Store.Approval(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body ApprovalView }{s.approvalView(ctx, a)}, nil
	})
}
