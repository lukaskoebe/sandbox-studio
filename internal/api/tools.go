package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// HarnessReleases finds a harness package's newest stable release (npmrelease.Finder).
type HarnessReleases interface {
	Stable(ctx context.Context, pkg string) (string, error)
}

// SandboxTools is what a computer has installed.
type SandboxTools struct {
	Harnesses []SandboxHarness `json:"harnesses" nullable:"false" doc:"The harnesses' CLIs, as the base image installs them or as updated since"`
	Mise      []SandboxTool    `json:"mise" nullable:"false" doc:"Each version of a tool mise has, by name and version"`
	MiseError string           `json:"miseError,omitempty" doc:"Why mise didn't list its tools"`
}

// SandboxHarness is a harness's CLI in a computer.
type SandboxHarness struct {
	Harness   string `json:"harness" enum:"claude,codex,opencode"`
	Version   string `json:"version"`
	Updatable bool   `json:"updatable" doc:"Studio can install another version of it in the computer"`
}

// SandboxTool is one version of a tool mise has.
type SandboxTool struct {
	Name      string `json:"name" doc:"mise's name for it: node, python, npm:prettier"`
	Version   string `json:"version"`
	Requested string `json:"requested,omitempty" doc:"What the config that pins it asks for: 24, latest"`
	Config    string `json:"config,omitempty" doc:"The config file that pins it"`
	Origin    string `json:"origin" enum:"template,added,kept,project,unpinned" doc:"Where it came from: the computer's template pins it; mise use -g pinned it in the computer; a rebase kept it from the old VM; a project's mise.toml asks for it; or no config asks for this version"`
	Installed bool   `json:"installed" doc:"False for a version a config asks for that isn't installed"`
	Active    bool   `json:"active" doc:"What the computer's shells run, outside a project with its own mise.toml"`
}

// HarnessRelease is a harness's newest stable release.
type HarnessRelease struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
}

type harnessPath struct {
	Harness string `path:"harness" enum:"claude,codex,opencode"`
}

type harnessUpdateIn struct {
	Env     string `path:"env" doc:"Environment ID"`
	ID      string `path:"id" doc:"Sandbox ID"`
	Harness string `path:"harness" enum:"claude,codex,opencode"`
	Body    struct {
		Version string `json:"version" doc:"An exact version, such as GET /api/harnesses/{harness}/release names"`
	}
}

func toolsOut(t sandboxes.Tools) SandboxTools {
	out := SandboxTools{Harnesses: []SandboxHarness{}, Mise: []SandboxTool{}, MiseError: t.MiseError}
	for _, h := range t.Harnesses {
		out.Harnesses = append(out.Harnesses, SandboxHarness{
			Harness: h.Harness, Version: h.Version, Updatable: slices.Contains(agentproto.UpdatableHarnesses, h.Harness),
		})
	}
	for _, m := range t.Mise {
		out.Mise = append(out.Mise, SandboxTool{
			Name: m.Name, Version: m.Version, Requested: m.Requested, Config: m.Config,
			Origin: m.Origin, Installed: m.Installed, Active: m.Active,
		})
	}
	return out
}

func toolsError(err error) error {
	switch {
	case errors.Is(err, sandboxes.ErrGuestTooOld):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, sandboxes.ErrInvalidHarnessUpdate):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, sandboxes.ErrHarnessUpdate):
		return huma.Error502BadGateway(err.Error())
	}
	return apiError(err)
}

func (s *Server) registerTools(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getSandboxTools", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/tools", Tags: []string{"sandboxes"},
		Summary:     "What a running computer has installed",
		Description: "The harnesses' CLIs and the tools mise has, asked of the computer's guest agent. A computer that doesn't run answers 409, as does one started before Studio was upgraded, until it restarts.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body SandboxTools }, error) {
		t, err := s.Sandboxes.Tools(ctx, in.Env, in.ID)
		if err != nil {
			return nil, toolsError(err)
		}
		return &struct{ Body SandboxTools }{toolsOut(t)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "updateSandboxHarness", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/harnesses/{harness}/update", Tags: []string{"sandboxes"},
		Summary: "Install another version of a harness's CLI in a running computer",
		Description: "npm installs it in the computer over the base image's, downloading through the environment's network rules, so it may wait on the user to allow registry.npmjs.org. " +
			"It answers once the version is installed, with the computer's tools; that can take minutes. Sessions already running keep their version, and a rebase or disk resize brings back the base image's. Only Claude Code updates.",
	}, func(ctx context.Context, in *harnessUpdateIn) (*struct{ Body SandboxTools }, error) {
		t, err := s.Sandboxes.UpdateHarness(ctx, in.Env, in.ID, in.Harness, in.Body.Version)
		if err != nil {
			return nil, toolsError(err)
		}
		return &struct{ Body SandboxTools }{toolsOut(t)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getHarnessRelease", Method: http.MethodGet, Path: "/api/harnesses/{harness}/release", Tags: []string{"sandboxes"},
		Summary:     "A harness's newest stable release",
		Description: "Studio asks npm, and remembers the answer for ten minutes. 502 when npm can't be reached.",
	}, func(ctx context.Context, in *harnessPath) (*struct{ Body HarnessRelease }, error) {
		if s.HarnessReleases == nil {
			return nil, huma.Error501NotImplemented("this Studio doesn't look up releases")
		}
		v, err := s.HarnessReleases.Stable(ctx, agentproto.HarnessPackages[in.Harness])
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		return &struct{ Body HarnessRelease }{HarnessRelease{Harness: in.Harness, Version: v}}, nil
	})
}
