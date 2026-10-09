package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/preview"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

func TestAuthenticatedPreviewFlow(t *testing.T) {
	apiHandler, s, env, sb := previewFixture(t)
	s.Auth = previewTestAuth(t, func(host string) bool {
		_, _, ok := preview.Parse(host)
		return ok
	})
	seen := make(chan string, 1)
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Cookie")
		http.SetCookie(w, &http.Cookie{Name: webauth.SessionCookie, Value: "guest-forgery", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "app", Value: "works", Path: "/"})
		w.Write([]byte("guest page"))
	}))
	defer guest.Close()
	dial := func(ctx context.Context, id string, port int) (net.Conn, error) {
		if id != sb.ID || port != 3000 {
			t.Errorf("unexpected target %s:%d", id, port)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(guest.URL, "http://"))
	}
	h := Guard(s.Auth.Middleware(preview.Route(dial, apiHandler)))
	token := s.Auth.IssueLogin()
	body, _ := json.Marshal(map[string]string{"token": token})
	request := func(method, target, body, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	main := "http://localhost:7878"
	if w := request("POST", main+"/api/auth/exchange", string(body), "http://evil.example"); w.Code != 403 {
		t.Fatalf("cross-origin login: %d", w.Code)
	}
	w := request("POST", main+"/api/auth/exchange", string(body), main)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login: %d", w.Code)
	}
	session := w.Result().Cookies()[0]
	open := main + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/previews/3000/open"
	if w = request("POST", open, "", main); w.Code != 401 {
		t.Fatalf("unauthenticated open: %d", w.Code)
	}
	w = request("POST", open, "", main, session)
	if w.Code != 200 {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
	var ticket previewOpenBody
	if err := json.Unmarshal(w.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(ticket.URL)
	if err != nil {
		t.Fatal(err)
	}
	root := "http://" + u.Host + "/"
	if w = request("GET", root, "", "", session); w.Code != 403 {
		t.Fatalf("Studio cookie accepted by preview: %d", w.Code)
	}
	w = request("GET", ticket.URL, "", "")
	if w.Code != 303 || w.Header().Get("Location") != "/" || len(w.Result().Cookies()) != 1 {
		t.Fatalf("preview exchange: %d", w.Code)
	}
	previewCookie := w.Result().Cookies()[0]
	if w = request("GET", ticket.URL, "", ""); w.Code != 403 {
		t.Fatalf("preview ticket replay: %d", w.Code)
	}
	w = request("GET", root, "", "", session, previewCookie, &http.Cookie{Name: "app", Value: "hello"})
	if w.Code != 200 || w.Body.String() != "guest page" {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	if got := <-seen; got != "app=hello" {
		t.Fatalf("guest received cookies: %q", got)
	}
	if cookies := w.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "app" {
		t.Fatalf("guest auth cookie escaped filtering: %v", cookies)
	}
	if w = request("GET", main+"/api/environments", "", "", previewCookie); w.Code != 401 {
		t.Fatalf("preview cookie accepted by API: %d", w.Code)
	}
}
