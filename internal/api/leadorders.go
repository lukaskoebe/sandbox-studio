package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/lead"
)

// registerLeadOrders adds Lead's standing orders: the user's words for a plan Lead drafts on its
// own at a set time. Each plan waits for approval as any plan does.
func (s *Server) registerLeadOrders(api huma.API, ready func(ctx context.Context, env string) error) {
	tags := []string{"lead"}
	base := "/api/environments/{env}/lead/orders"
	orderErr := func(err error) error {
		switch {
		case errors.Is(err, lead.ErrTooManyOrders), errors.Is(err, lead.ErrPlanBusy):
			return huma.Error409Conflict(err.Error())
		case errors.Is(err, lead.ErrNoMessage), errors.Is(err, lead.ErrNoDays):
			return huma.Error422UnprocessableEntity(err.Error())
		case errors.Is(err, lead.ErrNoMessenger):
			return huma.Error503ServiceUnavailable(err.Error())
		}
		return apiError(err)
	}

	huma.Register(api, huma.Operation{
		OperationID: "listLeadOrders", Method: http.MethodGet, Path: base, Tags: tags,
		Description: "The environment's standing orders, oldest first, with the plan each drafted last and when it comes due next.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body []lead.OrderView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.Orders(ctx, in.Env)
		return &struct{ Body []lead.OrderView }{v}, orderErr(err)
	})

	type addIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body lead.OrderInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createLeadOrder", Method: http.MethodPost, Path: base, Tags: tags,
		DefaultStatus: http.StatusCreated,
		Description: "Adds a standing order. It first comes due at its next time, not at once; " +
			"409 when the environment has 20 already.",
	}, func(ctx context.Context, in *addIn) (*struct{ Body lead.OrderView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.AddOrder(ctx, in.Env, in.Body)
		return &struct{ Body lead.OrderView }{v}, orderErr(err)
	})

	type setIn struct {
		Env   string `path:"env" doc:"Environment ID"`
		Order string `path:"order" doc:"Standing order ID"`
		Body  lead.OrderInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "setLeadOrder", Method: http.MethodPut, Path: base + "/{order}", Tags: tags,
		Description: "Changes a standing order. Moved to a time that passed today, or turned on after it, it waits for its next time.",
	}, func(ctx context.Context, in *setIn) (*struct{ Body lead.OrderView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.SetOrder(ctx, in.Env, in.Order, in.Body)
		return &struct{ Body lead.OrderView }{v}, orderErr(err)
	})

	type orderIn struct {
		Env   string `path:"env" doc:"Environment ID"`
		Order string `path:"order" doc:"Standing order ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "deleteLeadOrder", Method: http.MethodDelete, Path: base + "/{order}", Tags: tags,
		DefaultStatus: http.StatusNoContent,
		Description:   "Deletes a standing order. The plans it drafted stay.",
	}, func(ctx context.Context, in *orderIn) (*struct{}, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		return &struct{}{}, orderErr(s.Lead.DeleteOrder(ctx, in.Env, in.Order))
	})

	huma.Register(api, huma.Operation{
		OperationID: "runLeadOrder", Method: http.MethodPost, Path: base + "/{order}/run", Tags: tags,
		DefaultStatus: http.StatusAccepted,
		Description: "Drafts the order's plan now, whether or not it is on; its schedule stays as it is. " +
			"The plan waits for approval as any plan does.",
	}, func(ctx context.Context, in *orderIn) (*struct{ Body lead.LeadPlanView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.RunOrder(ctx, in.Env, in.Order)
		return &struct{ Body lead.LeadPlanView }{v}, orderErr(err)
	})
}
