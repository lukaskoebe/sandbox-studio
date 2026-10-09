package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuardRejectsDifferentSchemeAndMalformedOrigins(t *testing.T) {
	h := Guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, origin := range []string{
		"https://localhost:7878", "null", "http://user@localhost:7878",
		"http://localhost:7878/path", "http://localhost:7878?query", "http://localhost:7878#fragment",
	} {
		t.Run(origin, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://localhost:7878/api/auth/exchange", nil)
			r.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("origin %q accepted: %d", origin, w.Code)
			}
		})
	}
}
