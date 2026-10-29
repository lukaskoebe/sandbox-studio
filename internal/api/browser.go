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

// BrowserDownloadInput is a download to copy into a sandbox of the teammate.
type BrowserDownloadInput struct {
	Session   string `json:"session,omitempty" maxLength:"64" doc:"The ephemeral session that saved the file; empty for the persistent profile"`
	Name      string `json:"name" minLength:"1" maxLength:"255" doc:"The file's name, as the list shows it"`
	SandboxID string `json:"sandboxId" minLength:"1" maxLength:"64" doc:"The sandbox to copy the file into, under downloads/"`
}

func (s *Server) browserError(err error) error {
	switch {
	case errors.Is(err, browser.ErrBadPattern), errors.Is(err, browser.ErrBadURL), errors.Is(err, browser.ErrBadMove),
		errors.Is(err, browser.ErrBadPoint), errors.Is(err, browser.ErrBadViewport):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, browser.ErrNotLive), errors.Is(err, browser.ErrNoTakeover), errors.Is(err, browser.ErrHidden),
		errors.Is(err, browser.ErrBusy):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, browser.ErrNoSession), errors.Is(err, browser.ErrNoElement):
		return huma.Error404NotFound(err.Error())
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

	type setStealthIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     struct {
			On bool `json:"on" doc:"Run the teammate's browser in stealth mode"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setBrowserStealth", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{teammate}/browser/stealth", Tags: []string{"browser"},
		Description: "Turns stealth mode of the teammate's browser on or off (docs/browser.md#stealth-mode). The browser restarts with its next command, keeping its profile.",
	}, func(ctx context.Context, in *setStealthIn) (*struct{ Body store.Teammate }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		t, err := s.Store.SetTeammateBrowserStealth(ctx, in.Env, in.Teammate, in.Body.On)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body store.Teammate }{t}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "setBrowserSharePages", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{teammate}/browser/share-pages", Tags: []string{"browser"},
		Description: "Sets whether the teammate's agents hear, after the user hands the browser back, which pages it was on while the user drove. Off, that stays private to the user.",
	}, func(ctx context.Context, in *setStealthIn) (*struct{ Body store.Teammate }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		t, err := s.Store.SetTeammateBrowserSharePages(ctx, in.Env, in.Teammate, in.Body.On)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body store.Teammate }{t}, nil
	})

	type navigateBrowserIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     struct {
			URL     string `json:"url" minLength:"1" maxLength:"4000" doc:"Absolute http or https URL to open while the user has taken over"`
			Session string `json:"session,omitempty" maxLength:"32" doc:"An ephemeral session's ID; empty for the persistent profile"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "navigateBrowser", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{teammate}/browser/navigate", Tags: []string{"browser"},
	}, func(ctx context.Context, in *navigateBrowserIn) (*struct{ Body browser.BrowserStatus }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		st, err := s.Browser.Navigate(ctx, in.Env, in.Teammate, in.Body.Session, in.Body.URL)
		if err != nil {
			return nil, s.browserError(err)
		}
		return &struct{ Body browser.BrowserStatus }{st}, nil
	})

	type goBrowserIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     struct {
			To      string `json:"to" enum:"back,forward,reload" doc:"Where to go in the session's history"`
			Session string `json:"session,omitempty" maxLength:"32" doc:"An ephemeral session's ID; empty for the persistent profile"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "goBrowser", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{teammate}/browser/go", Tags: []string{"browser"},
		Description: "Goes back, forward or reloads the page while the user has taken over, like a browser's toolbar.",
	}, func(ctx context.Context, in *goBrowserIn) (*struct{ Body browser.BrowserStatus }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		st, err := s.Browser.Go(ctx, in.Env, in.Teammate, in.Body.Session, in.Body.To)
		if err != nil {
			return nil, s.browserError(err)
		}
		return &struct{ Body browser.BrowserStatus }{st}, nil
	})

	type resizeBrowserIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     struct {
			Width   int    `json:"width" minimum:"320" maximum:"1920" doc:"The page's width in CSS pixels"`
			Height  int    `json:"height" minimum:"200" maximum:"1080" doc:"The page's height in CSS pixels"`
			Session string `json:"session,omitempty" maxLength:"32" doc:"An ephemeral session's ID; empty for the persistent profile"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "resizeBrowser", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{teammate}/browser/viewport", Tags: []string{"browser"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Sets the page size of a running session, so the page fits the user's view of it; the user needn't have taken over. A browser that restarts goes back to agent-browser's own size, 1280×720.",
	}, func(ctx context.Context, in *resizeBrowserIn) (*struct{}, error) {
		if err := need(); err != nil {
			return nil, err
		}
		return nil, s.browserError(s.Browser.Resize(ctx, in.Env, in.Teammate, in.Body.Session, in.Body.Width, in.Body.Height))
	})

	type closeSessionIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Session  string `path:"session" maxLength:"32" doc:"The ephemeral session's ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "closeBrowserSession", Method: http.MethodDelete, Path: "/api/environments/{env}/teammates/{teammate}/browser/sessions/{session}", Tags: []string{"browser"},
		DefaultStatus: http.StatusNoContent, Description: "Ends an ephemeral session: its browser closes and its temporary profile is deleted.",
	}, func(ctx context.Context, in *closeSessionIn) (*struct{}, error) {
		if err := need(); err != nil {
			return nil, err
		}
		return nil, s.browserError(s.Browser.CloseSession(ctx, in.Env, in.Teammate, in.Session))
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
			On      bool   `json:"on" doc:"true: the user drives and agent calls to the session pause; false: hand back to the agents"`
			Session string `json:"session,omitempty" maxLength:"32" doc:"An ephemeral session's ID; empty for the persistent profile"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setBrowserTakeover", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{teammate}/browser/takeover", Tags: []string{"browser"},
	}, func(ctx context.Context, in *takeoverIn) (*struct{ Body browser.BrowserStatus }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		if err := s.Browser.SetTakeover(ctx, in.Env, in.Teammate, in.Body.Session, in.Body.On); err != nil {
			return nil, s.browserError(err)
		}
		st, err := s.Browser.Status(ctx, in.Env, in.Teammate)
		return &struct{ Body browser.BrowserStatus }{st}, s.browserError(err)
	})

	type elementIn struct {
		Env      string  `path:"env" doc:"Environment ID"`
		Teammate string  `path:"teammate" doc:"Teammate ID"`
		X        float64 `query:"x" required:"true" minimum:"0" maximum:"100000" doc:"The point's x in CSS pixels of the page's viewport"`
		Y        float64 `query:"y" required:"true" minimum:"0" maximum:"100000" doc:"The point's y in CSS pixels of the page's viewport"`
		Session  string  `query:"session" maxLength:"32" doc:"An ephemeral session's ID; empty for the persistent profile"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "getBrowserElement", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{teammate}/browser/element", Tags: []string{"browser"},
		Description: "The element at a point of a session's page, for a note the user pins there: what it is, its accessible name, a CSS path, " +
			"its box and the text it shows, never what a form field holds. It doesn't wait for another command (409), and it leaves the page " +
			"and the agents' element refs as they are (docs/browser.md).",
	}, func(ctx context.Context, in *elementIn) (*struct{ Body browser.Element }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		el, err := s.Browser.ElementAt(ctx, in.Env, in.Teammate, in.Session, in.X, in.Y)
		if err != nil {
			return nil, s.browserError(err)
		}
		return &struct{ Body browser.Element }{el}, nil
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

	huma.Register(api, huma.Operation{
		OperationID: "listBrowserDownloads", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{teammate}/browser/downloads", Tags: []string{"browser"},
		Description: "The files the browser saved, newest first: the persistent profile's and those of its open ephemeral sessions. A stopped browser has none to list.",
	}, func(ctx context.Context, in *browserPath) (*struct{ Body []browser.Download }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		list, err := s.Browser.Downloads(ctx, in.Env, in.Teammate)
		return &struct{ Body []browser.Download }{list}, s.browserError(err)
	})

	type downloadIn struct {
		Env      string `path:"env" doc:"Environment ID"`
		Teammate string `path:"teammate" doc:"Teammate ID"`
		Body     BrowserDownloadInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "sendBrowserDownload", Method: http.MethodPost, Path: "/api/environments/{env}/teammates/{teammate}/browser/downloads/send", Tags: []string{"browser"},
		DefaultStatus: http.StatusCreated,
		Description:   "Copies a download into downloads/ of one of the teammate's sandboxes, under its name or a free one. The file is copied as bytes and never opened on the host.",
	}, func(ctx context.Context, in *downloadIn) (*struct{ Body UploadedFile }, error) {
		if err := need(); err != nil {
			return nil, err
		}
		reply, err := s.Browser.SendDownload(ctx, in.Env, in.Teammate, in.Body.Session, in.Body.Name, in.Body.SandboxID)
		return &struct{ Body UploadedFile }{UploadedFile{Path: "downloads/" + reply.Name, Name: reply.Name, Size: reply.Size}}, s.browserError(err)
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

// browserFrame serves the latest screencast frame of a session of the teammate's browser
// (?session= an ephemeral session's ID, else the persistent profile) as a JPEG.
func (s *Server) browserFrame(w http.ResponseWriter, r *http.Request) {
	if s.Browser == nil {
		http.Error(w, "the browser broker is not running", http.StatusServiceUnavailable)
		return
	}
	env, teammate := r.PathValue("env"), r.PathValue("teammate")
	if _, err := s.Store.Teammate(r.Context(), env, teammate); err != nil {
		http.Error(w, "teammate not found", http.StatusNotFound)
		return
	}
	jpeg, err := s.Browser.Frame(r.Context(), env, teammate, r.URL.Query().Get("session"))
	switch {
	case errors.Is(err, browser.ErrNoSession):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, browser.ErrNotLive), errors.Is(err, browser.ErrHidden):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "the browser's picture could not be read", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(jpeg)
}

// browserLive relays the screencast of a session of the teammate's browser (?session= an
// ephemeral session's ID, else the persistent profile) and, while the user has taken that
// session over, the user's input.
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
	if err := s.Browser.Relay(r.Context(), env, teammate, r.URL.Query().Get("session"), c); err != nil {
		msg := "the live view could not reach the browser"
		if errors.Is(err, browser.ErrNotLive) || errors.Is(err, browser.ErrNoSession) {
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
