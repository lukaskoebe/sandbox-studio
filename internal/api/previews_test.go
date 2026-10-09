package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

func previewFixture(t *testing.T) (http.Handler, *Server, store.Environment, store.Sandbox) {
	t.Helper()
	h, s := newTestServer(t)
	ctx := context.Background()
	env, err := s.Store.CreateEnvironment(ctx, "preview-test")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	return h, s, env, sb
}

func previewTestAuth(t *testing.T, isPreview func(string) bool) *webauth.Auth {
	t.Helper()
	auth, err := webauth.New([]byte("0123456789abcdef0123456789abcdef"), isPreview)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestOpenPreviewTicket(t *testing.T) {
	h, s, env, sb := previewFixture(t)
	var issuedHost string
	wantHost := "3000-" + sb.ID + ".localhost:7878"
	s.Auth = previewTestAuth(t, func(host string) bool {
		issuedHost = host
		return host == wantHost
	})

	path := "/api/environments/" + url.PathEscape(env.ID) + "/sandboxes/" + url.PathEscape(sb.ID) + "/previews/3000/open"
	rec := do(h, http.MethodPost, "http://localhost:7878"+path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("open preview: got %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body)
	}
	var body previewOpenBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body)
	}
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("parse preview URL %q: %v", body.URL, err)
	}
	if u.Scheme != "http" || u.Host != wantHost || u.Path != "/__studio/preview-login" {
		t.Fatalf("preview URL: got %q, want http://%s/__studio/preview-login?token=...", body.URL, wantHost)
	}
	if u.Query().Get("token") == "" || issuedHost != wantHost {
		t.Fatalf("ticket: token present=%v, issued host=%q, want %q", u.Query().Get("token") != "", issuedHost, wantHost)
	}
}

func TestOpenPreviewValidatesSandboxEnvironment(t *testing.T) {
	h, s, _, sb := previewFixture(t)
	other, err := s.Store.CreateEnvironment(context.Background(), "other-preview-test")
	if err != nil {
		t.Fatal(err)
	}
	s.Auth = previewTestAuth(t, func(string) bool { return true })

	path := "/api/environments/" + url.PathEscape(other.ID) + "/sandboxes/" + url.PathEscape(sb.ID) + "/previews/3000/open"
	rec := do(h, http.MethodPost, "http://localhost:7878"+path, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-environment sandbox: got %d, want %d (%s)", rec.Code, http.StatusNotFound, rec.Body)
	}
}

func TestOpenPreviewRejectsInvalidPort(t *testing.T) {
	h, s, env, sb := previewFixture(t)
	s.Auth = previewTestAuth(t, func(string) bool { return true })

	for _, port := range []string{"0", "65536"} {
		path := "/api/environments/" + url.PathEscape(env.ID) + "/sandboxes/" + url.PathEscape(sb.ID) + "/previews/" + port + "/open"
		rec := do(h, http.MethodPost, "http://localhost:7878"+path, "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("port %s: got %d, want %d (%s)", port, rec.Code, http.StatusUnprocessableEntity, rec.Body)
		}
	}
}

func TestOpenPreviewWithoutAuthIsUnavailable(t *testing.T) {
	h, _, env, sb := previewFixture(t)
	path := "/api/environments/" + url.PathEscape(env.ID) + "/sandboxes/" + url.PathEscape(sb.ID) + "/previews/3000/open"
	rec := do(h, http.MethodPost, "http://localhost:7878"+path, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("open preview without auth: got %d, want %d (%s)", rec.Code, http.StatusServiceUnavailable, rec.Body)
	}
}
