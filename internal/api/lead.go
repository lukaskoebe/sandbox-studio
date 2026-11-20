package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/lead"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// registerLead adds Lead, which writes up each environment's day (docs/lead.md): its state,
// a day's page, Summarize now and its settings. Lead only reads; nothing here acts on a
// teammate, a computer or an approval.
func (s *Server) registerLead(api huma.API) {
	tags := []string{"lead"}
	base := "/api/environments/{env}/lead"
	ready := func(ctx context.Context, env string) error {
		if err := s.checkEnvironment(ctx, env); err != nil {
			return err
		}
		if s.Lead == nil {
			return huma.Error503ServiceUnavailable("Lead is not running")
		}
		return nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "getLead", Method: http.MethodGet, Path: base, Tags: tags,
		Description: "Lead's settings, today so far as Home's card shows it, the written days, today's model calls and the model Lead calls.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body lead.LeadView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.View(ctx, in.Env)
		return &struct{ Body lead.LeadView }{v}, apiError(err)
	})

	type dayIn struct {
		Env string `path:"env" doc:"Environment ID"`
		Day string `path:"day" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"The local date, YYYY-MM-DD"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "getLeadDay", Method: http.MethodGet, Path: base + "/days/{day}", Tags: tags,
		Description: "A day as Lead wrote it, with the facts it was written from and the day as Markdown; for today before it is written, the facts so far.",
	}, func(ctx context.Context, in *dayIn) (*struct{ Body lead.LeadDayView }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.Lead.Day(ctx, in.Env, in.Day)
		return &struct{ Body lead.LeadDayView }{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "summarizeLeadDay", Method: http.MethodPost, Path: base + "/summarize", Tags: tags,
		DefaultStatus: http.StatusAccepted,
		Description:   "Writes today's day now, in the background; a lead event says when it is written. 409 while it is being written.",
	}, func(ctx context.Context, in *envPath) (*struct{}, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		if err := s.Lead.Summarize(in.Env); errors.Is(err, lead.ErrWriting) {
			return nil, huma.Error409Conflict(err.Error())
		} else if err != nil {
			return nil, apiError(err)
		}
		return &struct{}{}, nil
	})

	type settingsIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Name         string `json:"name" minLength:"1" maxLength:"40" doc:"What Lead is called in the sidebar"`
			WriteAt      int    `json:"writeAt" minimum:"0" maximum:"1439" doc:"When the day is written, in minutes after local midnight"`
			Days         int    `json:"days" minimum:"0" maximum:"127" doc:"The days it is written on: bit 0 is Monday, bit 6 Sunday"`
			SkipQuiet    bool   `json:"skipQuiet"`
			OwnComputers bool   `json:"ownComputers"`
			ShowCosts    bool   `json:"showCosts"`
			Heading      bool   `json:"heading"`
			DailyLimit   int64  `json:"dailyLimit" minimum:"0" maximum:"100000000" doc:"What Lead's model calls may cost a day, in USD × 10^6, at most $100"`
			Notify       string `json:"notify" enum:"waiting,always,never"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setLeadSettings", Method: http.MethodPut, Path: base + "/settings", Tags: tags,
		Description: "Saves how Lead writes the day.",
	}, func(ctx context.Context, in *settingsIn) (*struct{ Body store.LeadSettings }, error) {
		if err := ready(ctx, in.Env); err != nil {
			return nil, err
		}
		b := in.Body
		name := strings.TrimSpace(b.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("Lead needs a name")
		}
		v, err := s.Store.SetLeadSettings(ctx, in.Env, store.LeadSettings{Name: name, WriteAt: b.WriteAt, Days: b.Days,
			SkipQuiet: b.SkipQuiet, OwnComputers: b.OwnComputers, ShowCosts: b.ShowCosts, Heading: b.Heading,
			DailyLimit: b.DailyLimit, Notify: b.Notify})
		if err != nil {
			return nil, apiError(err)
		}
		if s.Bus != nil {
			s.Bus.Publish(events.Event{Topic: events.TopicLead, EnvironmentID: in.Env, ID: time.Now().Format(time.DateOnly)})
		}
		return &struct{ Body store.LeadSettings }{v}, nil
	})
}
