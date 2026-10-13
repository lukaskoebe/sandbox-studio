package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
)

// registerMemoryDream adds consolidation: starting a dream, its runs, a conflict with its
// sources and timeline, and promoting a teammate fact to shared memory.
func (s *Server) registerMemoryDream(api huma.API) {
	tags := []string{"memory"}
	base := "/api/environments/{env}/memory"
	ready := func(ctx context.Context, env string) error {
		if err := s.checkEnvironment(ctx, env); err != nil {
			return err
		}
		if s.AgentMem == nil {
			return huma.Error503ServiceUnavailable("memory in agent sessions is not running")
		}
		return nil
	}
	dreamError := func(err error) error {
		if errors.Is(err, agentmem.ErrDreamRunning) {
			return huma.Error409Conflict(err.Error())
		}
		return memoryError(err)
	}

	type dreamIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Scope string `json:"scope" doc:"shared or teammate:<id>"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "startMemoryDream", Method: http.MethodPost, Path: base + "/dream", Tags: tags,
		DefaultStatus: http.StatusAccepted,
		Description:   "Consolidates one scope now: merges duplicates, judges related facts, supersedes or opens conflicts and recompiles entity pages. 409 while a dream of the scope runs.",
	}, func(ctx context.Context, in *dreamIn) (*struct{ Body agentmem.DreamRun }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		r, err := s.AgentMem.StartDream(ctx, in.Env, in.Body.Scope)
		return &struct{ Body agentmem.DreamRun }{r}, dreamError(err)
	})

	type runsIn struct {
		Env   string `path:"env" doc:"Environment ID"`
		Scope string `query:"scope" doc:"Only this scope's runs"`
		Limit int    `query:"limit" minimum:"0" maximum:"200" doc:"At most this many, newest first; 20 by default"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listMemoryDreams", Method: http.MethodGet, Path: base + "/dreams", Tags: tags,
		Description: "Dream runs with their stats, newest first.",
	}, func(ctx context.Context, in *runsIn) (*struct{ Body []agentmem.DreamRun }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.AgentMem.DreamRuns(ctx, in.Env, in.Scope, in.Limit)
		return &struct{ Body []agentmem.DreamRun }{list}, dreamError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getMemoryConflict", Method: http.MethodGet, Path: base + "/conflicts/{id}", Tags: tags,
		Description: "A conflict with both facts' sources and their timeline.",
	}, func(ctx context.Context, in *memoryPath) (*struct{ Body agentmem.ConflictDetail }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		d, err := s.AgentMem.ConflictDetail(ctx, in.Env, in.ID)
		return &struct{ Body agentmem.ConflictDetail }{d}, dreamError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "promoteMemoryFact", Method: http.MethodPost, Path: base + "/facts/{id}/promote", Tags: tags,
		Description: "Proposes a teammate's fact for shared memory through a memory.share approval.",
	}, func(ctx context.Context, in *memoryPath) (*struct{ Body ApprovalView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		a, err := s.AgentMem.Promote(ctx, in.Env, in.ID)
		if err != nil {
			return nil, dreamError(err)
		}
		return &struct{ Body ApprovalView }{s.approvalView(ctx, a)}, nil
	})

	type suggestionsIn struct {
		Env   string `path:"env" doc:"Environment ID"`
		Scope string `query:"scope" doc:"shared or teammate:<id>: the scope whose facts the suggestions are about"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listMemorySuggestions", Method: http.MethodGet, Path: base + "/suggestions", Tags: tags,
		Description: "Open suggestions about a scope's facts: a teammate fact to propose for shared memory, or a fact whose page should be a core page. Nothing is applied until accepted.",
	}, func(ctx context.Context, in *suggestionsIn) (*struct{ Body []memory.Suggestion }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Memory.Suggestions(ctx, in.Env, in.Scope)
		return &struct{ Body []memory.Suggestion }{list}, memoryError(err)
	})

	decide := func(accept bool) func(context.Context, *memoryPath) (*struct{ Body memory.Suggestion }, error) {
		return func(ctx context.Context, in *memoryPath) (*struct{ Body memory.Suggestion }, error) {
			if err := ready(ctx, in.Env); err != nil {
				return nil, err
			}
			sg, err := s.AgentMem.DecideSuggestion(ctx, in.Env, in.ID, accept)
			return &struct{ Body memory.Suggestion }{sg}, dreamError(err)
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "acceptMemorySuggestion", Method: http.MethodPost, Path: base + "/suggestions/{id}/accept", Tags: tags,
		Description: "Accepts a suggestion. A promotion proposes the fact for shared memory through a memory.share approval, which the user decides in the inbox; a core page becomes a core page if the scope's core budget has room.",
	}, decide(true))
	huma.Register(api, huma.Operation{
		OperationID: "dismissMemorySuggestion", Method: http.MethodPost, Path: base + "/suggestions/{id}/dismiss", Tags: tags,
		Description: "Dismisses a suggestion. It is not made again.",
	}, decide(false))
}
