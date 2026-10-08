package preview

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
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
