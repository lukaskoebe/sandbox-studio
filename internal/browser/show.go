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

// KindShow is an agent showing the user a page in its browser. The card waits for the user:
// allowing it takes the wheel, dismissing it, which Watch does too, only clears it. The agent
// never waits for the answer.
const KindShow = "browser.show"

// ShowPayload is what a browser.show card shows.
type ShowPayload struct {
	TeammateID   string `json:"teammateId"`
	TeammateName string `json:"teammateName"`
	SandboxID    string `json:"sandboxId"`
	SandboxName  string `json:"sandboxName"`
	URL          string `json:"url" doc:"The page the browser is on after opening it, in full"`
	Title        string `json:"title,omitempty" doc:"What the agent calls the page"`
	Note         string `json:"note,omitempty" doc:"What the agent wants the user to look at"`
}

// show answers browser_show: it opens the URL in the teammate's browser like browser_open, so
// the browser's rules and approvals apply, and then posts a card for the user. A newer card of
// the same teammate replaces its older one.
func (c *call) show(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Note  string `json:"note"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	origin := Origin(in.URL)
	if origin == "" || len(in.URL) > 4000 {
		return nil, refuse("url must be an absolute http or https URL")
	}
	if _, err := c.openPage(ctx, in.URL, origin); err != nil {
		return nil, err
	}
	teammate, err := c.s.Store.Teammate(ctx, c.sb.EnvironmentID, c.sb.TeammateID)
	if err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	at := c.pg.url
	c.s.mu.Unlock()
	if at == "" {
		at = in.URL
	}
	title := clip(strings.TrimSpace(in.Title), 120)
	raw, _ := json.Marshal(ShowPayload{TeammateID: teammate.ID, TeammateName: teammate.Name, SandboxID: c.sb.ID,
		SandboxName: c.sb.Name, URL: at, Title: title, Note: clip(strings.TrimSpace(in.Note), 500)})
	what := title
	if what == "" {
		what = at
	}
	a, _, err := c.s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: c.sb.EnvironmentID, SandboxID: c.sb.ID, Kind: KindShow,
		Subject: fmt.Sprintf("%s wants to show you %s", teammate.Name, what), Payload: raw})
	if err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	older := c.st.shown
	c.st.shown = a.ID
	c.s.mu.Unlock()
	if older != "" && older != a.ID {
		// The user may have answered it already.
		if err := c.s.Store.DecideApproval(ctx, c.sb.EnvironmentID, older, store.StatusDismissed, ""); err != nil && !errors.Is(err, store.ErrNotFound) {
			c.s.log().Warn("replacing a browser.show card", "approval", older, "err", err)
		}
	}
	c.s.notify(c.sb.EnvironmentID)
	return c.scrub(fmt.Sprintf("Showing %s in your browser. The user has a card to watch it or take the wheel; "+
		"carry on without waiting for them. If they take the wheel, your browser calls are paused until they hand it back.", at)), nil
}

// decideShow settles a browser.show card. Allowing it hands the user the persistent profile's
// wheel; the other decisions only clear the card.
func (s *Service) decideShow(ctx context.Context, a store.Approval, d integrations.Decision) error {
	status, err := decisionStatus(d)
	if err != nil {
		return err
	}
	var p ShowPayload
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
	return s.SetTakeover(ctx, a.EnvironmentID, p.TeammateID, "", true)
}
