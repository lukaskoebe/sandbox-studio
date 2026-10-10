package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
)

// registerMemoryAgent adds what memory did in agent sessions: the sessions, each one's
// log of injections and writes with their reasons, and the extraction budget.
func (s *Server) registerMemoryAgent(api huma.API) {
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

	type sessionsIn struct {
		Env     string `path:"env" doc:"Environment ID"`
		Persona string `query:"persona" doc:"Only this persona's sessions"`
		Limit   int    `query:"limit" minimum:"0" maximum:"200" doc:"At most this many, newest first; 50 by default"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listMemorySessions", Method: http.MethodGet, Path: base + "/sessions", Tags: tags,
		Description: "Harness sessions that used memory, newest first.",
	}, func(ctx context.Context, in *sessionsIn) (*struct{ Body []agentmem.SessionView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.AgentMem.Sessions(ctx, in.Env, in.Persona, in.Limit)
		return &struct{ Body []agentmem.SessionView }{list}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getMemorySessionLog", Method: http.MethodGet, Path: base + "/sessions/{id}/log", Tags: tags,
		Description: "What memory gave a session and wrote from it, in order, each with its reason.",
	}, func(ctx context.Context, in *memoryPath) (*struct{ Body []agentmem.LogEntry }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.AgentMem.SessionLog(ctx, in.Env, in.ID)
		return &struct{ Body []agentmem.LogEntry }{list}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getMemoryUsage", Method: http.MethodGet, Path: base + "/usage", Tags: tags,
		Description: "Today's extraction calls and spending against the daily budget, and each provider's utility model.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body agentmem.UsageView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.AgentMem.UsageReport(ctx, in.Env)
		return &struct{ Body agentmem.UsageView }{v}, apiError(err)
	})
}
