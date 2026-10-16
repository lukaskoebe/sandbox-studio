package gateway

import (
	"crypto/tls"
	"net/http"
	"testing"
)

// Chromium's report uploads and their preflights are reports; a page's own requests, its CORS
// preflights among them, aren't.
func TestIsReport(t *testing.T) {
	request := func(method string, header map[string]string) *http.Request {
		r, err := http.NewRequest(method, "https://a.nel.cloudflare.com/report/v4", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.TLS = &tls.ConnectionState{}
		for k, v := range header {
			r.Header.Set(k, v)
		}
		return r
	}
	preflight := map[string]string{"Origin": "https://openai.com", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"}
	pagePreflight := map[string]string{"Sec-Fetch-Mode": "cors", "Origin": "https://openai.com", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"}
	for _, c := range []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"reporting upload", request(http.MethodPost, map[string]string{"Content-Type": "application/reports+json"}), true},
		{"CSP report", request(http.MethodPost, map[string]string{"Content-Type": "application/csp-report; charset=utf-8"}), true},
		{"upload preflight", request(http.MethodOptions, preflight), true},
		{"page preflight", request(http.MethodOptions, pagePreflight), false},
		{"page POST", request(http.MethodPost, map[string]string{"Content-Type": "application/json", "Sec-Fetch-Mode": "cors"}), false},
		{"page GET", request(http.MethodGet, map[string]string{"Sec-Fetch-Mode": "no-cors"}), false},
	} {
		if got := isReport(c.r); got != c.want {
			t.Errorf("%s: isReport = %v, want %v", c.name, got, c.want)
		}
	}
	plain := request(http.MethodOptions, preflight)
	plain.TLS = nil
	if isReport(plain) {
		t.Error("a preflight over plain HTTP, which carries no Sec-Fetch headers, counted as a report")
	}
}
