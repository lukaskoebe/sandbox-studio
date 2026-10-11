package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// settingAgentView stores how agent sessions open by default (docs/design/chat-mode.md).
const settingAgentView = "ui.agent_view"

// UISettings are the web UI's stored choices.
type UISettings struct {
	AgentView string `json:"agentView" enum:"chat,terminal" doc:"How agent sessions open by default"`
	Asked     bool   `json:"asked" doc:"The user chose agentView; until then it is chat and the UI asks once"`
}

// UISettingsUpdate sets the web UI's choices.
type UISettingsUpdate struct {
	AgentView string `json:"agentView" enum:"chat,terminal"`
}

func (s *Server) registerUISettings(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getUISettings", Method: http.MethodGet, Path: "/api/settings/ui", Tags: []string{"settings"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body UISettings }, error) {
		out, err := s.uiSettings(ctx)
		return &struct{ Body UISettings }{out}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "setUISettings", Method: http.MethodPut, Path: "/api/settings/ui", Tags: []string{"settings"},
	}, func(ctx context.Context, in *struct{ Body UISettingsUpdate }) (*struct{ Body UISettings }, error) {
		if err := s.Store.SetSetting(ctx, settingAgentView, []byte(in.Body.AgentView)); err != nil {
			return nil, apiError(err)
		}
		out, err := s.uiSettings(ctx)
		return &struct{ Body UISettings }{out}, apiError(err)
	})
}

func (s *Server) uiSettings(ctx context.Context) (UISettings, error) {
	v, err := s.Store.Setting(ctx, settingAgentView)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return UISettings{AgentView: "chat"}, nil
	case err != nil:
		return UISettings{}, err
	case string(v) == "terminal":
		return UISettings{AgentView: "terminal", Asked: true}, nil
	}
	return UISettings{AgentView: "chat", Asked: true}, nil
}
