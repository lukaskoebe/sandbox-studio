package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// DevTools are Chrome's own DevTools for a page of a teammate's browser, for the user. The
// session's Chromium serves both the DevTools frontend and its debugging protocol on a port in
// the browser VM (BrowserDevToolsPort). Opening DevTools issues a grant: a random token that
// names the host devtools-<token>.localhost on Studio's port, which DevToolsRoute proxies to
// that port over the VM's channel. Only the user's API opens one; agents never reach either.
//
// The token is the only credential, like a keyed preview's key, and the frontend runs on that
// host's origin, so it never runs with Studio's. A grant lasts as long as the browser's run
// and the session, and at most devToolsTTL (docs/browser.md).
type DevTools struct {
	Token  string // the grant; webauth.DevToolsHost names its host
	Target string // the page's DevTools target ID
	Title  string // the page's title and URL when DevTools opened
	URL    string
}

var ErrNoDevTools = errors.New("the browser's DevTools can't be opened")

const (
	devToolsTTL       = 12 * time.Hour
	devToolsMaxGrants = 32
	devToolsWait      = 10 * time.Second
)

// devGrant is one opened DevTools: which run of which session it inspects, and where.
type devGrant struct {
	env, teammate string
	st            *state
	pg            *page
	vm, session   string
	port          int
	target        string
	created       time.Time
}

// OpenDevTools issues a grant for DevTools on the page of a teammate's browser session (""
// for the persistent profile), or hands back the one it issued for the same page of this run.
// The browser must be running; a page that shows a credential refuses, as its picture does.
func (s *Service) OpenDevTools(ctx context.Context, envID, teammateID, session string) (DevTools, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return DevTools{}, err
	}
	st := s.state(envID, teammateID)
	s.mu.Lock()
	pg := st.pageFor(session)
	hidden := pg != nil && pg.visible
	s.mu.Unlock()
	switch {
	case pg == nil:
		return DevTools{}, ErrNoSession
	case hidden:
		return DevTools{}, ErrHidden
	}
	t, err := s.streamTarget(ctx, envID, teammateID, session)
	if err != nil {
		return DevTools{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, devToolsWait)
	defer cancel()
	data, _, err := s.exec(ctx, st, pg, agentproto.BrowserCommand{Args: []string{agentproto.BrowserDevToolsPort}})
	switch {
	case err == nil:
	case errors.Is(err, ErrNotLive), ctx.Err() != nil:
		return DevTools{}, err
	case strings.Contains(err.Error(), "is not allowed"):
		// A VM started before this Studio runs the guest agent it started with.
		return DevTools{}, fmt.Errorf("%w: the browser runs an older guest agent; stop it and open it again", ErrNoDevTools)
	default:
		return DevTools{}, fmt.Errorf("%w: %v", ErrNoDevTools, err)
	}
	var reply struct {
		Port int `json:"port"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.Port < 1 || reply.Port > 65535 {
		return DevTools{}, errors.New("the browser's debugging port was not understood")
	}
	s.mu.Lock()
	want := pg.url
	s.mu.Unlock()
	target, err := s.devToolsTarget(ctx, t.vm, reply.Port, want)
	if err != nil {
		return DevTools{}, fmt.Errorf("%w: %v", ErrNoDevTools, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.streamCurrentLocked(st, pg, envID, t.vm, t.session) {
		return DevTools{}, ErrNotLive
	}
	if pg.visible {
		return DevTools{}, ErrHidden
	}
	now := s.now()
	open := DevTools{Target: target.ID, Title: target.Title, URL: target.URL}
	for token, g := range s.devtools {
		if g.st == st && g.pg == pg && g.vm == t.vm && g.session == t.session && g.port == reply.Port &&
			g.target == target.ID && now.Sub(g.created) < devToolsTTL/2 {
			open.Token = token
			return open, nil
		}
	}
	s.pruneDevToolsLocked(now)
	b := make([]byte, webauth.DevToolsTokenLen/2)
	if _, err := rand.Read(b); err != nil {
		return DevTools{}, err
	}
	open.Token = hex.EncodeToString(b)
	if s.devtools == nil {
		s.devtools = map[string]*devGrant{}
	}
	s.devtools[open.Token] = &devGrant{env: envID, teammate: teammateID, st: st, pg: pg, vm: t.vm, session: t.session,
		port: reply.Port, target: target.ID, created: now}
	return open, nil
}

// pruneDevToolsLocked forgets grants that no longer work, and the oldest past the bound.
func (s *Service) pruneDevToolsLocked(now time.Time) {
	var oldest string
	for token, g := range s.devtools {
		if !s.devGrantLiveLocked(g, now) {
			delete(s.devtools, token)
			continue
		}
		if oldest == "" || g.created.Before(s.devtools[oldest].created) {
			oldest = token
		}
	}
	if len(s.devtools) >= devToolsMaxGrants {
		delete(s.devtools, oldest)
	}
}

func (s *Service) devGrantLiveLocked(g *devGrant, now time.Time) bool {
	return now.Sub(g.created) < devToolsTTL && s.streamCurrentLocked(g.st, g.pg, g.env, g.vm, g.session)
}

// devTarget is one entry of Chromium's /json/list.
type devTarget struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// devToolsTarget picks the tab DevTools inspect: the one on the session's page, else the
// first tab Chromium lists.
func (s *Service) devToolsTarget(ctx context.Context, vm string, port int, pageURL string) (devTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(vm, strconv.Itoa(port))+"/json/list", nil)
	if err != nil {
		return devTarget{}, err
	}
	req.Host = devToolsUpstreamHost(port)
	res, err := s.devToolsTransport().RoundTrip(req)
	if err != nil {
		return devTarget{}, fmt.Errorf("the browser's DevTools did not answer: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return devTarget{}, fmt.Errorf("the browser's DevTools answered %s", res.Status)
	}
	var targets []devTarget
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&targets); err != nil {
		return devTarget{}, errors.New("the browser's list of tabs was not understood")
	}
	var first *devTarget
	for i := range targets {
		t := &targets[i]
		if t.Type != "page" || !validTargetID(t.ID) {
			continue
		}
		if pageURL != "" && t.URL == pageURL {
			return *t, nil
		}
		if first == nil {
			first = t
		}
	}
	if first == nil {
		return devTarget{}, errors.New("the browser has no tab open")
	}
	return *first, nil
}

// validTargetID keeps a target ID to what Chromium makes (32 hex digits), so it can go
// into a URL and a path unescaped.
func validTargetID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// Chromium's debugging server answers only a Host that is an IP address or localhost, as
// its own guard against DNS rebinding.
func devToolsUpstreamHost(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// devToolsTransport dials "<vm>:<port>" over the VM's channel, keeping a few connections to
// each for the frontend's many files.
func (s *Service) devToolsTransport() *http.Transport {
	s.devOnce.Do(func() {
		s.devTransport = &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				vm, p, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				port, err := strconv.Atoi(p)
				if err != nil {
					return nil, err
				}
				return s.Runner.DialTCP(ctx, vm, port)
			},
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     30 * time.Second,
		}
		s.devProxy = &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				g := pr.In.Context().Value(devGrantKey{}).(*devGrant)
				pr.SetURL(&url.URL{Scheme: "http", Host: net.JoinHostPort(g.vm, strconv.Itoa(g.port))})
				pr.Out.Host = devToolsUpstreamHost(g.port)
				// Chromium refuses a WebSocket with an Origin it wasn't told to allow; nothing
				// else of the user's browser is for the VM.
				for _, h := range []string{"Origin", "Cookie", "Authorization", "Referer"} {
					pr.Out.Header.Del(h)
				}
			},
			Transport: s.devTransport,
			ModifyResponse: func(res *http.Response) error {
				res.Header.Del("Set-Cookie")
				devToolsHeaders(res.Header)
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				devToolsPage(w, http.StatusBadGateway, "The browser's DevTools did not answer",
					"The browser may have restarted. Open DevTools again from Studio. ("+err.Error()+")")
			},
		}
	})
	return s.devTransport
}

type devGrantKey struct{}

// devToolsHeaders keep the frontend in Studio's frames only and its URL, which holds the
// token, out of Referer headers. Chromium's page sets its own content policy for the rest.
func devToolsHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "frame-ancestors http://localhost:* https://localhost:* http://127.0.0.1:* https://127.0.0.1:*")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
}

// DevToolsRoute answers the hosts of DevTools grants (webauth.DevToolsHost) and sends every
// other request to next. A grant's host serves:
//
//   - / : a redirect to the frontend, connected to the grant's page;
//   - /devtools/page/<target>: the page's debugging WebSocket, the grant's target only;
//   - /devtools/<file>: the frontend's files.
//
// Nothing else: no /json listings, no browser-wide or other pages' sockets, no other methods.
func DevToolsRoute(s *Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := webauth.DevToolsToken(r.Host)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if s == nil {
			devToolsPage(w, http.StatusNotFound, "No DevTools here", "This Studio runs no browsers.")
			return
		}
		s.serveDevTools(w, r, token)
	})
}

func (s *Service) serveDevTools(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	g := s.devtools[token]
	live := g != nil && s.devGrantLiveLocked(g, s.now())
	if g != nil && !live {
		delete(s.devtools, token)
	}
	hidden := live && g.pg.visible
	s.mu.Unlock()
	switch {
	case g == nil:
		devToolsPage(w, http.StatusNotFound, "No DevTools here", "Open DevTools from the teammate's browser in Studio.")
		return
	case !live:
		devToolsPage(w, http.StatusGone, "These DevTools ended", "The browser stopped or its session closed. Open DevTools again from Studio.")
		return
	}
	p := r.URL.Path
	if p != path.Clean(p) && p != "/devtools/" {
		http.NotFound(w, r)
		return
	}
	upgrade := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	socket := "/devtools/page/" + g.target
	switch {
	case p == "/" && !upgrade:
		scheme := "ws"
		if r.TLS != nil {
			scheme = "wss"
		}
		devToolsHeaders(w.Header())
		q := url.Values{scheme: {r.Host + socket}}
		http.Redirect(w, r, "/devtools/devtools_app.html?"+q.Encode(), http.StatusFound)
		return
	case p == socket && upgrade:
		if hidden {
			// The page shows a credential: no new connection reads it, as no picture of it leaves.
			http.Error(w, ErrHidden.Error(), http.StatusConflict)
			return
		}
	case upgrade, strings.HasPrefix(p, "/devtools/page/"), strings.HasPrefix(p, "/devtools/browser"),
		!strings.HasPrefix(p, "/devtools/"):
		http.NotFound(w, r)
		return
	}
	s.devToolsTransport()
	s.devProxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), devGrantKey{}, g)))
}

// devToolsPage answers a DevTools host with a short page of its own.
func devToolsPage(w http.ResponseWriter, code int, title, body string) {
	devToolsHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<body style="font:15px system-ui;margin:3rem;color:#444"><h1 style="font-size:1.2rem">%s</h1><p>%s</p>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(body))
}
