package preview

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	} {
		id, port, ok := Parse(host)
		if id != want.id || port != want.port || ok != want.ok {
			t.Errorf("Parse(%q) = %q %d %v, want %+v", host, id, port, ok, want)
		}
	}
}

func TestRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "guest says hi to "+r.Host)
	}))
	defer upstream.Close()

	var gotID string
	var gotPort int
	dial := func(ctx context.Context, id string, port int) (net.Conn, error) {
		gotID, gotPort = id, port
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}
	h := Route(dial, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "studio")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://5173-sb1.localhost:7878/x", nil))
	if body := rec.Body.String(); body != "guest says hi to 5173-sb1.localhost:7878" || gotID != "sb1" || gotPort != 5173 {
		t.Fatalf("preview: %d %q via %s:%d", rec.Code, body, gotID, gotPort)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost:7878/", nil))
	if rec.Body.String() != "studio" {
		t.Fatalf("non-preview host: %q", rec.Body.String())
	}
}

func TestRouteCookieBoundary(t *testing.T) {
	gotCookies := make(chan []*http.Cookie, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookies <- r.Cookies()
		w.Header().Add("Set-Cookie", webauth.SessionCookie+"=guest; Path=/")
		w.Header().Add("Set-Cookie", webauth.PreviewCookie+"=guest; Path=/")
		w.Header().Add("Set-Cookie", "domain=guest; Domain=localhost; Path=/")
		w.Header().Add("Set-Cookie", `app=x; Comment="ignored; Domain=localhost; rest"`)
		w.Header().Add("Set-Cookie", "app=kept; Path=/; HttpOnly")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	h := Route(func(ctx context.Context, _ string, _ int) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}, http.NotFoundHandler())
	req := httptest.NewRequest("GET", "http://3000-sb1.localhost:7878/", nil)
	req.Header.Set("Cookie", webauth.SessionCookie+"=studio; app=kept; "+webauth.PreviewCookie+"=ticket")
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

	proxy := httptest.NewServer(Route(func(ctx context.Context, _ string, _ int) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	}, http.NotFoundHandler()))
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
}
