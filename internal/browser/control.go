package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// KindControl is an agent's request for a session of the browser back while the user drives
// it. Allowing it hands the session back; denying it keeps the user driving.
const KindControl = "browser.control"

// ControlPayload is the request of a browser.control approval.
type ControlPayload struct {
	TeammateID   string `json:"teammateId"`
	TeammateName string `json:"teammateName"`
	SandboxID    string `json:"sandboxId"`
	SandboxName  string `json:"sandboxName"`
	Session      string `json:"session,omitempty" doc:"The ephemeral browser session asked for; empty for the persistent profile"`
	Reason       string `json:"reason" doc:"What the agent wants to do in the browser"`
}

// toolRequestControl answers browser_request_control: it asks the user, who drives the
// session, to hand it back, and waits a while for the answer. It doesn't take the command
// slot, as the user's commands hold it and every other call waits for the handback.
func (s *Service) toolRequestControl(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Reason  string `json:"reason"`
		Session string `json:"session"`
	}
	if err := decodeTool(params, &in); err != nil {
		return nil, err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, integrations.ToolError("say in reason what you want to do in the browser")
	}
	if in.Session != "" && !IsEphemeral(in.Session) {
		return nil, toolError(ErrNoSession)
	}
	st, err := s.callerState(sb)
	if err != nil {
		return nil, toolError(err)
	}
	pg, err := s.downloadPage(st, in.Session, &sb)
	if err != nil {
		return nil, toolError(err)
	}
	s.mu.Lock()
	driving := pg.takeover
	s.mu.Unlock()
	if !driving {
		return "The user is not driving this browser: use it now.", nil
	}
	teammate, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(ControlPayload{TeammateID: teammate.ID, TeammateName: teammate.Name, SandboxID: sb.ID, SandboxName: sb.Name,
		Session: in.Session, Reason: clip(in.Reason, 500)})
	// One request per session while it is pending: asking again counts as an attempt.
	subject := fmt.Sprintf("%s asks for the browser back%s", teammate.Name, sessionNote(in.Session))
	a, created, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: KindControl, Subject: subject, Payload: raw})
	if err != nil {
		return nil, err
	}
	if created {
		s.mu.Lock()
		pg.control = a.ID
		s.mu.Unlock()
		s.notify(sb.EnvironmentID)
		s.record(ctx, st, pg, entry{actor: "agent", sandbox: sb.ID, action: "request control", outcome: "pending", detail: clip(in.Reason, 200)})
	}
	status, err := s.awaitApproval(ctx, sb.EnvironmentID, a.ID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	driving = pg.takeover
	s.mu.Unlock()
	switch {
	case !driving:
		return "The user handed the browser back: use it now. Earlier refs are stale, so take a browser_snapshot first.", nil
	case status == store.StatusDenied:
		return "The user keeps control of the browser for now. Don't ask again right away: carry on with other work, " +
			"and use the browser once they hand it back.", nil
	}
	return "The user has not answered yet; your request stays in Studio and its notification. Carry on with other work, " +
		"or call again later to see whether they handed it back.", nil
}

// decideControl settles a browser.control approval. Allowing it hands the session back to
// the agents.
func (s *Service) decideControl(ctx context.Context, a store.Approval, d integrations.Decision) error {
	status, err := decisionStatus(d)
	if err != nil {
		return err
	}
	var p ControlPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, status, ""); err != nil {
		return err
	}
	s.notify(a.EnvironmentID)
	if status != store.StatusApproved {
		return nil
	}
	if err := s.SetTakeover(ctx, a.EnvironmentID, p.TeammateID, p.Session, false); err != nil && !errors.Is(err, ErrNoSession) {
		return err
	}
	return nil
}

// settleControl moves the agent's pending request for pg to status once the session is handed
// back another way (approved) or the browser stops (dismissed), so it doesn't stay asking.
func (s *Service) settleControl(ctx context.Context, envID string, pg *page, status string) {
	s.mu.Lock()
	id := pg.control
	pg.control = ""
	s.mu.Unlock()
	if id == "" {
		return
	}
	if err := s.Store.DecideApproval(ctx, envID, id, status, ""); err == nil {
		s.notify(envID)
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log().Warn("could not settle a request for the browser", "approval", id, "err", err)
	}
}
