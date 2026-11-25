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

// A page's CORS preflight is decided as the request it asks to send.
func TestPreflightNamesItsRequest(t *testing.T) {
	for _, c := range []struct {
		name   string
		header map[string]string
		want   string
	}{
		{"page preflight", map[string]string{"Origin": "http://localhost:5173", "Access-Control-Request-Method": "post"}, "POST"},
		{"no Origin", map[string]string{"Access-Control-Request-Method": "POST"}, ""},
		{"no method asked", map[string]string{"Origin": "http://localhost:5173"}, ""},
		{"not a method rules name", map[string]string{"Origin": "http://localhost:5173", "Access-Control-Request-Method": "PROPFIND"}, ""},
	} {
		r, _ := http.NewRequest(http.MethodOptions, "https://api.example.com/v1/items", nil)
		for k, v := range c.header {
			r.Header.Set(k, v)
		}
		info := requestInfo(r, "api.example.com")
		if info.Method != http.MethodOptions || info.Preflight != c.want {
			t.Errorf("%s: %s, preflight of %q, want %q", c.name, info.Method, info.Preflight, c.want)
		}
	}
	r, _ := http.NewRequest(http.MethodPost, "https://api.example.com/v1/items", nil)
	r.Header.Set("Origin", "http://localhost:5173")
	r.Header.Set("Access-Control-Request-Method", "POST")
	if info := requestInfo(r, "api.example.com"); info.Preflight != "" {
		t.Errorf("a POST counted as a preflight: %+v", info)
	}
}
