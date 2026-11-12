package webauth

import (
	"net/http"
	"strings"
	"testing"
)

func TestWithoutParamKeepsTheRestAsWritten(t *testing.T) {
	for _, tc := range []struct{ raw, query, value string }{
		{"dev-sw&__studio_sw=abc", "dev-sw", "abc"},
		{"__studio_sw=abc", "", "abc"},
		{"b=2&__studio_sw=abc&a=1", "b=2&a=1", "abc"},
		{"x=__studio_sw", "x=__studio_sw", ""},
	} {
		query, value, _ := withoutParam(tc.raw, WorkerParam)
		if query != tc.query || value != tc.value {
			t.Errorf("%q: %q %q", tc.raw, query, value)
		}
	}
}

// The capability lets a worker's script fetch in without cookies, on its own host only, and
// never reaches the app; any other request with it still needs the cookie.
func TestWorkerCapabilityOpensOnlyWorkerFetches(t *testing.T) {
	a := testAuth(t)
	host := "5173-sb1.localhost:7878"
	token := a.workerToken(host)
	var seen string
	var carried string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, carried = r.URL.RequestURI(), WorkerToken(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	worker := func(dest, site string) http.Header {
		h := make(http.Header)
		h.Set("Sec-Fetch-Dest", dest)
		h.Set("Sec-Fetch-Site", site)
		return h
	}

	rec := request(a, http.MethodGet, "http://"+host+"/sw.js?dev-sw&"+WorkerParam+"="+token, host, worker("serviceworker", "same-origin"), "", next)
	if rec.Code != http.StatusNoContent || seen != "/sw.js?dev-sw" || carried != token {
		t.Fatalf("worker fetch: %d, app saw %q with %q", rec.Code, seen, carried)
	}
	for name, tc := range map[string]struct {
		host    string
		token   string
		headers http.Header
	}{
		"a page's fetch":       {host, token, worker("empty", "same-origin")},
		"another site":         {host, token, worker("serviceworker", "cross-site")},
		"no fetch metadata":    {host, token, nil},
		"another preview's":    {host, a.workerToken("5173-sb2.localhost:7878"), worker("serviceworker", "same-origin")},
		"a made-up capability": {host, "nope", worker("serviceworker", "same-origin")},
	} {
		seen = ""
		rec := request(a, http.MethodGet, "http://"+tc.host+"/sw.js?"+WorkerParam+"="+tc.token, tc.host, tc.headers, "", next)
		if rec.Code != http.StatusForbidden || seen != "" {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestWorkerPatchNeedsThePreviewCookieAndCarriesItsHostsCapability(t *testing.T) {
	a := testAuth(t)
	host := "5173-sb1.localhost:7878"
	if rec := request(a, http.MethodGet, "http://"+host+WorkerPatchPath, host, nil, "", downstream()); rec.Code != http.StatusForbidden {
		t.Fatalf("without the cookie: %d", rec.Code)
	}
	ticket, err := a.IssuePreview(host)
	if err != nil {
		t.Fatal(err)
	}
	login := request(a, http.MethodGet, "http://"+host+"/__studio/preview-login?token="+ticket, host, nil, "", downstream())
	headers := make(http.Header)
	for _, c := range login.Result().Cookies() {
		headers.Add("Cookie", c.Name+"="+c.Value)
	}
	rec := request(a, http.MethodGet, "http://"+host+WorkerPatchPath, host, headers, "", downstream())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"`+a.workerToken(host)+`"`) || rec.Header().Get("X-Downstream") != "" {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
}
