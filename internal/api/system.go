package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/updates"
)

// System backs the install-wide settings: the doctor, autostart and the update check.
// They are not part of any environment.
type System struct {
	Doctor    func(context.Context) doctor.Report
	Autostart *autostart.Service
	Updates   *updates.Checker
}

type toggleIn struct {
	Body struct {
		Enabled bool `json:"enabled"`
	}
}

func (s *Server) registerSystem(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getDoctor", Method: http.MethodGet, Path: "/api/system/doctor", Tags: []string{"system"},
		Summary: "Run the host checks",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body doctor.Report }, error) {
		if s.System.Doctor == nil {
			return nil, huma.Error503ServiceUnavailable("the doctor is not available")
		}
		return &struct{ Body doctor.Report }{s.System.Doctor(ctx)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getAutostart", Method: http.MethodGet, Path: "/api/system/autostart", Tags: []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body autostart.Status }, error) {
		if s.System.Autostart == nil {
			return &struct{ Body autostart.Status }{}, nil
		}
		st, err := s.System.Autostart.Status()
		return &struct{ Body autostart.Status }{st}, autostartError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "setAutostart", Method: http.MethodPut, Path: "/api/system/autostart", Tags: []string{"system"},
		Summary: "Start Studio at login, or stop doing so",
	}, func(ctx context.Context, in *toggleIn) (*struct{ Body autostart.Status }, error) {
		if s.System.Autostart == nil {
			return nil, huma.Error422UnprocessableEntity(autostart.ErrUnsupported.Error())
		}
		var st autostart.Status
		var err error
		if in.Body.Enabled {
			st, err = s.System.Autostart.Enable()
		} else {
			st, err = s.System.Autostart.Disable()
		}
		return &struct{ Body autostart.Status }{st}, autostartError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getUpdates", Method: http.MethodGet, Path: "/api/system/updates", Tags: []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body updates.State }, error) {
		if s.System.Updates == nil {
			return nil, huma.Error503ServiceUnavailable("the update check is not available")
		}
		st, err := s.System.Updates.State(ctx)
		return &struct{ Body updates.State }{st}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "setUpdates", Method: http.MethodPut, Path: "/api/system/updates", Tags: []string{"system"},
		Summary: "Turn the daily update check on or off; turning it on checks right away",
	}, func(ctx context.Context, in *toggleIn) (*struct{ Body updates.State }, error) {
		if s.System.Updates == nil {
			return nil, huma.Error503ServiceUnavailable("the update check is not available")
		}
		st, err := s.System.Updates.SetEnabled(ctx, in.Body.Enabled)
		return &struct{ Body updates.State }{st}, apiError(err)
	})
}

// autostartError reports refusals, such as a `go run` binary, as the user's to fix.
func autostartError(err error) error {
	if err == nil {
		return nil
	}
	return huma.Error422UnprocessableEntity(err.Error())
}
