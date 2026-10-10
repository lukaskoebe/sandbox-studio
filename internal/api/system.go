package api

import (
	"context"
	"net/http"
	"time"

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

// The API's names for the system types, so that the generated schemas say what they are.
type (
	// HostCheck is one doctor check.
	HostCheck doctor.Check
	// AutostartStatus describes the start-at-login entry.
	AutostartStatus autostart.Status
	// UpdateState is the update check's setting and last result.
	UpdateState updates.State
)

// DoctorReport is the result of the host checks.
type DoctorReport struct {
	Status    doctor.Status `json:"status" enum:"ok,warn,fail" doc:"The worst status of all checks"`
	Checks    []HostCheck   `json:"checks"`
	CheckedAt time.Time     `json:"checkedAt"`
}

func doctorReport(r doctor.Report) DoctorReport {
	out := DoctorReport{Status: r.Status, CheckedAt: r.CheckedAt, Checks: make([]HostCheck, len(r.Checks))}
	for i, c := range r.Checks {
		out.Checks[i] = HostCheck(c)
	}
	return out
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
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body DoctorReport }, error) {
		if s.System.Doctor == nil {
			return nil, huma.Error503ServiceUnavailable("the doctor is not available")
		}
		return &struct{ Body DoctorReport }{doctorReport(s.System.Doctor(ctx))}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getAutostart", Method: http.MethodGet, Path: "/api/system/autostart", Tags: []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body AutostartStatus }, error) {
		if s.System.Autostart == nil {
			return &struct{ Body AutostartStatus }{}, nil
		}
		st, err := s.System.Autostart.Status()
		return &struct{ Body AutostartStatus }{AutostartStatus(st)}, autostartError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "setAutostart", Method: http.MethodPut, Path: "/api/system/autostart", Tags: []string{"system"},
		Summary: "Start Studio at login, or stop doing so",
	}, func(ctx context.Context, in *toggleIn) (*struct{ Body AutostartStatus }, error) {
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
		return &struct{ Body AutostartStatus }{AutostartStatus(st)}, autostartError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getUpdates", Method: http.MethodGet, Path: "/api/system/updates", Tags: []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body UpdateState }, error) {
		if s.System.Updates == nil {
			return nil, huma.Error503ServiceUnavailable("the update check is not available")
		}
		st, err := s.System.Updates.State(ctx)
		return &struct{ Body UpdateState }{UpdateState(st)}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "setUpdates", Method: http.MethodPut, Path: "/api/system/updates", Tags: []string{"system"},
		Summary: "Turn the daily update check on or off; turning it on checks right away",
	}, func(ctx context.Context, in *toggleIn) (*struct{ Body UpdateState }, error) {
		if s.System.Updates == nil {
			return nil, huma.Error503ServiceUnavailable("the update check is not available")
		}
		st, err := s.System.Updates.SetEnabled(ctx, in.Body.Enabled)
		return &struct{ Body UpdateState }{UpdateState(st)}, apiError(err)
	})
}

// autostartError reports refusals, such as a `go run` binary, as the user's to fix.
func autostartError(err error) error {
	if err == nil {
		return nil
	}
	return huma.Error422UnprocessableEntity(err.Error())
}
