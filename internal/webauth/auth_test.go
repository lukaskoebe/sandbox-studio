package webauth

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func testAuth(t *testing.T) *Auth {
	t.Helper()
	a, err := New(testKey, isTestPreview)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	return a
}

func isTestPreview(host string) bool {
	h := strings.ToLower(host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		h = strings.ToLower(parsed)
	}
	return strings.HasSuffix(h, ".localhost") && strings.Contains(h, "-")
}

func request(a *Auth, method, target, host string, headers http.Header, body string, next http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	if headers != nil {
		req.Header = headers.Clone()
	}
	rec := httptest.NewRecorder()
	a.Middleware(next).ServeHTTP(rec, req)
	return rec
}

func downstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Downstream", "reached")
		w.WriteHeader(http.StatusNoContent)
	})
}

func exchange(t *testing.T, a *Auth, host, token string) *httptest.ResponseRecorder {
	t.Helper()
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return request(a, http.MethodPost, "http://"+host+"/api/auth/exchange", host, headers, `{"token":"`+token+`"}`, downstream())
}

func TestNewRequiresAndCopiesPersistentInstallKey(t *testing.T) {
	if _, err := New([]byte("short"), isTestPreview); err == nil {
		t.Fatal("New accepted a short key")
	}

	key := append([]byte(nil), testKey...)
	a, err := New(key, isTestPreview)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	key[0] ^= 0xff
	token := a.IssueLogin()
	rec := exchange(t, a, "localhost:7878", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("exchange status = %d, want 204", rec.Code)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != SessionCookie || cookie.Domain != "" || cookie.Path != "/" || !cookie.HttpOnly || cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != int(sessionLifetime.Seconds()) || cookie.Expires.IsZero() {
		t.Fatalf("session cookie flags = %#v", cookie)
	}

	restart, err := New(testKey, isTestPreview)
	if err != nil {
		t.Fatal(err)
	}
	restart.now = a.now
	req := httptest.NewRequest(http.MethodGet, "http://localhost:7878/api/auth/status", nil)
	req.AddCookie(cookie)
	status := httptest.NewRecorder()
	restart.Middleware(downstream()).ServeHTTP(status, req)
	var result struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &result); err != nil || !result.Authenticated {
		t.Fatalf("same-key restart status = %d %q (%v)", status.Code, status.Body.String(), err)
	}
	expired, err := New(testKey, isTestPreview)
	if err != nil {
		t.Fatal(err)
	}
	expired.now = func() time.Time { return a.now().Add(sessionLifetime) }
	expiredReq := httptest.NewRequest(http.MethodGet, "http://localhost:7878/api/auth/status", nil)
	expiredReq.AddCookie(cookie)
	expiredStatus := httptest.NewRecorder()
	expired.Middleware(downstream()).ServeHTTP(expiredStatus, expiredReq)
	if strings.Contains(expiredStatus.Body.String(), `"authenticated":true`) {
		t.Fatalf("expired session remained valid: %q", expiredStatus.Body.String())
	}

	for _, host := range []string{"localhost:7879", "LOCALHOST:7878"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost:7878/api/auth/status", nil)
		req.Host = host
		req.AddCookie(cookie)
		got := httptest.NewRecorder()
		restart.Middleware(downstream()).ServeHTTP(got, req)
		if host == "LOCALHOST:7878" && got.Code != http.StatusOK {
			t.Errorf("lowercased matching host status = %d", got.Code)
		}
		if host == "localhost:7879" {
			var body map[string]any
			_ = json.Unmarshal(got.Body.Bytes(), &body)
			if body["authenticated"] != false {
				t.Errorf("cookie accepted on another port: %s", got.Body.String())
			}
		}
	}
}

func TestSessionCookieSecureOnlyOnTLS(t *testing.T) {
	a := testAuth(t)
	rec := exchange(t, a, "localhost:7878", a.IssueLogin())
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "https://localhost:7878/api/auth/exchange", strings.NewReader(`{"token":"`+a.IssueLogin()+`"}`))
	req.Host = "localhost:7878"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Content-Type", "application/json")
	secure := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(secure, req)
	if got := secure.Result().Cookies()[0]; !got.Secure {
		t.Fatalf("TLS session cookie missing Secure: %#v", got)
	}
}

func TestLoginExchangeIsAtomicOneUseUnderConcurrency(t *testing.T) {
	a := testAuth(t)
	token := a.IssueLogin()
	const requests = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[int]int{}
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := exchange(t, a, "localhost:7878", token)
			mu.Lock()
			counts[rec.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if counts[http.StatusNoContent] != 1 || counts[http.StatusUnauthorized] != requests-1 {
		t.Fatalf("exchange status counts = %#v", counts)
	}
}

func TestLoginTicketExpiresAndRegistryIsBounded(t *testing.T) {
	a := testAuth(t)
	token := a.IssueLogin()
	a.now = func() time.Time { return time.Date(2026, 10, 9, 12, 10, 0, 0, time.UTC) }
	if rec := exchange(t, a, "localhost:7878", token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired login exchange status = %d", rec.Code)
	}

	a = testAuth(t)
	oldest := a.IssueLogin()
	for i := 0; i < maxOutstandingTickets; i++ {
		if a.IssueLogin() == "" {
			t.Fatal("IssueLogin returned an empty token")
		}
	}
	if rec := exchange(t, a, "localhost:7878", oldest); rec.Code != http.StatusUnauthorized {
		t.Fatalf("oldest token survived registry eviction: %d", rec.Code)
	}
}

func TestUnauthenticatedAPIPathsIncludingStreamsAndWebSocketsAreProtected(t *testing.T) {
	a := testAuth(t)
	paths := []string{
		"/api",
		"/api/events",
		"/api/environments/env/sandboxes/sb/terminals/shell/attach",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://localhost:7878"+path, nil)
			rec := httptest.NewRecorder()
			a.Middleware(downstream()).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Downstream") != "" {
				t.Fatalf("protected response = %d headers=%v body=%q", rec.Code, rec.Header(), rec.Body.String())
			}
			var problem struct {
				Status int    `json:"status"`
				Title  string `json:"title"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Status != 401 || problem.Title != "Authentication required" || problem.Detail != "Open a Studio launch link to connect." {
				t.Fatalf("unauthenticated problem = %q (%v)", rec.Body.String(), err)
			}
		})
	}
}

func TestHostValidationRejectsNonMainAndNonPreviewLocalhostHosts(t *testing.T) {
	a := testAuth(t)
	for _, host := range []string{"evil.example", "evil.localhost:7878", "localhost:99999", "localhost:bad"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost:7878/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		a.Middleware(downstream()).ServeHTTP(rec, req)
		if rec.Code != http.StatusMisdirectedRequest || rec.Header().Get("X-Downstream") != "" {
			t.Errorf("host %q response = %d", host, rec.Code)
		}
	}
	for _, host := range []string{"localhost", "localhost:7878", "127.0.0.1:7878", "[::1]:7878"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost:7878/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		a.Middleware(downstream()).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("main host %q response = %d", host, rec.Code)
		}
	}
}

func TestLaunchRequiresOnlyControlCredentialAndOnlyLaunchAcceptsIt(t *testing.T) {
	a := testAuth(t)
	control := ControlToken(testKey)
	if control == "" || ControlToken([]byte("short")) != "" {
		t.Fatal("unexpected control token result")
	}
	for _, header := range []string{"", "Bearer wrong", "bearer " + control, "Bearer " + control + " extra"} {
		headers := make(http.Header)
		headers.Set("Authorization", header)
		rec := request(a, http.MethodPost, "http://localhost:7878/api/auth/launch", "localhost:7878", headers, "", downstream())
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("launch with Authorization %q status = %d", header, rec.Code)
		}
	}

	// A session cookie cannot substitute for the control credential.
	session := exchange(t, a, "localhost:7878", a.IssueLogin()).Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodPost, "http://localhost:7878/api/auth/launch", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("session cookie authorized launch: %d", rec.Code)
	}

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+control)
	launched := request(a, http.MethodPost, "http://localhost:7878/api/auth/launch", "localhost:7878", headers, "", downstream())
	if launched.Code != http.StatusOK || launched.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("launch response = %d %q", launched.Code, launched.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(launched.Body.Bytes(), &body); err != nil || body.Token == "" {
		t.Fatalf("launch body = %q (%v)", launched.Body.String(), err)
	}
	if got := exchange(t, a, "localhost:7878", body.Token); got.Code != http.StatusNoContent {
		t.Fatalf("launched login token exchange status = %d", got.Code)
	}

	// The control token alone does not authorize an ordinary API route.
	api := request(a, http.MethodGet, "http://localhost:7878/api/health", "localhost:7878", headers, "", downstream())
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("control credential authorized a normal API route: %d", api.Code)
	}
}

func TestAuthenticationEndpointsRejectWrongMethods(t *testing.T) {
	a := testAuth(t)
	for _, tc := range []struct {
		path   string
		method string
	}{
		{"status", http.MethodPost},
		{"exchange", http.MethodGet},
		{"launch", http.MethodGet},
		{"logout", http.MethodGet},
	} {
		req := httptest.NewRequest(tc.method, "http://localhost:7878/api/auth/"+tc.path, nil)
		rec := httptest.NewRecorder()
		a.Middleware(downstream()).ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/auth/%s status = %d", tc.method, tc.path, rec.Code)
		}
	}
}

func TestOversizedSignedCookieIsRejected(t *testing.T) {
	a := testAuth(t)
	req := httptest.NewRequest(http.MethodGet, "http://localhost:7878/api/auth/status", nil)
	req.Header.Set("Cookie", SessionCookie+"="+strings.Repeat("a", maxSignedCookieBytes+1))
	rec := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("oversized cookie authenticated: %d %q", rec.Code, rec.Body.String())
	}
}

func TestLogoutRequiresSessionAndClearsHostOnlyCookie(t *testing.T) {
	a := testAuth(t)
	unauthorized := request(a, http.MethodPost, "http://localhost:7878/api/auth/logout", "localhost:7878", nil, "", downstream())
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("logout without session status = %d", unauthorized.Code)
	}

	session := exchange(t, a, "localhost:7878", a.IssueLogin()).Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodPost, "http://localhost:7878/api/auth/logout", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("logout cookies = %#v", cookies)
	}
	cleared := cookies[0]
	if cleared.Name != SessionCookie || cleared.Value != "" || cleared.Domain != "" || cleared.Path != "/" || !cleared.HttpOnly || cleared.SameSite != http.SameSiteStrictMode || cleared.MaxAge != -1 {
		t.Fatalf("logout clearing cookie = %#v", cleared)
	}
}

func TestExchangeRejectsMalformedAndOversizedBodies(t *testing.T) {
	cases := []struct {
		name, contentType, body string
		want                    int
	}{
		{"missing content type", "", `{"token":"x"}`, http.StatusUnsupportedMediaType},
		{"malformed json", "application/json", `{`, http.StatusBadRequest},
		{"missing token", "application/json", `{}`, http.StatusBadRequest},
		{"unknown field", "application/json", `{"token":"x","other":true}`, http.StatusBadRequest},
		{"trailing value", "application/json", `{"token":"x"} {}`, http.StatusBadRequest},
		{"oversized", "application/json", `{"token":"` + strings.Repeat("x", maxExchangeBody) + `"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testAuth(t)
			headers := make(http.Header)
			headers.Set("Content-Type", tc.contentType)
			rec := request(a, http.MethodPost, "http://localhost:7878/api/auth/exchange", "localhost:7878", headers, tc.body, downstream())
			if rec.Code != tc.want || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("exchange response = %d, want %d; body=%q", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPreviewTicketTargetBindingCookieIsolationAndRedirect(t *testing.T) {
	a := testAuth(t)
	host := "3000-sb.localhost:7878"
	token, err := a.IssuePreview("3000-sb.localhost:7878")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.IssuePreview("localhost:7878"); err == nil {
		t.Fatal("IssuePreview accepted a main host")
	}

	wrongHost := "3000-sb.localhost:7879"
	wrong := request(a, http.MethodGet, "http://"+wrongHost+"/__studio/preview-login?token="+token, wrongHost, nil, "", downstream())
	if wrong.Code != http.StatusForbidden || wrong.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("wrong-host ticket response = %d, headers=%v", wrong.Code, wrong.Header())
	}

	// A main session credential is never accepted as a preview credential.
	session := exchange(t, a, "localhost:7878", a.IssueLogin()).Result().Cookies()[0]
	previewReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/guest", nil)
	previewReq.Host = host
	previewReq.AddCookie(session)
	previewDenied := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(previewDenied, previewReq)
	if previewDenied.Code != http.StatusForbidden || !strings.Contains(previewDenied.Body.String(), "Open this preview from Sandbox Studio.") {
		t.Fatalf("session cookie reached preview: %d %q", previewDenied.Code, previewDenied.Body.String())
	}

	// Attacker-supplied redirect parameters are ignored; successful redemption
	// always lands at the preview root.
	target := "http://" + host + "/__studio/preview-login?token=" + token + "&redirect=https%3A%2F%2Fevil.example"
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	req.TLS = &tls.ConnectionState{}
	login := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(login, req)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/" || login.Header().Get("Cache-Control") != "no-store" || login.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("preview login response = %d headers=%v", login.Code, login.Header())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("preview login cookies = %#v", cookies)
	}
	previewCookie := cookies[0]
	if previewCookie.Name != PreviewCookie || previewCookie.Domain != "" || previewCookie.Path != "/" || !previewCookie.HttpOnly || !previewCookie.Secure || previewCookie.SameSite != http.SameSiteLaxMode || previewCookie.MaxAge != int(previewSessionLife.Seconds()) {
		t.Fatalf("preview cookie flags = %#v", previewCookie)
	}

	// The same one-time ticket cannot be redeemed again.
	again := request(a, http.MethodGet, "http://"+host+"/__studio/preview-login?token="+token, host, nil, "", downstream())
	if again.Code != http.StatusForbidden {
		t.Fatalf("preview ticket was reusable: %d", again.Code)
	}

	guestReq := httptest.NewRequest(http.MethodGet, "http://"+host+"/guest", nil)
	guestReq.Host = host
	guestReq.AddCookie(previewCookie)
	guest := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(guest, guestReq)
	if guest.Code != http.StatusNoContent || guest.Header().Get("X-Downstream") != "reached" {
		t.Fatalf("preview cookie did not reach guest: %d", guest.Code)
	}
	otherPreviewHost := "3000-sb.localhost:7879"
	otherPreviewReq := httptest.NewRequest(http.MethodGet, "http://"+otherPreviewHost+"/guest", nil)
	otherPreviewReq.Host = otherPreviewHost
	otherPreviewReq.AddCookie(previewCookie)
	otherPreview := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(otherPreview, otherPreviewReq)
	if otherPreview.Code != http.StatusForbidden {
		t.Fatalf("preview cookie accepted on another port: %d", otherPreview.Code)
	}

	// A preview cookie never authenticates the main Studio API.
	mainReq := httptest.NewRequest(http.MethodGet, "http://localhost:7878/api/auth/status", nil)
	mainReq.AddCookie(previewCookie)
	main := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(main, mainReq)
	if main.Code != http.StatusOK || strings.Contains(main.Body.String(), `"authenticated":true`) {
		t.Fatalf("preview cookie authenticated main session: %d %q", main.Code, main.Body.String())
	}
}

func TestPreviewAuthEndpointsAreGuestRoutesAfterPreviewAuth(t *testing.T) {
	a := testAuth(t)
	host := "3000-sb.localhost:7878"
	previewToken, err := a.IssuePreview(host)
	if err != nil {
		t.Fatal(err)
	}
	previewLogin := request(a, http.MethodGet, "http://"+host+"/__studio/preview-login?token="+previewToken, host, nil, "", downstream())
	previewCookie := previewLogin.Result().Cookies()[0]

	for _, path := range []string{"/api/auth/status", "/api/auth/exchange", "/api/auth/launch", "/api/auth/logout", "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		req.Host = host
		req.AddCookie(previewCookie)
		rec := httptest.NewRecorder()
		a.Middleware(downstream()).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent || rec.Header().Get("X-Downstream") != "reached" || len(rec.Result().Cookies()) != 0 {
			t.Errorf("preview endpoint %s response = %d headers=%v", path, rec.Code, rec.Header())
		}
	}

	// A preview API path without its preview cookie is denied before any Studio
	// auth endpoint can consume a login token or issue a session.
	loginToken := a.IssueLogin()
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	withoutCookie := request(a, http.MethodPost, "http://"+host+"/api/auth/exchange", host, headers, `{"token":"`+loginToken+`"}`, downstream())
	if withoutCookie.Code != http.StatusForbidden {
		t.Fatalf("preview exchange without preview cookie status = %d", withoutCookie.Code)
	}
	if rec := exchange(t, a, "localhost:7878", loginToken); rec.Code != http.StatusNoContent {
		t.Fatalf("preview exchange consumed a main login token: %d", rec.Code)
	}
}

func TestPreviewTicketExpiryAndWrongMethod(t *testing.T) {
	a := testAuth(t)
	host := "3000-sb.localhost:7878"
	token, err := a.IssuePreview(host)
	if err != nil {
		t.Fatal(err)
	}
	wrongMethod := request(a, http.MethodPost, "http://"+host+"/__studio/preview-login?token="+token, host, nil, "", downstream())
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("preview login POST status = %d", wrongMethod.Code)
	}
	a.now = func() time.Time { return time.Date(2026, 10, 9, 12, 1, 0, 0, time.UTC) }
	expired := request(a, http.MethodGet, "http://"+host+"/__studio/preview-login?token="+token, host, nil, "", downstream())
	if expired.Code != http.StatusForbidden {
		t.Fatalf("expired preview ticket status = %d", expired.Code)
	}
}

func TestMainHostSessionUsesExactLowercasedHostAndPreviewCookieDoesNotAuthenticate(t *testing.T) {
	a := testAuth(t)
	cookie := exchange(t, a, "127.0.0.1:7878", a.IssueLogin()).Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7878/api/health", nil)
	req.Host = "127.0.0.1:7878"
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("loopback address session status = %d", rec.Code)
	}

	previewCookie := &http.Cookie{Name: PreviewCookie, Value: cookie.Value}
	apiReq := httptest.NewRequest(http.MethodGet, "http://localhost:7878/api/health", nil)
	apiReq.AddCookie(previewCookie)
	api := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(api, apiReq)
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("preview cookie authenticated main API: %d", api.Code)
	}
}

func TestControlTokenIsNotAValidSessionCookie(t *testing.T) {
	a := testAuth(t)
	control := &http.Cookie{Name: SessionCookie, Value: ControlToken(testKey)}
	req := httptest.NewRequest(http.MethodGet, "http://localhost:7878/api/auth/status", nil)
	req.AddCookie(control)
	rec := httptest.NewRecorder()
	a.Middleware(downstream()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("control token authenticated session: %d %q", rec.Code, rec.Body.String())
	}
}

func TestLoginTokenCannotRedeemPreviewAndPreviewTokenCannotExchange(t *testing.T) {
	a := testAuth(t)
	host := "3000-sb.localhost:7878"
	login := a.IssueLogin()
	preview := request(a, http.MethodGet, "http://"+host+"/__studio/preview-login?token="+login, host, nil, "", downstream())
	if preview.Code != http.StatusForbidden {
		t.Fatalf("login token redeemed as preview: %d", preview.Code)
	}

	previewToken, err := a.IssuePreview(host)
	if err != nil {
		t.Fatal(err)
	}
	if rec := exchange(t, a, "localhost:7878", previewToken); rec.Code != http.StatusUnauthorized {
		t.Fatalf("preview token exchanged as login: %d", rec.Code)
	}
	if rec := exchange(t, a, "localhost:7878", login); rec.Code != http.StatusNoContent {
		t.Fatalf("wrong credential type consumed login token: %d", rec.Code)
	}
}

func TestPreviewHostPredicate(t *testing.T) {
	if !isTestPreview("3000-sb.LOCALHOST:7878") || isTestPreview("localhost:7878") || isTestPreview("evil.example") {
		t.Fatalf("unexpected test preview predicate")
	}
}
