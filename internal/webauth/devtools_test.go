package webauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevToolsHosts(t *testing.T) {
	token := strings.Repeat("0a", 16)
	host := DevToolsHost(token, "7878")
	if host != "devtools-"+token+".localhost:7878" {
		t.Fatalf("host = %q", host)
	}
	if got, ok := DevToolsToken(host); !ok || got != token {
		t.Fatalf("token of %q = %q, %v", host, got, ok)
	}
	if got, ok := DevToolsToken(strings.ToUpper(host)); !ok || got != token {
		t.Fatalf("upper case: %q, %v", got, ok)
	}
	for _, bad := range []string{
		"localhost:7878",
		"devtools-.localhost:7878",
		"devtools-" + token[:31] + ".localhost:7878",
		"devtools-" + token + "0.localhost:7878",
		"devtools-" + strings.Repeat("zz", 16) + ".localhost:7878",
		"x.devtools-" + token + ".localhost:7878",
		"devtools-" + token + ".example:7878",
		"9222-" + token + ".localhost:7878",
	} {
		if _, ok := DevToolsToken(bad); ok {
			t.Errorf("%q read as a DevTools host", bad)
		}
	}
}

// A DevTools host reaches the broker's route with no cookie; the route checks the grant.
func TestMiddlewareLetsDevToolsHostsThrough(t *testing.T) {
	a := testAuth(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost:7878/devtools/devtools_app.html", nil)
	req.Host = DevToolsHost(strings.Repeat("ab", 16), "7878")
	rec := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("X-Downstream") == "" {
		t.Fatalf("DevTools host response = %d", rec.Code)
	}
}
