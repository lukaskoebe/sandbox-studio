package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// settingOnboarding stores the onboarding's progress (docs/frontend.md#onboarding).
const settingOnboarding = "onboarding.v1"

// onboardingFeatures are the features the onboarding shows, in the order of Home's card.
var onboardingFeatures = []string{"network", "push", "browser", "preview", "secret"}

// OnboardingFeature is one feature of the onboarding: whether it happened, and whether the
// user closed its tip. Home's card ticks it off on either.
type OnboardingFeature struct {
	Key      string `json:"key" enum:"network,push,browser,preview,secret"`
	Happened bool   `json:"happened" doc:"It happened in the environment: a held request, a push or pull request to review, a browser used, a secret of the user's. Previews leave no record, so they never read happened"`
	Seen     bool   `json:"seen" doc:"The user closed its tip, or it happened before the onboarding was first read"`
}

// Onboarding is the progress of Home's Get to know Studio card and the first-time tips.
type Onboarding struct {
	Features  []OnboardingFeature `json:"features" nullable:"false"`
	Dismissed bool                `json:"dismissed" doc:"The user put the card away with Not now"`
	TipsOff   bool                `json:"tipsOff" doc:"The user stopped the tips"`
	DoneAt    *time.Time          `json:"doneAt,omitempty" doc:"When every feature had happened or been seen"`
}

// onboardingStored is what the setting keeps; happened is read from the environment.
type onboardingStored struct {
	Seen      []string   `json:"seen"`
	Dismissed bool       `json:"dismissed"`
	TipsOff   bool       `json:"tipsOff"`
	DoneAt    *time.Time `json:"doneAt,omitempty"`
}

func (s *Server) registerOnboarding(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getOnboarding", Method: http.MethodGet, Path: "/api/onboarding", Tags: []string{"setup"},
		Summary:     "The onboarding's progress",
		Description: "The first read marks the features that already happened as seen, so a user who used them before the onboarding existed gets no tips for them.",
	}, func(ctx context.Context, in *setupQuery) (*struct{ Body Onboarding }, error) {
		if _, err := s.Store.Environment(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		out, err := s.onboardingState(ctx, in.Env, nil)
		return &struct{ Body Onboarding }{out}, apiError(err)
	})

	type onboardingUpdate struct {
		Env  string `query:"env" required:"true" doc:"The environment whose features the answer reports"`
		Body struct {
			Seen      []string `json:"seen" enum:"network,push,browser,preview,secret" doc:"The features whose tips the user closed"`
			Dismissed bool     `json:"dismissed"`
			TipsOff   bool     `json:"tipsOff"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setOnboarding", Method: http.MethodPut, Path: "/api/onboarding", Tags: []string{"setup"},
		Summary:     "Record which tips were seen, and whether the card or the tips are put away",
		Description: "Replaces the stored progress. An empty seen list with dismissed and tipsOff false shows the tips again.",
	}, func(ctx context.Context, in *onboardingUpdate) (*struct{ Body Onboarding }, error) {
		if _, err := s.Store.Environment(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		seen := []string{}
		for _, k := range onboardingFeatures {
			if slices.Contains(in.Body.Seen, k) {
				seen = append(seen, k)
			}
		}
		out, err := s.onboardingState(ctx, in.Env, &onboardingStored{
			Seen: seen, Dismissed: in.Body.Dismissed, TipsOff: in.Body.TipsOff,
		})
		return &struct{ Body Onboarding }{out}, apiError(err)
	})
}

// onboardingState merges the stored progress, or update in its place, with what happened in
// envID, and saves it when that changed it.
func (s *Server) onboardingState(ctx context.Context, envID string, update *onboardingStored) (Onboarding, error) {
	var out Onboarding
	facts, err := s.Store.OnboardingFacts(ctx, envID)
	if err != nil {
		return out, err
	}
	happened := map[string]bool{"network": facts.Network, "push": facts.Push, "browser": facts.Browser, "secret": facts.Secret}

	var st onboardingStored
	save := false
	raw, err := s.Store.Setting(ctx, settingOnboarding)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// The first read: what already happened needs no tip.
		for _, k := range onboardingFeatures {
			if happened[k] {
				st.Seen = append(st.Seen, k)
			}
		}
		save = true
	case err != nil:
		return out, err
	default:
		if err := json.Unmarshal(raw, &st); err != nil {
			return out, err
		}
	}
	if update != nil {
		update.DoneAt = st.DoneAt
		st, save = *update, true
	}

	done := true
	for _, k := range onboardingFeatures {
		f := OnboardingFeature{Key: k, Happened: happened[k], Seen: slices.Contains(st.Seen, k)}
		done = done && (f.Happened || f.Seen)
		out.Features = append(out.Features, f)
	}
	switch {
	case done && st.DoneAt == nil:
		now := time.Now().UTC()
		st.DoneAt, save = &now, true
	case !done && st.DoneAt != nil:
		// Shown again: the card comes back for what is left.
		st.DoneAt, save = nil, true
	}
	if save {
		raw, err := json.Marshal(st)
		if err != nil {
			return out, err
		}
		if err := s.Store.SetSetting(ctx, settingOnboarding, raw); err != nil {
			return out, err
		}
	}
	out.Dismissed, out.TipsOff, out.DoneAt = st.Dismissed, st.TipsOff, st.DoneAt
	return out, nil
}
