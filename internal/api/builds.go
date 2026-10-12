package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

const (
	defaultBuildJobsLimit = 50
	maxBuildJobsLimit     = 100
	maxBuildSourceBytes   = 64 << 10
)

// BuildJobSummary contains lightweight status metadata for a build. Runtime
// image references, cache identities, prewarm credentials, and builder sandbox
// IDs stay internal to Studio.
type BuildJobSummary struct {
	ID             string    `json:"id"`
	EnvironmentID  string    `json:"environmentId"`
	Status         string    `json:"status" enum:"queued,preparing,setting_up,exporting,ready,failed,cancelled"`
	TemplateID     string    `json:"templateId,omitempty"`
	Error          string    `json:"error,omitempty"`
	CleanupError   string    `json:"cleanupError,omitempty"`
	CleanupPending bool      `json:"cleanupPending"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// BuildJobView adds the submitted source to a job's status metadata.
type BuildJobView struct {
	BuildJobSummary
	Source string `json:"source"`
}

// BuildJobLog is the bounded output snapshot for one build job.
type BuildJobLog struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// BuildSourceProblem is one reason a template spec is invalid. Line and column
// are 1-based; column counts characters. Column is 0 when only the line is
// known, and both are 0 for problems with the whole source.
type BuildSourceProblem struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

// BuildSourceValidation is the result of checking a template spec without
// submitting it.
type BuildSourceValidation struct {
	OK       bool                 `json:"ok"`
	Problems []BuildSourceProblem `json:"problems"`
}

func publicBuildJobSummary(job store.BuildJob) BuildJobSummary {
	return BuildJobSummary{
		ID: job.ID, EnvironmentID: job.EnvironmentID,
		Status: job.Status, TemplateID: job.TemplateID, Error: job.Error,
		CleanupError: job.CleanupError, CleanupPending: job.CleanupPending,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
}

func publicBuildJob(job store.BuildJob) BuildJobView {
	return BuildJobView{BuildJobSummary: publicBuildJobSummary(job), Source: job.Source}
}

type buildJobPath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Build job ID"`
}

// buildOperationError keeps backend and setup details out of HTTP problem
// responses. Build command output is available only through the scoped log route.
func buildOperationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, templatespec.ErrInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("build job not found")
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict("build job state changed")
	case errors.Is(err, store.ErrExists):
		return huma.Error409Conflict("a matching template build already exists")
	default:
		return huma.Error500InternalServerError("build operation failed")
	}
}

func (s *Server) checkBuildEnvironment(ctx context.Context, envID string) error {
	_, err := s.Store.Environment(ctx, envID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("environment not found")
	default:
		return huma.Error500InternalServerError("build operation failed")
	}
}

func (s *Server) registerBuilds(api huma.API) {
	type listIn struct {
		Env   string `path:"env" doc:"Environment ID"`
		Limit int    `query:"limit" default:"50" minimum:"1" maximum:"100" doc:"Maximum number of recent build jobs to return"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listBuildJobs", Method: http.MethodGet, Path: "/api/environments/{env}/builds", Tags: []string{"builds"},
	}, func(ctx context.Context, in *listIn) (*struct{ Body []BuildJobSummary }, error) {
		if err := s.checkBuildEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit == 0 {
			limit = defaultBuildJobsLimit
		}
		if limit < 1 || limit > maxBuildJobsLimit {
			return nil, huma.Error422UnprocessableEntity("limit must be between 1 and 100")
		}
		jobs, err := s.Store.BuildJobs(ctx, in.Env, limit)
		if err != nil {
			return nil, buildOperationError(err)
		}
		out := make([]BuildJobSummary, len(jobs))
		for i, job := range jobs {
			out[i] = publicBuildJobSummary(job)
		}
		return &struct{ Body []BuildJobSummary }{out}, nil
	})

	type submitIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Source string `json:"source" minLength:"1" maxLength:"65536" doc:"Template specification in YAML"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "submitBuildJob", Method: http.MethodPost, Path: "/api/environments/{env}/builds", Tags: []string{"builds"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *submitIn) (*struct{ Body BuildJobView }, error) {
		if err := s.checkBuildEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		if len(in.Body.Source) > maxBuildSourceBytes {
			return nil, huma.Error422UnprocessableEntity("source must be at most 65536 bytes")
		}
		if s.Builds == nil {
			return nil, huma.Error503ServiceUnavailable("template builds are unavailable")
		}
		job, _, err := s.Builds.Submit(ctx, in.Env, in.Body.Source)
		if err != nil {
			return nil, buildOperationError(err)
		}
		if job.EnvironmentID != in.Env || job.ID == "" {
			return nil, huma.Error500InternalServerError("build operation failed")
		}
		return &struct{ Body BuildJobView }{publicBuildJob(job)}, nil
	})

	// Validation runs only the spec parser: it creates no job and does not
	// need the build backend. Source limits match submission.
	type validateIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Source string `json:"source" minLength:"1" maxLength:"65536" doc:"Template specification in YAML"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "validateBuildSource", Method: http.MethodPost, Path: "/api/environments/{env}/builds/validate", Tags: []string{"builds"},
	}, func(ctx context.Context, in *validateIn) (*struct{ Body BuildSourceValidation }, error) {
		if err := s.checkBuildEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		if len(in.Body.Source) > maxBuildSourceBytes {
			return nil, huma.Error422UnprocessableEntity("source must be at most 65536 bytes")
		}
		out := BuildSourceValidation{OK: true, Problems: []BuildSourceProblem{}}
		if _, err := templatespec.ParseYAML([]byte(in.Body.Source)); err != nil {
			problems := templatespec.Problems(err)
			if problems == nil {
				return nil, huma.Error500InternalServerError("build operation failed")
			}
			out.OK = false
			for _, problem := range problems {
				out.Problems = append(out.Problems, BuildSourceProblem(problem))
			}
		}
		return &struct{ Body BuildSourceValidation }{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getTemplateSpecVocabulary", Method: http.MethodGet, Path: "/api/template-spec/vocabulary", Tags: []string{"builds"},
		Description: "The keys, tool names, and size units that the template spec parser accepts, for editor suggestions.",
	}, func(_ context.Context, _ *struct{}) (*struct{ Body templatespec.Vocabulary }, error) {
		return &struct{ Body templatespec.Vocabulary }{templatespec.EditorVocabulary()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getBuildJob", Method: http.MethodGet, Path: "/api/environments/{env}/builds/{id}", Tags: []string{"builds"},
	}, func(ctx context.Context, in *buildJobPath) (*struct{ Body BuildJobView }, error) {
		if err := s.checkBuildEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		job, err := s.Store.BuildJob(ctx, in.Env, in.ID)
		if err != nil {
			return nil, buildOperationError(err)
		}
		return &struct{ Body BuildJobView }{publicBuildJob(job)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "cancelBuildJob", Method: http.MethodPost, Path: "/api/environments/{env}/builds/{id}/cancel", Tags: []string{"builds"},
	}, func(ctx context.Context, in *buildJobPath) (*struct{ Body BuildJobView }, error) {
		if err := s.checkBuildEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		if s.Builds == nil {
			return nil, huma.Error503ServiceUnavailable("template builds are unavailable")
		}
		job, err := s.Builds.Cancel(ctx, in.Env, in.ID)
		if err != nil {
			return nil, buildOperationError(err)
		}
		if job.EnvironmentID != in.Env || job.ID != in.ID {
			return nil, huma.Error404NotFound("build job not found")
		}
		return &struct{ Body BuildJobView }{publicBuildJob(job)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getBuildJobLog", Method: http.MethodGet, Path: "/api/environments/{env}/builds/{id}/log", Tags: []string{"builds"},
	}, func(ctx context.Context, in *buildJobPath) (*struct{ Body BuildJobLog }, error) {
		if err := s.checkBuildEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		text, truncated, err := s.Store.BuildLog(ctx, in.Env, in.ID)
		if err != nil {
			return nil, buildOperationError(err)
		}
		return &struct{ Body BuildJobLog }{BuildJobLog{Text: text, Truncated: truncated}}, nil
	})
}
