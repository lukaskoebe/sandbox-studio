package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// testStudio runs the API behind the same guards as the server, over an in-memory catalog with
// one environment. It returns the address and the control credential of the installation.
func testStudio(t *testing.T) (addr, control string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateEnvironment(ctx, "Default"); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	auth, err := webauth.New(key, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	s := &api.Server{Store: st, Bus: &events.Bus{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(api.Guard(auth.Middleware(mux)))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), webauth.ControlToken(key)
}

func TestConnectSignsInAsTheOwner(t *testing.T) {
	addr, control := testStudio(t)
	ctx := context.Background()
	c, err := Connect(ctx, addr, control)
	if err != nil {
		t.Fatal(err)
	}
	envs, err := c.Environments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].Name != "Default" {
		t.Fatalf("environments: %+v", envs)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Closing ends the session: the client no longer sends a cookie.
	_, err = c.Environments(ctx)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("after Close: %v", err)
	}
}

func TestConnectRefusesAWrongCredential(t *testing.T) {
	addr, _ := testStudio(t)
	if _, err := Connect(context.Background(), addr, "not-the-control-credential"); err == nil {
		t.Fatal("wrong control credential accepted")
	}
}

func TestConnectWithoutStudioSaysSo(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, err = Connect(context.Background(), addr, "control")
	if !errors.Is(err, ErrNotRunning) || !strings.Contains(err.Error(), "studio") {
		t.Fatalf("no Studio at %s: %v", addr, err)
	}
}

func TestConnectRequiresLoopback(t *testing.T) {
	if _, err := Connect(context.Background(), "example.com:7878", "control"); err == nil {
		t.Fatal("remote address accepted")
	}
}
