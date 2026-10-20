package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// settingSetup stores the guided setup's progress (docs/frontend.md).
const settingSetup = "setup.v1"

// The two outcomes a setup step can have. A step without one is still to do.
const (
	setupDone    = "done"
	setupSkipped = "skipped"
)

// SetupSteps are the outcomes of the six steps of the guided setup, in the order of the
// wizard. The fields are the only keys the setup has: any other key is refused.
type SetupSteps struct {
	Doctor   string `json:"doctor,omitempty" enum:"done,skipped" doc:"Host check"`
	Provider string `json:"provider,omitempty" enum:"done,skipped" doc:"AI provider"`
	Teammate string `json:"teammate,omitempty" enum:"done,skipped" doc:"Teammate"`
	Git      string `json:"git,omitempty" enum:"done,skipped" doc:"Git forge"`
	Network  string `json:"network,omitempty" enum:"done,skipped" doc:"How approvals work"`
	Sandbox  string `json:"sandbox,omitempty" enum:"done,skipped" doc:"First sandbox"`
}

// fields returns the steps in wizard order, for reading and setting them as a list.
func (st *SetupSteps) fields() []*string {
	return []*string{&st.Doctor, &st.Provider, &st.Teammate, &st.Git, &st.Network, &st.Sandbox}
}

// resolved reports whether every step is done or skipped.
func (st SetupSteps) resolved() bool {
	for _, f := range st.fields() {
		if *f != setupDone && *f != setupSkipped {
			return false
		}
	}
	return true
}

// Setup is the guided setup's state. Steps read done when their condition already holds in
// the environment, even if nothing was chosen for them.
type Setup struct {
	Steps       SetupSteps `json:"steps"`
	Dismissed   bool       `json:"dismissed" doc:"The user dismissed the setup, so Home stops showing it"`
	CompletedAt *time.Time `json:"completedAt,omitempty" doc:"When every step was done or skipped"`
}

type setupQuery struct {
	Env string `query:"env" required:"true" doc:"The environment whose provider, teammate, forge and sandbox steps are checked"`
}

func (s *Server) registerSetup(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getSetup", Method: http.MethodGet, Path: "/api/setup", Tags: []string{"setup"},
		Summary:     "The guided setup's progress",
		Description: "A step reads done when its condition holds: the host check passes and the environment has a provider, a teammate, a forge or a sandbox.",
	}, func(ctx context.Context, in *setupQuery) (*struct{ Body Setup }, error) {
		if _, err := s.Store.Environment(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		out, err := s.setupState(ctx, in.Env)
		return &struct{ Body Setup }{out}, apiError(err)
	})

	type setupUpdate struct {
		Env  string `query:"env" required:"true" doc:"The environment whose conditions the answer reports"`
		Body struct {
			Steps     SetupSteps `json:"steps" doc:"The outcome of each step; omitted steps are still to do"`
			Dismissed bool       `json:"dismissed"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setSetup", Method: http.MethodPut, Path: "/api/setup", Tags: []string{"setup"},
		Summary:     "Record the guided setup's steps and whether it is dismissed",
		Description: "Replaces the stored steps. The setup is completed, and stays so, once every step is done or skipped.",
	}, func(ctx context.Context, in *setupUpdate) (*struct{ Body Setup }, error) {
		if _, err := s.Store.Environment(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		stored, err := s.storedSetup(ctx)
		if err != nil {
			return nil, apiError(err)
		}
		stored.Steps = in.Body.Steps
		stored.Dismissed = in.Body.Dismissed
		if err := s.saveSetup(ctx, stored); err != nil {
			return nil, apiError(err)
		}
		out, err := s.setupState(ctx, in.Env)
		return &struct{ Body Setup }{out}, apiError(err)
	})
}

// storedSetup reads the progress as it was recorded; before the first write it is empty.
func (s *Server) storedSetup(ctx context.Context) (Setup, error) {
	var out Setup
	raw, err := s.Store.Setting(ctx, settingSetup)
	if errors.Is(err, store.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

// saveSetup records the setup's progress.
func (s *Server) saveSetup(ctx context.Context, st Setup) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.Store.SetSetting(ctx, settingSetup, raw)
}

// setupState merges the recorded steps with the conditions that already hold in envID. The
// first time every step reads done or skipped, the completion is recorded, whichever of the
// two made it so.
func (s *Server) setupState(ctx context.Context, envID string) (Setup, error) {
	stored, err := s.storedSetup(ctx)
	if err != nil {
		return stored, err
	}
	holds, err := s.setupConditions(ctx, envID)
	if err != nil {
		return stored, err
	}
	out := stored
	mine, theirs := out.Steps.fields(), holds.fields()
	for i := range mine {
		if *theirs[i] == setupDone {
			*mine[i] = setupDone
		}
	}
	if out.CompletedAt == nil && out.Steps.resolved() {
		now := time.Now().UTC()
		stored.CompletedAt = &now
		out.CompletedAt = &now
		if err := s.saveSetup(ctx, stored); err != nil {
			return out, err
		}
	}
	return out, nil
}

// setupConditions returns the steps whose condition holds, marked done. The network step has
// none: it is done when the user goes through it.
func (s *Server) setupConditions(ctx context.Context, envID string) (SetupSteps, error) {
	var holds SetupSteps
	if s.System.Doctor != nil && s.System.Doctor(ctx).Status == doctor.OK {
		holds.Doctor = setupDone
	}
	providers, err := s.Store.Providers(ctx, envID)
	if err != nil {
		return holds, err
	}
	if len(providers) > 0 {
		holds.Provider = setupDone
	}
	teammates, err := s.Store.Teammates(ctx, envID)
	if err != nil {
		return holds, err
	}
	if len(teammates) > 0 {
		holds.Teammate = setupDone
	}
	forges, err := s.Store.Forges(ctx, envID)
	if err != nil {
		return holds, err
	}
	if len(forges) > 0 {
		holds.Git = setupDone
	}
	recs, err := s.Store.Sandboxes(ctx, envID)
	if err != nil {
		return holds, err
	}
	for _, sb := range recs {
		// The sandboxes the list shows: build and browser VMs don't count as the first one.
		if sandboxes.Public(sb) {
			holds.Sandbox = setupDone
		}
	}
	return holds, nil
}
