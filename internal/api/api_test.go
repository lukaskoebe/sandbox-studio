package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// newTestServer returns the guarded API and the server behind it, for tests that need the
// store, policy engine or event bus.
func newTestServer(t *testing.T) (http.Handler, *Server) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := &events.Bus{}
	s := &Server{
		Store:  st,
		Policy: &policy.Engine{Store: st, Bus: bus, Hold: 5 * time.Second},
		Bus:    bus,
		Conns:  &gateway.ConnLog{},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Addr:   "127.0.0.1:7878",
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return Guard(mux), s
}

func do(h http.Handler, method, url, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	h, _ := newTestServer(t)
	rec := do(h, "GET", "http://localhost:7878/api/health", "")
	var body Health
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || body.Status != "ok" {
		t.Fatalf("%d %q (%v)", rec.Code, rec.Body.String(), err)
	}
}

func TestEnvironments(t *testing.T) {
	h, _ := newTestServer(t)
	if rec := do(h, "POST", "http://localhost:7878/api/environments", `{"name":"work"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "http://localhost:7878/api/environments", `{"name":"work"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "GET", "http://localhost:7878/api/environments", "")
	var envs []store.Environment
	if err := json.Unmarshal(rec.Body.Bytes(), &envs); err != nil || len(envs) != 1 || envs[0].Name != "work" {
		t.Fatalf("list: %s (%v)", rec.Body, err)
	}
}

func TestGuard(t *testing.T) {
	h, _ := newTestServer(t)
	for _, tc := range []struct {
		name, method, url string
		header            []string
		want              int
	}{
		{"rebound host", "GET", "http://evil.example:7878/api/health", nil, http.StatusMisdirectedRequest},
		{"loopback ip", "GET", "http://127.0.0.1:7878/api/health", nil, http.StatusOK},
		{"cross-site post", "POST", "http://localhost:7878/api/environments", []string{"Origin", "http://3000-x.localhost:7878"}, http.StatusForbidden},
		{"same-origin post", "POST", "http://localhost:7878/api/environments", []string{"Origin", "http://localhost:7878"}, http.StatusCreated},
		{"cli post", "POST", "http://localhost:7878/api/environments", nil, http.StatusConflict},
	} {
		if rec := do(h, tc.method, tc.url, `{"name":"x"}`, tc.header...); rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
}
