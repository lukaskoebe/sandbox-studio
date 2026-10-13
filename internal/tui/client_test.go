package tui

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestConnectSignsInAsTheOwner(t *testing.T) {
	studio := startStudio(t)
	addr, control := studio.Addr, studio.Control
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
	addr := startStudio(t).Addr
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
