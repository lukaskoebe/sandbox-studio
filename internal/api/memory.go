package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
)

// memoryError maps memory's validation errors to 422 and everything else like apiError.
func memoryError(err error) error {
	if errors.Is(err, memory.ErrInvalid) || errors.Is(err, memory.ErrCoreBudget) {
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return apiError(err)
}

type memoryPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id"`
}

// registerMemory adds the memory endpoints (PLAN §6.7). Everything written here comes from
// the user, so it is stored with tier user and no author persona.
func (s *Server) registerMemory(api huma.API) {
	tags := []string{"memory"}
	base := "/api/environments/{env}/memory"

	huma.Register(api, huma.Operation{
		OperationID: "listMemoryScopes", Method: http.MethodGet, Path: base + "/scopes", Tags: tags,
		Description: "The shared scope, always first, and every persona scope that holds facts or pages.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body []memory.Scope }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Memory.Scopes(ctx, in.Env)
		return &struct{ Body []memory.Scope }{list}, memoryError(err)
	})

	// --- facts

	type listFactsIn struct {
		Env    string `path:"env" doc:"Environment ID"`
		Scope  string `query:"scope" doc:"shared or persona:<id>; empty lists every scope"`
		Status string `query:"status" doc:"active, superseded, disputed or retracted; empty lists every status"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listMemoryFacts", Method: http.MethodGet, Path: base + "/facts", Tags: tags,
	}, func(ctx context.Context, in *listFactsIn) (*struct{ Body []memory.Fact }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Memory.Facts(ctx, in.Env, memory.FactFilter{Scope: in.Scope, Status: in.Status})
		return &struct{ Body []memory.Fact }{list}, memoryError(err)
	})

	type factIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body memory.FactInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createMemoryFact", Method: http.MethodPost, Path: base + "/facts", Tags: tags,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *factIn) (*struct{ Body memory.Fact }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		f, err := s.Memory.CreateFact(ctx, in.Env, in.Body, memory.UserOrigin)
		return &struct{ Body memory.Fact }{f}, memoryError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getMemoryFact", Method: http.MethodGet, Path: base + "/facts/{id}", Tags: tags,
	}, func(ctx context.Context, in *memoryPath) (*struct{ Body memory.Fact }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		f, err := s.Memory.Fact(ctx, in.Env, in.ID)
		return &struct{ Body memory.Fact }{f}, memoryError(err)
	})

	type updateFactIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id"`
		Body memory.FactInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateMemoryFact", Method: http.MethodPut, Path: base + "/facts/{id}", Tags: tags,
		Description: "The scope is fixed at creation and ignored here. An edit makes the fact tier user.",
	}, func(ctx context.Context, in *updateFactIn) (*struct{ Body memory.Fact }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		f, err := s.Memory.UpdateFact(ctx, in.Env, in.ID, in.Body, memory.UserOrigin)
		return &struct{ Body memory.Fact }{f}, memoryError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "retractMemoryFact", Method: http.MethodPost, Path: base + "/facts/{id}/retract", Tags: tags,
		Description: "Marks the fact retracted: it stays for the record but is never recalled.",
	}, func(ctx context.Context, in *memoryPath) (*struct{ Body memory.Fact }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		f, err := s.Memory.SetFactStatus(ctx, in.Env, in.ID, memory.StatusRetracted)
		return &struct{ Body memory.Fact }{f}, memoryError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteMemoryFact", Method: http.MethodDelete, Path: base + "/facts/{id}", Tags: tags,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *memoryPath) (*struct{}, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		return nil, memoryError(s.Memory.DeleteFact(ctx, in.Env, in.ID))
	})

	// --- pages

	type listPagesIn struct {
		Env   string `path:"env" doc:"Environment ID"`
		Scope string `query:"scope" doc:"shared or persona:<id>; empty lists every scope"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listMemoryPages", Method: http.MethodGet, Path: base + "/pages", Tags: tags,
		Description: "Pages without their timelines; core (always-load) pages first.",
	}, func(ctx context.Context, in *listPagesIn) (*struct{ Body []memory.Page }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Memory.Pages(ctx, in.Env, in.Scope)
		return &struct{ Body []memory.Page }{list}, memoryError(err)
	})

	type pageIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body memory.PageInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createMemoryPage", Method: http.MethodPost, Path: base + "/pages", Tags: tags,
		DefaultStatus: http.StatusCreated,
		Description:   "Always-load pages of one scope share a budget of 4000 characters; exceeding it is a 422.",
	}, func(ctx context.Context, in *pageIn) (*struct{ Body memory.Page }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		p, err := s.Memory.CreatePage(ctx, in.Env, in.Body, memory.UserOrigin)
		return &struct{ Body memory.Page }{p}, memoryError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getMemoryPage", Method: http.MethodGet, Path: base + "/pages/{id}", Tags: tags,
	}, func(ctx context.Context, in *memoryPath) (*struct{ Body memory.Page }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		p, err := s.Memory.Page(ctx, in.Env, in.ID)
		return &struct{ Body memory.Page }{p}, memoryError(err)
	})

	type updatePageIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id"`
		Body memory.PageInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateMemoryPage", Method: http.MethodPut, Path: base + "/pages/{id}", Tags: tags,
		Description: "Rewrites the compiled truth; scope and slug are fixed at creation. The timeline is append-only.",
	}, func(ctx context.Context, in *updatePageIn) (*struct{ Body memory.Page }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		p, err := s.Memory.UpdatePage(ctx, in.Env, in.ID, in.Body, memory.UserOrigin)
		return &struct{ Body memory.Page }{p}, memoryError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteMemoryPage", Method: http.MethodDelete, Path: base + "/pages/{id}", Tags: tags,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *memoryPath) (*struct{}, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		return nil, memoryError(s.Memory.DeletePage(ctx, in.Env, in.ID))
	})

	type timelineIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Page ID"`
		Body memory.TimelineInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "appendMemoryTimeline", Method: http.MethodPost, Path: base + "/pages/{id}/timeline", Tags: tags,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *timelineIn) (*struct{ Body memory.TimelineEntry }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		e, err := s.Memory.AppendTimeline(ctx, in.Env, in.ID, in.Body, memory.UserOrigin)
		return &struct{ Body memory.TimelineEntry }{e}, memoryError(err)
	})

	// --- search and conflicts

	type searchIn struct {
		Env               string `path:"env" doc:"Environment ID"`
		Q                 string `query:"q" required:"true" minLength:"1" maxLength:"500"`
		Persona           string `query:"persona" doc:"Searches this persona's private scope plus shared; empty searches shared only"`
		IncludeSuperseded bool   `query:"includeSuperseded" doc:"Also return superseded and expired facts, marked as such"`
		Limit             int    `query:"limit" minimum:"0" maximum:"50" doc:"Defaults to 10"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "searchMemory", Method: http.MethodGet, Path: base + "/search", Tags: tags,
		Description: "Hybrid search: BM25 and vector similarity fused by reciprocal rank, boosted by trust tier and recency. Each hit says why it ranked where it did.",
	}, func(ctx context.Context, in *searchIn) (*struct{ Body []memory.Hit }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		hits, err := s.Memory.Search(ctx, in.Env, memory.SearchRequest{Query: in.Q, Persona: in.Persona, IncludeSuperseded: in.IncludeSuperseded, Limit: in.Limit})
		return &struct{ Body []memory.Hit }{hits}, memoryError(err)
	})

	type listConflictsIn struct {
		Env   string `path:"env" doc:"Environment ID"`
		Scope string `query:"scope" doc:"Conflicts touching this scope; empty lists all"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listMemoryConflicts", Method: http.MethodGet, Path: base + "/conflicts", Tags: tags,
		Description: "Open conflicts first.",
	}, func(ctx context.Context, in *listConflictsIn) (*struct{ Body []memory.Conflict }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Memory.Conflicts(ctx, in.Env, in.Scope)
		return &struct{ Body []memory.Conflict }{list}, memoryError(err)
	})

	type resolveIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Conflict ID"`
		Body struct {
			Resolution string `json:"resolution" enum:"keep_a,keep_b,keep_both,dismiss"`
			Note       string `json:"note,omitempty" maxLength:"500"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "resolveMemoryConflict", Method: http.MethodPost, Path: base + "/conflicts/{id}/resolve", Tags: tags,
		Description: "Records the decision only; the facts are not changed. Edit or retract them separately.",
	}, func(ctx context.Context, in *resolveIn) (*struct{ Body memory.Conflict }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		c, err := s.Memory.ResolveConflict(ctx, in.Env, in.ID, in.Body.Resolution, in.Body.Note)
		return &struct{ Body memory.Conflict }{c}, memoryError(err)
	})
}
