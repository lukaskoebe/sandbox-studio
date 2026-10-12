package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

func (s *Server) registerLineage(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getSandboxLineage", Method: http.MethodGet,
		Path: "/api/environments/{env}/sandboxes/{id}/lineage", Tags: []string{"sandboxes"},
		Description: "The sandbox's history: its events in order, the sandbox it was forked from, the forks made from it and its checkpoints. Deleted forks and sources stay visible as tombstones.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body sandboxes.Lineage }, error) {
		lineage, err := s.Sandboxes.Lineage(ctx, in.Env, in.ID)
		return &struct{ Body sandboxes.Lineage }{lineage}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getLineageTree", Method: http.MethodGet,
		Path: "/api/environments/{env}/lineage", Tags: []string{"sandboxes"},
		Description: "The environment's family tree: sandboxes grouped by template, with each fork under its source. Deleted sandboxes stay as tombstones.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body sandboxes.LineageTree }, error) {
		tree, err := s.Sandboxes.LineageTree(ctx, in.Env)
		return &struct{ Body sandboxes.LineageTree }{tree}, apiError(err)
	})
}
