package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

func TestLoginURLCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SANDBOX_STUDIO_HOME", dir)
	st, err := store.Open(context.Background(), (paths.Paths{Data: dir}).DB())
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.Secret(context.Background(), "web-auth-key", 32)
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := webauth.New(key, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(auth.Middleware(http.NotFoundHandler()))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	link, err := requestLoginURL(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != addr || u.Path != "/" || u.RawQuery != "" {
		t.Fatalf("unexpected login URL: %s", link)
	}
	token := strings.TrimPrefix(u.Fragment, "studio-login=")
	if token == "" || token == u.Fragment {
		t.Fatal("missing fragment token")
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := http.Post(srv.URL+"/api/auth/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || len(resp.Cookies()) != 1 {
		t.Fatalf("exchange: HTTP %d, %d cookies", resp.StatusCode, len(resp.Cookies()))
	}
	resp, err = http.Post(srv.URL+"/api/auth/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket replay: %d", resp.StatusCode)
	}
}

func TestLoginURLRefusesRemoteAddress(t *testing.T) {
	if _, err := requestLoginURL(context.Background(), "example.com:7878"); err == nil {
		t.Fatal("remote address accepted")
	}
}
