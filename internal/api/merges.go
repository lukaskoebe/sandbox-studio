package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// Merging a fork back into its source (docs/sandboxes.md#merge-a-fork-back).
func (s *Server) registerMerges(api huma.API) {
	type mergeOut struct{ Body sandboxes.MergeView }
	type mergePath struct {
		Env   string `path:"env" doc:"Environment ID"`
		Merge string `path:"merge" doc:"Merge ID"`
	}

	huma.Register(api, huma.Operation{
		OperationID: "startMerge", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/merge", Tags: []string{"merges"},
		Summary: "Merge a fork back into its source",
		Description: "Starts merging the fork {id}, which must run, into the sandbox it was forked from. Studio forks the source into a scratch " +
			"sandbox <source>-merge-<n> and merges the fork's changes there; the source isn't touched until the merge is accepted. " +
			"It answers at once in state preparing; the merge becomes ready, conflicts or failed.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *sandboxPath) (*mergeOut, error) {
		v, err := s.Sandboxes.StartMerge(ctx, in.Env, in.ID)
		return &mergeOut{v}, mergeError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "listMerges", Method: http.MethodGet,
		Path: "/api/environments/{env}/sandboxes/{id}/merges", Tags: []string{"merges"},
		Summary:     "The merges a sandbox takes part in",
		Description: "As source, fork or scratch sandbox, newest first.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []sandboxes.MergeView }, error) {
		list, err := s.Sandboxes.Merges(ctx, in.Env, in.ID)
		return &struct{ Body []sandboxes.MergeView }{list}, mergeError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getMerge", Method: http.MethodGet,
		Path: "/api/environments/{env}/merges/{merge}", Tags: []string{"merges"},
		Summary: "A merge and its report",
	}, func(ctx context.Context, in *mergePath) (*mergeOut, error) {
		v, err := s.Sandboxes.GetMerge(ctx, in.Env, in.Merge)
		return &mergeOut{v}, mergeError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "checkMerge", Method: http.MethodPost,
		Path: "/api/environments/{env}/merges/{merge}/check", Tags: []string{"merges"},
		Summary: "Count the conflicts left",
		Description: "A text file is resolved once it has no conflict markers; any other conflict once its file under " +
			"/workspace/.studio-merge/conflicts is deleted. The scratch sandbox must run.",
	}, func(ctx context.Context, in *mergePath) (*mergeOut, error) {
		v, err := s.Sandboxes.RecheckMerge(ctx, in.Env, in.Merge)
		return &mergeOut{v}, mergeError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "acceptMerge", Method: http.MethodPost,
		Path: "/api/environments/{env}/merges/{merge}/accept", Tags: []string{"merges"},
		Summary: "Accept a merge",
		Description: "Checkpoints the source, copies everything that changed in the scratch sandbox into it, and deletes the scratch sandbox. " +
			"The source and the scratch sandbox must run, and no conflicts may be left. If the source changed a path since the merge " +
			"started, nothing is copied and the answer is 409, naming the paths. It continues if the client disconnects.",
	}, func(ctx context.Context, in *mergePath) (*mergeOut, error) {
		v, err := s.Sandboxes.AcceptMerge(ctx, in.Env, in.Merge)
		return &mergeOut{v}, mergeError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "abortMerge", Method: http.MethodPost,
		Path: "/api/environments/{env}/merges/{merge}/abort", Tags: []string{"merges"},
		Summary:       "Abort a merge",
		Description:   "Deletes the scratch sandbox. The source was never touched.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *mergePath) (*struct{}, error) {
		return nil, mergeError(s.Sandboxes.AbortMerge(ctx, in.Env, in.Merge))
	})
}

func mergeError(err error) error {
	var notRunning *sandboxes.MergeNotRunningError
	var replay *sandboxes.MergeReplayError
	switch {
	case errors.Is(err, sandboxes.ErrMergeUnsupported), errors.Is(err, sandboxes.ErrTransferUnsupported):
		return huma.Error501NotImplemented(err.Error())
	case errors.Is(err, sandboxes.ErrNotAFork), errors.Is(err, sandboxes.ErrMergeClosed), errors.Is(err, sandboxes.ErrMergeOpen),
		errors.Is(err, sandboxes.ErrMergeNotReady), errors.As(err, &notRunning), errors.As(err, &replay):
		return huma.Error409Conflict(err.Error())
	}
	return apiError(err)
}
