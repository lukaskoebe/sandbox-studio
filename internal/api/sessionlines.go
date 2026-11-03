package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// SessionLines is what Home draws of the environment's sandboxes over a window: each
// sandbox's line with its lineage events, and the sessions that branch off it.
type SessionLines struct {
	Since  time.Time            `json:"since"`
	Until  time.Time            `json:"until" doc:"Now, as the server read it"`
	Spans  []store.SessionSpan  `json:"spans" doc:"The terminals, programs, browser sessions, takeovers and ports open at some time in the window, oldest first"`
	Turns  []store.ChatTurnSpan `json:"turns" doc:"The chat turns that ran at some time in the window, oldest first, at most 2000"`
	Events []store.SandboxEvent `json:"events" doc:"The sandboxes' lineage events in the window: creates, forks, checkpoints, merges and the like"`
}

func (s *Server) registerSessionLines(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "sessionLines", Method: http.MethodGet, Path: "/api/environments/{env}/sessions", Tags: []string{"sandboxes"},
		Description: "What the environment's sandboxes had open over the last hours, for Home: chat turns, terminals and the programs " +
			"in them, browser sessions and the user's turns in them, ports listening, and lineage events. Terminals and ports are " +
			"seen every 10 s while a tab is open and every minute otherwise; closed sessions are kept for 14 days.",
	}, func(ctx context.Context, in *struct {
		Env   string `path:"env" doc:"Environment ID"`
		Hours int    `query:"hours" minimum:"1" maximum:"336" default:"24" doc:"How far back the window starts"`
	}) (*struct{ Body SessionLines }, error) {
		if _, err := s.Store.Environment(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		until := time.Now()
		since := until.Add(-time.Duration(in.Hours) * time.Hour)
		out := SessionLines{Since: since, Until: until}
		var err error
		if out.Spans, err = s.Store.SessionSpans(ctx, in.Env, since, until); err != nil {
			return nil, apiError(err)
		}
		if out.Turns, err = s.Store.EnvironmentTurns(ctx, in.Env, since, until); err != nil {
			return nil, apiError(err)
		}
		if out.Events, err = s.Store.EnvironmentEvents(ctx, in.Env, since, until); err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body SessionLines }{out}, nil
	})
}
