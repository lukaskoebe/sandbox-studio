package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/heartbeat"
	"github.com/lukaskoebe/sandbox-studio/internal/lead"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// LeadMessenger sends the messages of Lead's approved plans as a chat prompt does.
func (s *Server) LeadMessenger() lead.Messenger { return leadMessenger{s} }

type leadMessenger struct{ s *Server }

// ready starts or resumes a teammate's computer that is off and waits for its agent.
func (m leadMessenger) ready(ctx context.Context, env, sandboxID string) error {
	if m.s.Sandboxes == nil {
		return errors.New("this Studio runs no computers")
	}
	v, err := m.s.Sandboxes.Get(ctx, env, sandboxID)
	if err != nil {
		return err
	}
	switch v.Status {
	case runtime.StatusRunning, runtime.StatusStarting:
		if v.Agent != nil {
			return nil
		}
	case runtime.StatusStopped, runtime.StatusCrashed, runtime.StatusCreated:
		if _, err := m.s.Sandboxes.Start(ctx, env, sandboxID); err != nil {
			return fmt.Errorf("%s did not start: %w", v.Name, err)
		}
	case runtime.StatusSuspended:
		if _, err := m.s.Sandboxes.Resume(ctx, env, sandboxID); err != nil {
			return fmt.Errorf("%s did not resume: %w", v.Name, err)
		}
	default:
		return fmt.Errorf("%s is %s", v.Name, v.Status)
	}
	if err := m.s.Sandboxes.WaitReady(ctx, sandboxID, heartbeat.ReadyTimeout); err != nil {
		return fmt.Errorf("the agent of %s did not connect: %w", v.Name, err)
	}
	return nil
}

func (m leadMessenger) Queue(ctx context.Context, env, chatID, text string, from chat.FromLead, sent func()) (string, error) {
	chats, err := m.s.chats()
	if err != nil {
		return "", err
	}
	c, err := m.s.Store.Chat(ctx, env, chatID)
	if err != nil {
		return "", err
	}
	if c.State == store.ChatTerminal {
		return "", errors.New("the thread runs in a terminal, so Studio can't write in it")
	}
	if err := m.ready(ctx, env, c.SandboxID); err != nil {
		return "", err
	}
	launch, err := m.s.chatLaunchFor(ctx, env, chatID)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil { // the plan was cancelled while the computer started
		return "", err
	}
	q, queued, err := chats.Enqueue(ctx, env, chatID, launch, chat.Prompt{Text: text, Lead: &from}, sent)
	if err != nil || !queued {
		return "", err
	}
	return q.ID, nil
}

func (m leadMessenger) Unqueue(env, chatID, queued string) bool {
	chats, err := m.s.chats()
	return err == nil && chats.Unqueue(env, chatID, queued) == nil
}

func (m leadMessenger) Start(ctx context.Context, env, sandboxID, title, text string, from chat.FromLead) (string, error) {
	chats, err := m.s.chats()
	if err != nil {
		return "", err
	}
	sb, err := m.s.sessionSandbox(ctx, env, sandboxID)
	if err != nil {
		return "", err
	}
	if sb.TeammateID == "" {
		return "", errors.New("Lead only writes to teammates' computers")
	}
	if err := m.ready(ctx, env, sandboxID); err != nil {
		return "", err
	}
	launch, err := m.s.chatLaunch(ctx, sb)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil { // the plan was cancelled while the computer started
		return "", err
	}
	c, err := chats.CreateTitled(ctx, sb, launch.Harness.Name(), title)
	if err != nil {
		return "", err
	}
	if _, err := chats.Send(ctx, env, c.ID, launch, chat.Prompt{Text: text, Lead: &from}); err != nil {
		_ = chats.Delete(context.WithoutCancel(ctx), env, c.ID)
		return "", err
	}
	return c.ID, nil
}

// registerLeadPlans adds Lead's plans: the user asks for something, Lead proposes messages to
// teammates, and Studio sends them once the user approves the lead.plan approval.
func (s *Server) registerLeadPlans(api huma.API, ready func(ctx context.Context, env string) error) {
	tags := []string{"lead"}
	base := "/api/environments/{env}/lead/plans"
	planErr := func(err error) error {
		switch {
		case errors.Is(err, lead.ErrPlanBusy), errors.Is(err, lead.ErrPlanClosed), errors.Is(err, lead.ErrPlanChanged):
			return huma.Error409Conflict(err.Error())
		case errors.Is(err, lead.ErrNoMessage):
			return huma.Error422UnprocessableEntity(err.Error())
		case errors.Is(err, lead.ErrNoMessenger):
			return huma.Error503ServiceUnavailable(err.Error())
		}
		return apiError(err)
	}

	huma.Register(api, huma.Operation{
		OperationID: "listLeadPlans", Method: http.MethodGet, Path: base, Tags: tags,
		Description: "Lead's plans, the last changed first, without their threads.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body []lead.LeadPlanView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.Plans(ctx, in.Env)
		return &struct{ Body []lead.LeadPlanView }{v}, planErr(err)
	})

	type messageBody struct {
		Text string `json:"text" minLength:"1" maxLength:"2000"`
	}
	type newIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body messageBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "createLeadPlan", Method: http.MethodPost, Path: base, Tags: tags,
		DefaultStatus: http.StatusAccepted,
		Description: "Asks Lead for a plan. Lead drafts it in the background and asks for a lead.plan approval; " +
			"nothing is sent to a teammate until the user approves it. A lead event says when the draft is there.",
	}, func(ctx context.Context, in *newIn) (*struct{ Body lead.LeadPlanView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.NewPlan(ctx, in.Env, in.Body.Text)
		return &struct{ Body lead.LeadPlanView }{v}, planErr(err)
	})

	type planIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Plan string `path:"plan" doc:"Plan ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "getLeadPlan", Method: http.MethodGet, Path: base + "/{plan}", Tags: tags,
		Description: "A plan with its thread: what the user and Lead said, the steps and Lead's report.",
	}, func(ctx context.Context, in *planIn) (*struct{ Body lead.LeadPlanView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.Plan(ctx, in.Env, in.Plan)
		return &struct{ Body lead.LeadPlanView }{v}, planErr(err)
	})

	type messageIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Plan string `path:"plan" doc:"Plan ID"`
		Body messageBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "messageLeadPlan", Method: http.MethodPost, Path: base + "/{plan}/messages", Tags: tags,
		DefaultStatus: http.StatusAccepted,
		Description: "Answers Lead's question or asks for a change. A proposed plan's approval is withdrawn and Lead " +
			"drafts the plan again. 409 while Lead writes or carries out the plan, and once it is done or cancelled.",
	}, func(ctx context.Context, in *messageIn) (*struct{ Body lead.LeadPlanView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.PlanMessage(ctx, in.Env, in.Plan, in.Body.Text)
		return &struct{ Body lead.LeadPlanView }{v}, planErr(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "cancelLeadPlan", Method: http.MethodPost, Path: base + "/{plan}/cancel", Tags: tags,
		Description: "Cancels a plan: one waiting for approval is never sent, and a running one sends nothing more. " +
			"Messages already sent stay in their threads.",
	}, func(ctx context.Context, in *planIn) (*struct{ Body lead.LeadPlanView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.CancelPlan(ctx, in.Env, in.Plan)
		return &struct{ Body lead.LeadPlanView }{v}, planErr(err)
	})
}
