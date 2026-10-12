package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The browser broker's endpoints (docs/browser.md): a teammate's browser VM, the user's takeover,
// the action log and the teammate's browser patterns. The live view is a WebSocket.

type browserPath struct {
	Env      string `path:"env" doc:"Environment ID"`
	Teammate string `path:"teammate" doc:"Teammate ID"`
}

// BrowserPatternInput is a browser pattern to add.
type BrowserPatternInput struct {
	Action  string `json:"action,omitempty" maxLength:"32" doc:"A browser action such as click or fill, or *; ignored for sensitive"`
	Origin  string `json:"origin" minLength:"1" maxLength:"300" doc:"scheme://host[:port], *.example.com or *"`
	Role    string `json:"role,omitempty" maxLength:"64"`
	Label   string `json:"label,omitempty" maxLength:"200" doc:"Glob on the accessible name; * matches anything"`
	Verdict string `json:"verdict" enum:"allow,deny,sensitive"`
}

func (s *Server) browserError(err error) error {
	switch {
	case errors.Is(err, browser.ErrBadPattern), errors.Is(err, browser.ErrBadURL):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, browser.ErrNotLive), errors.Is(err, browser.ErrNoTakeover):
		return huma.Error409Conflict(err.Error())
	}
	return apiError(err)
}

func (s *Server) registerBrowser(api huma.API) {
	need := func() error {
		if s.Browser == nil {
			return huma.Error503ServiceUnavailable("the browser broker is not running")
		}
		return nil
	}
	huma.Register(api, huma.Operation{
		OperationID: "getBrowser", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{teammate}/browser", Tags: []string{"browser"},
	}, func(ctx context.Context, in *browserPath) (*struct{ Body browser.BrowserStatus }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		st, err := s.Browser.Status(ctx, in.Env, in.Teammate)
		return &struct{ Body browser.BrowserStatus }{st}, s.browserError(err)
	})

	type navigateBrowserIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     struct {
			URL string `json:"url" minLength:"1" maxLength:"4000" doc:"Absolute http or https URL to open while the user has taken over"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "navigateBrowser", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{teammate}/browser/navigate", Tags: []string{"browser"},
	}, func(ctx context.Context, in *navigateBrowserIn) (*struct{ Body browser.BrowserStatus }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		st, err := s.Browser.Navigate(ctx, in.Env, in.Teammate, in.Body.URL)
		if err != nil {
			return nil, s.browserError(err)
		}
		return &struct{ Body browser.BrowserStatus }{st}, nil
	})

	type browserAction struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Action   string `path:"action" enum:"start,stop" doc:"start boots the browser VM, stop stops it (its profile stays)"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "controlBrowser", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{teammate}/browser/{action}", Tags: []string{"browser"},
	}, func(ctx context.Context, in *browserAction) (*struct{ Body browser.BrowserStatus }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		var err error
		if in.Action == "start" {
			err = s.Browser.Start(ctx, in.Env, in.Teammate)
		} else {
			err = s.Browser.Stop(ctx, in.Env, in.Teammate)
		}
		if err != nil {
			return nil, s.browserError(err)
		}
		st, err := s.Browser.Status(ctx, in.Env, in.Teammate)
		return &struct{ Body browser.BrowserStatus }{st}, s.browserError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteBrowser", Method: http.MethodDelete, Path: "/api/environments/{env}/teammates/{teammate}/browser", Tags: []string{"browser"},
		DefaultStatus: http.StatusNoContent, Description: "Deletes the browser VM with its profile: cookies and logins. The action log stays.",
	}, func(ctx context.Context, in *browserPath) (*struct{}, error) {
		if err := need(); err != nil {
			return nil, err
		}
		if _, err := s.Store.Teammate(ctx, in.Env, in.Teammate); err != nil {
			return nil, apiError(err)
		}
		return nil, s.browserError(s.Browser.Remove(ctx, in.Env, in.Teammate))
	})

	type takeoverIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     struct {
			On bool `json:"on" doc:"true: the user drives and agent calls pause; false: hand back to the agents"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setBrowserTakeover", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{teammate}/browser/takeover", Tags: []string{"browser"},
	}, func(ctx context.Context, in *takeoverIn) (*struct{ Body browser.BrowserStatus }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		if err := s.Browser.SetTakeover(ctx, in.Env, in.Teammate, in.Body.On); err != nil {
			return nil, s.browserError(err)
		}
		st, err := s.Browser.Status(ctx, in.Env, in.Teammate)
		return &struct{ Body browser.BrowserStatus }{st}, s.browserError(err)
	})

	type actionsIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Session  string `query:"session" doc:"Only this session's actions"`
		Limit    int    `query:"limit" minimum:"1" maximum:"500" default:"200"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listBrowserActions", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{teammate}/browser/actions", Tags: []string{"browser"},
		Description: "The browser's action log, newest first. Screenshots are at .../actions/{id}/screenshot.",
	}, func(ctx context.Context, in *actionsIn) (*struct{ Body []store.BrowserAction }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		list, err := s.Browser.Actions(ctx, in.Env, in.Teammate, in.Session, in.Limit)
		return &struct{ Body []store.BrowserAction }{list}, s.browserError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "listBrowserPatterns", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{teammate}/browser/patterns", Tags: []string{"browser"},
	}, func(ctx context.Context, in *browserPath) (*struct{ Body []store.BrowserPattern }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		list, err := s.Browser.Patterns(ctx, in.Env, in.Teammate)
		return &struct{ Body []store.BrowserPattern }{list}, s.browserError(err)
	})

	type patternIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     BrowserPatternInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createBrowserPattern", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{teammate}/browser/patterns", Tags: []string{"browser"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *patternIn) (*struct{ Body store.BrowserPattern }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		p, err := s.Browser.AddPattern(ctx, store.BrowserPattern{EnvironmentID: in.Env, TeammateID: in.Teammate, Action: in.Body.Action,
			Origin: in.Body.Origin, Role: in.Body.Role, Label: in.Body.Label, Verdict: in.Body.Verdict})
		return &struct{ Body store.BrowserPattern }{p}, s.browserError(err)
	})

	type patternPath struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		ID       string `path:"id" doc:"Pattern ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "deleteBrowserPattern", Method: http.MethodDelete, Path: "/api/environments/{env}/teammates/{teammate}/browser/patterns/{id}", Tags: []string{"browser"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *patternPath) (*struct{}, error) {
		if err := need(); err != nil {
			return nil, err
		}
		return nil, apiError(s.Store.DeleteBrowserPattern(ctx, in.Env, in.Teammate, in.ID))
	})
}

// browserScreenshot serves the screenshot of one action log entry.
func (s *Server) browserScreenshot(w http.ResponseWriter, r *http.Request) {
	if s.Browser == nil {
		http.Error(w, "the browser broker is not running", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path, err := s.Browser.ShotPath(r.Context(), r.PathValue("env"), r.PathValue("teammate"), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, f)
}

// browserLive relays the teammate's browser screencast and, while the user has taken over,
// the user's input.
func (s *Server) browserLive(w http.ResponseWriter, r *http.Request) {
	if s.Browser == nil {
		http.Error(w, "the browser broker is not running", http.StatusServiceUnavailable)
		return
	}
	env, teammate := r.PathValue("env"), r.PathValue("teammate")
	if _, err := s.Store.Teammate(r.Context(), env, teammate); err != nil {
		http.Error(w, "teammate not found", http.StatusNotFound)
		return
	}
	// Accept verifies that Origin matches Host, so other sites can't watch or drive.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	if err := s.Browser.Relay(r.Context(), env, teammate, c); err != nil {
		msg := "the live view could not reach the browser"
		if errors.Is(err, browser.ErrNotLive) {
			msg = err.Error()
		}
		c.Close(websocket.StatusTryAgainLater, msg)
	}
}

// forgetBrowser removes a teammate's browser VM and action log before the teammate is
// deleted, unless agent sandboxes still keep the teammate.
func (s *Server) forgetBrowser(ctx context.Context, env, teammate string) error {
	if s.Browser == nil {
		return nil
	}
	list, err := s.Store.Sandboxes(ctx, env)
	if err != nil {
		return apiError(err)
	}
	for _, sb := range list {
		if sb.TeammateID == teammate && sb.Kind == "" {
			return nil // DeleteTeammate refuses it
		}
	}
	return s.browserError(s.Browser.ForgetTeammate(ctx, env, teammate))
}
