package preview

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

func TestParse(t *testing.T) {
	for host, want := range map[string]struct {
		id   string
		port int
		ok   bool
	}{
		"3000-abc.localhost:7878": {"abc", 3000, true},
		"8080-abc.localhost":      {"abc", 8080, true},
		"localhost:7878":          {},
		"3000-abc.example.com":    {},
		"x-abc.localhost":         {},
		"99999-abc.localhost":     {},
		"3000-.localhost":         {},
		"a.3000-abc.localhost":    {},
		// A host that carries its key; webauth checks the key.
		"3000-abc-k3y.localhost:7878": {"abc", 3000, true},
		"3000-abc-.localhost":         {},
		"3000--k3y.localhost":         {},
		"3000-abc-k-y.localhost":      {},
	} {
		id, port, ok := Parse(host)
		if id != want.id || port != want.port || ok != want.ok {
			t.Errorf("Parse(%q) = %q %d %v, want %+v", host, id, port, ok, want)
		}
	}
}

func TestRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "guest says hi to "+r.Host)
	}))
	defer upstream.Close()

	var gotID string
	var gotPort int
	dial := func(ctx context.Context, id string, port int) (net.Conn, error) {
		gotID, gotPort = id, port
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}
	var counted []string
	count := func(id string, port, status int) {
		counted = append(counted, fmt.Sprintf("%s:%d %d", id, port, status))
	}
	h := Route(dial, count, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "studio")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://5173-sb1.localhost:7878/x", nil))
	if body := rec.Body.String(); body != "guest says hi to 5173-sb1.localhost:7878" || gotID != "sb1" || gotPort != 5173 {
		t.Fatalf("preview: %d %q via %s:%d", rec.Code, body, gotID, gotPort)
	}

	if rec.Header().Get("Referrer-Policy") != "" {
		t.Fatalf("a plain preview host got a Referrer-Policy: %q", rec.Header().Get("Referrer-Policy"))
	}
	// A host that carries its key reaches the same sandbox, and sends it to no other site.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://5173-sb1-k3y.localhost:7878/x", nil))
	if gotID != "sb1" || gotPort != 5173 || rec.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("keyed preview: %d via %s:%d, Referrer-Policy %q", rec.Code, gotID, gotPort, rec.Header().Get("Referrer-Policy"))
	}
	counted = counted[:1]

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost:7878/", nil))
	if rec.Body.String() != "studio" {
		t.Fatalf("non-preview host: %q", rec.Body.String())
	}

	// A port nothing answers on is a 502, which Home's lines count as an error.
	h = Route(func(context.Context, string, int) (net.Conn, error) { return nil, io.EOF }, count, nil, http.NotFoundHandler())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://8080-sb1.localhost:7878/", nil))
	if fmt.Sprint(counted) != "[sb1:5173 200 sb1:8080 502]" {
		t.Fatalf("counted %v", counted)
	}
}

func TestRouteCookieBoundary(t *testing.T) {
	gotCookies := make(chan []*http.Cookie, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookies <- r.Cookies()
		w.Header().Add("Set-Cookie", webauth.SessionCookie+"=guest; Path=/")
		w.Header().Add("Set-Cookie", webauth.PreviewCookie+"=guest; Path=/")
		w.Header().Add("Set-Cookie", webauth.PreviewFrameCookie+"=guest; Path=/")
		w.Header().Add("Set-Cookie", "domain=guest; Domain=localhost; Path=/")
		w.Header().Add("Set-Cookie", `app=x; Comment="ignored; Domain=localhost; rest"`)
		w.Header().Add("Set-Cookie", "app=kept; Path=/; HttpOnly")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	h := Route(func(ctx context.Context, _ string, _ int) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}, nil, nil, http.NotFoundHandler())
	req := httptest.NewRequest("GET", "http://3000-sb1.localhost:7878/", nil)
	req.Header.Set("Cookie", webauth.SessionCookie+"=studio; app=kept; "+webauth.PreviewCookie+"=ticket; "+webauth.PreviewFrameCookie+"=ticket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	guestCookies := <-gotCookies
	if len(guestCookies) != 1 || guestCookies[0].Name != "app" || guestCookies[0].Value != "kept" {
		t.Fatalf("guest request cookies: got %#v, want only app=kept", guestCookies)
	}
	setCookies := rec.Result().Cookies()
	if len(setCookies) != 1 || setCookies[0].Name != "app" || setCookies[0].Value != "kept" {
		t.Fatalf("browser response cookies: got %#v, want only app=kept", setCookies)
	}
}

func TestRouteFiltersUpgradeResponseCookies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n")
		fmt.Fprintf(rw, "Set-Cookie: %s=guest; Path=/\r\n", webauth.SessionCookie)
		fmt.Fprintf(rw, "Set-Cookie: %s=guest; Path=/\r\n", webauth.PreviewCookie)
		fmt.Fprint(rw, "Set-Cookie: domain=guest; Domain=localhost; Path=/\r\n")
		fmt.Fprint(rw, "Set-Cookie: app=kept; Path=/; HttpOnly\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		var b [1]byte
		_, _ = conn.Read(b[:])
	}))
	defer upstream.Close()

	// Counting the request leaves the connection to be taken over.
	counted := make(chan int, 1)
	proxy := httptest.NewServer(Route(func(ctx context.Context, _ string, _ int) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}, func(_ string, _, status int) { counted <- status }, nil, http.NotFoundHandler()))
	defer proxy.Close()
	_, studioPort, err := net.SplitHostPort(proxy.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", proxy.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("GET", proxy.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "3000-sb1.localhost:" + studioPort
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", req.Host)
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status: got %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	setCookies := resp.Cookies()
	if len(setCookies) != 1 || setCookies[0].Name != "app" || setCookies[0].Value != "kept" {
		t.Fatalf("upgrade response cookies: got %#v, want only app=kept", setCookies)
	}
	conn.Close()
	if status := <-counted; status >= 500 {
		t.Fatalf("upgrade counted as %d", status)
	}
}

func TestGuardScript(t *testing.T) {
	for in, want := range map[string]string{
		"self.x=1":                 workerGuard + "self.x=1",
		"\"use strict\";self.x=1":  "\"use strict\";;" + workerGuard + "self.x=1",
		"  'use strict'\nself.x=1": "  'use strict';" + workerGuard + "\nself.x=1",
		"import a from './a.js'":   workerGuard + "import a from './a.js'",
	} {
		if got := string(guardScript([]byte(in))); got != want {
			t.Errorf("guardScript(%q) = %q, want %q", in, got, want)
		}
	}
}

// A service worker's script gets the guard, decoded from the guest's gzip; other scripts and
// pages pass through as the guest sent them.
func TestRouteGuardsServiceWorkers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		if r.Header.Get("Accept-Encoding") == "gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = io.WriteString(zw, "self.v=1")
			zw.Close()
			return
		}
		_, _ = io.WriteString(w, "self.v=1")
	}))
	defer upstream.Close()
	dial := func(ctx context.Context, id string, port int) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}
	h := Route(dial, nil, nil, http.NotFoundHandler())

	req := httptest.NewRequest("GET", "http://5173-sb1.localhost:7878/sw.js", nil)
	req.Header.Set("Service-Worker", "script")
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if body := rec.Body.String(); body != workerGuard+"self.v=1" || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("worker script: %q, encoding %q", body, rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Content-Length") != fmt.Sprint(len(workerGuard)+len("self.v=1")) {
		t.Fatalf("worker script length: %q", rec.Header().Get("Content-Length"))
	}

	req = httptest.NewRequest("GET", "http://5173-sb1.localhost:7878/app.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if body := rec.Body.String(); body != "self.v=1" {
		t.Fatalf("plain script: %q", body)
	}
}

// A framed page's basic auth challenge asks Studio instead of the browser; the sign-in given
// there goes with each request until the app refuses it. A tab of its own keeps the challenge.
func TestRouteBasicAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "dev" || p != "secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="Stag</script>ing", charset="UTF-8"`)
			w.Header().Set("X-Frame-Options", "DENY")
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "in")
	}))
	defer upstream.Close()
	dial := func(ctx context.Context, id string, port int) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}
	logins := NewLogins()
	h := Route(dial, nil, logins, http.NotFoundHandler())
	get := func(dest string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://5173-sb1.localhost:7878/", nil)
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Dest", dest)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := get("iframe")
	body := rec.Body.String()
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" || rec.Header().Get("X-Frame-Options") != "" ||
		!strings.Contains(body, `"realm":"Stag\u003c/script\u003eing"`) || !strings.Contains(body, `"failed":false`) {
		t.Fatalf("framed challenge: %d %v %s", rec.Code, rec.Header(), body)
	}
	if rec := get("document"); rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("a tab of its own lost the challenge: %v", rec.Header())
	}

	logins.Set("sb1", 5173, "dev", "secret")
	if rec := get("iframe"); rec.Code != http.StatusOK || rec.Body.String() != "in" {
		t.Fatalf("signed in: %d %s", rec.Code, rec.Body)
	}
	logins.Set("sb1", 5173, "dev", "wrong")
	if rec := get("iframe"); !strings.Contains(rec.Body.String(), `"failed":true`) {
		t.Fatalf("refused sign-in: %s", rec.Body)
	}
	if _, ok := logins.get("sb1", 5173); ok {
		t.Fatal("a refused sign-in was kept")
	}
	if c, ok := basicRealm([]string{`Bearer realm="x"`, `basic realm=staging`}); !ok || c != "staging" {
		t.Fatalf("realm %q %v", c, ok)
	}
}
