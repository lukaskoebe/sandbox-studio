package browser

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

const devTarget1, devTarget2 = "4E1B0C2A9F7D45C38B6E0A1D2C3B4A59", "7A9C1E3B5D7F49A2B4C6D8E0F1A3B5C7"

// fakeChromium is the browser VM's Chromium debugging server: two tabs, the frontend's
// files, and a page socket that echoes. It notes what it was asked and with which headers.
type fakeChromium struct {
	mu       sync.Mutex
	requests []*http.Request
	pageURL  string
}

func (c *fakeChromium) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.requests = append(c.requests, r.Clone(context.Background()))
	page := c.pageURL
	c.mu.Unlock()
	if r.Host != "127.0.0.1:9333" {
		http.Error(w, "Host header is specified and is not an IP address or localhost.", http.StatusInternalServerError)
		return
	}
	switch {
	case r.URL.Path == "/json/list":
		_, _ = w.Write([]byte(`[{"id":"` + devTarget2 + `","type":"page","title":"Other","url":"https://other.example/"},
			{"id":"worker","type":"service_worker","url":"` + page + `"},
			{"id":"` + devTarget1 + `","type":"page","title":"Sign in","url":"` + page + `"}]`))
	case strings.HasPrefix(r.URL.Path, "/devtools/page/"):
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		typ, msg, err := ws.Read(r.Context())
		if err == nil {
			_ = ws.Write(r.Context(), typ, append([]byte(strings.TrimPrefix(r.URL.Path, "/devtools/page/")+" "), msg...))
		}
	case r.URL.Path == "/devtools/devtools_app.html":
		http.SetCookie(w, &http.Cookie{Name: "chromium", Value: "x"})
		_, _ = w.Write([]byte(fakeDevToolsPage))
	case strings.HasPrefix(r.URL.Path, "/devtools/"):
		http.SetCookie(w, &http.Cookie{Name: "chromium", Value: "x"})
		_, _ = w.Write([]byte("frontend " + r.URL.Path))
	default:
		_, _ = w.Write([]byte("leaked " + r.URL.Path))
	}
}

// fakeDevToolsPage is the start of Chromium's devtools_app.html.
const fakeDevToolsPage = `<!DOCTYPE html>
<meta http-equiv="Content-Security-Policy" content="script-src 'self'">
<script type="module" src="./entrypoints/devtools_app/devtools_app.js"></script>
<body class="undocked" id="-blink-dev-tools">`

func (c *fakeChromium) seen() []*http.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*http.Request(nil), c.requests...)
}

func TestDevTools(t *testing.T) {
	f := newFixture(t)
	chromium := &fakeChromium{}
	upstream := httptest.NewServer(chromium)
	t.Cleanup(upstream.Close)
	f.fb.mu.Lock()
	f.fb.devtoolsPort = 9333
	f.fb.dial = func(port int) (net.Conn, error) {
		if port != 9333 {
			return nil, errors.New("nothing listens there")
		}
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}
	chromium.pageURL = f.fb.url
	f.fb.mu.Unlock()

	if _, err := f.svc.OpenDevTools(f.ctx, f.env, f.ada.ID, ""); !errors.Is(err, ErrNotLive) {
		t.Fatalf("a browser that never started: %v", err)
	}
	if err := f.svc.Start(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.OpenDevTools(f.ctx, f.env, f.ada.ID, "nosuch"); !errors.Is(err, ErrNoSession) {
		t.Errorf("no such session: %v", err)
	}
	dt, err := f.svc.OpenDevTools(f.ctx, f.env, f.ada.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := webauth.DevToolsToken(webauth.DevToolsHost(dt.Token, "7878")); !ok {
		t.Fatalf("token %q makes no DevTools host", dt.Token)
	}
	// The tab on the session's page, not the first or a worker on it.
	if dt.Target != devTarget1 || dt.Title != "Sign in" || dt.URL != f.fb.url {
		t.Errorf("opened %+v", dt)
	}
	if again, err := f.svc.OpenDevTools(f.ctx, f.env, f.ada.ID, ""); err != nil || again.Token != dt.Token {
		t.Errorf("opening again gave %q (%v), want the same grant %q", again.Token, err, dt.Token)
	}
	last := f.fb.allCommands()
	if cmd := last[len(last)-1]; len(cmd.Args) != 1 || cmd.Args[0] != agentproto.BrowserDevToolsPort || cmd.Screenshot || cmd.Stdin != "" {
		t.Errorf("command = %+v", cmd)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	route := DevToolsRoute(f.svc, next)
	host := webauth.DevToolsHost(dt.Token, "7878")
	get := func(method, host, path string, h http.Header) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost:7878/", nil)
		req.URL.Path, req.URL.RawQuery, _ = strings.Cut(path, "?")
		req.Host = host
		for k, v := range h {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		route.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("GET", "localhost:7878", "/", nil); rec.Code != 299 {
		t.Errorf("Studio's own host went to DevTools: %d", rec.Code)
	}

	rec := get("GET", host, "/", nil)
	want := "/devtools/devtools_app.html?ws=" + url.QueryEscape(host+"/devtools/page/"+devTarget1)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		t.Errorf("/ = %d to %q, want %q", rec.Code, rec.Header().Get("Location"), want)
	}

	rec = get("GET", host, "/devtools/devtools_app.html?ws=x", http.Header{
		"Cookie": {"studio_session=secret"}, "Origin": {"http://" + host}, "Referer": {"http://" + host + "/"},
		"Authorization": {"Basic eDp5"},
	})
	// The page loads Studio's shim for WebKit before its own scripts.
	shimmed := strings.Replace(fakeDevToolsPage, `<script type="module"`, `<script src="./studio-webkit.js"></script>`+"\n"+`<script type="module"`, 1)
	if rec.Code != http.StatusOK || rec.Body.String() != shimmed || rec.Header().Get("Content-Length") != strconv.Itoa(len(shimmed)) {
		t.Errorf("frontend = %d %q (Content-Length %s)", rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"))
	}
	if rec.Header().Get("Set-Cookie") != "" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors http://localhost:*") ||
		rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("frontend headers = %v", rec.Header())
	}
	reqs := chromium.seen()
	up := reqs[len(reqs)-1]
	if up.URL.RawQuery != "ws=x" {
		t.Errorf("upstream query = %q", up.URL.RawQuery)
	}
	for _, h := range []string{"Cookie", "Origin", "Referer", "Authorization", "X-Forwarded-For"} {
		if up.Header.Get(h) != "" {
			t.Errorf("upstream got %s: %q", h, up.Header.Get(h))
		}
	}

	if rec := get("GET", host, "/devtools/other.html", nil); rec.Body.String() != "frontend /devtools/other.html" {
		t.Errorf("another file = %q", rec.Body.String())
	}

	// Studio serves the shim itself, under the frontend's content policy.
	before := len(chromium.seen())
	rec = get("GET", host, "/devtools/studio-webkit.js", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "window.requestIdleCallback = function") ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("shim = %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	if n := len(chromium.seen()); n != before {
		t.Errorf("the shim was asked of Chromium")
	}

	// Nothing but the frontend and the grant's page: no listings, no browser-wide or other
	// tabs' sockets, no other methods, no way around the path check.
	for _, c := range []struct {
		method, path string
		upgrade      bool
		code         int
	}{
		{"GET", "/json/list", false, 404},
		{"GET", "/json/version", false, 404},
		{"GET", "/json", false, 404},
		{"GET", "/devtools/browser/f6c7c29b", true, 404},
		{"GET", "/devtools/browser/f6c7c29b", false, 404},
		{"GET", "/devtools/page/" + devTarget2, true, 404},
		{"GET", "/devtools/page/" + devTarget1, false, 404},
		{"GET", "/devtools/../json/list", false, 404},
		{"GET", "/devtools//json/list", false, 404},
		{"GET", "/devtools/devtools_app.html", true, 404},
		{"GET", "/devtools/studio-webkit.js", true, 404},
		{"GET", "/other", false, 404},
		{"POST", "/devtools/devtools_app.html", false, 405},
		{"PUT", "/", false, 405},
	} {
		h := http.Header{}
		if c.upgrade {
			h.Set("Upgrade", "websocket")
			h.Set("Connection", "Upgrade")
		}
		if rec := get(c.method, host, c.path, h); rec.Code != c.code {
			t.Errorf("%s %s (upgrade %v) = %d, want %d", c.method, c.path, c.upgrade, rec.Code, c.code)
		}
	}
	if n := len(chromium.seen()); n != before {
		t.Errorf("%d refused requests reached Chromium", n-before)
	}
	if rec := get("GET", webauth.DevToolsHost(strings.Repeat("0", 32), "7878"), "/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("a token never issued = %d", rec.Code)
	}

	// The page's socket goes through, to the grant's tab.
	srv := httptest.NewServer(route)
	t.Cleanup(srv.Close)
	dialWS := func(path string) (*websocket.Conn, *http.Response, error) {
		hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("tcp", srv.Listener.Addr().String())
		}}}
		return websocket.Dial(f.ctx, "ws://"+host+path, &websocket.DialOptions{HTTPClient: hc,
			HTTPHeader: http.Header{"Origin": {"http://" + host}}})
	}
	ws, _, err := dialWS("/devtools/page/" + devTarget1)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(f.ctx, websocket.MessageText, []byte(`{"id":1,"method":"DOM.getDocument"}`)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := ws.Read(f.ctx)
	if err != nil || string(msg) != devTarget1+` {"id":1,"method":"DOM.getDocument"}` {
		t.Errorf("socket answered %q (%v)", msg, err)
	}
	ws.CloseNow()

	// A page that shows a credential opens no DevTools and no new socket to it.
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	st.visible = true
	f.svc.mu.Unlock()
	if _, err := f.svc.OpenDevTools(f.ctx, f.env, f.ada.ID, ""); !errors.Is(err, ErrHidden) {
		t.Errorf("a page showing a credential: %v", err)
	}
	if _, res, err := dialWS("/devtools/page/" + devTarget1); err == nil || res == nil || res.StatusCode != http.StatusConflict {
		t.Errorf("socket to a page showing a credential: %v", err)
	}
	f.svc.mu.Lock()
	st.visible = false
	f.svc.mu.Unlock()

	// The grant ends with the browser's run.
	if err := f.svc.Stop(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	if rec := get("GET", host, "/devtools/devtools_app.html", nil); rec.Code != http.StatusGone {
		t.Errorf("after the browser stopped = %d", rec.Code)
	}
	if rec := get("GET", host, "/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("a grant that ended, asked again = %d", rec.Code)
	}
}

// Grants are bounded: the oldest goes when there are too many.
func TestDevToolsGrantsAreBounded(t *testing.T) {
	f := newFixture(t)
	f.svc.mu.Lock()
	defer f.svc.mu.Unlock()
	st := f.svc.stateLocked(f.env, f.ada.ID)
	st.vm, st.session = "vm", "run"
	now := f.svc.now()
	f.svc.devtools = map[string]*devGrant{}
	for i := range devToolsMaxGrants {
		f.svc.devtools[strings.Repeat(string(rune('a'+i%26)), 2)+string(rune('0'+i/26))] = &devGrant{env: f.env, st: st, pg: &st.page,
			vm: "vm", session: "run", created: now.Add(-time.Duration(devToolsMaxGrants-i) * time.Minute)}
	}
	f.svc.pruneDevToolsLocked(now)
	if len(f.svc.devtools) != devToolsMaxGrants-1 {
		t.Fatalf("%d grants after pruning", len(f.svc.devtools))
	}
	if _, ok := f.svc.devtools["aa0"]; ok {
		t.Error("the oldest grant was kept")
	}
}
