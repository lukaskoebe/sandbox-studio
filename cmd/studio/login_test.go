package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

// A second start while the first Studio is still starting waits for it to answer, as long as
// it holds the data directory; once it doesn't, the second start gives up at once.
func TestSecondStartWaitsForAStartingStudio(t *testing.T) {
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
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	log := slog.New(slog.DiscardHandler)

	start := time.Now()
	if err := openRunning(context.Background(), addr, dir, false, "", log, func() bool { return false }); err == nil || time.Since(start) > time.Second {
		t.Fatalf("with the data no longer held: %v after %s", err, time.Since(start))
	}

	go func() {
		time.Sleep(1500 * time.Millisecond)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		srv := &http.Server{Handler: auth.Middleware(http.NotFoundHandler())}
		t.Cleanup(func() { srv.Close() })
		_ = srv.Serve(l)
	}()
	start = time.Now()
	if err := openRunning(context.Background(), addr, dir, false, "", log, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < time.Second {
		t.Errorf("answered after %s, before the Studio listened", time.Since(start))
	}
}
