package agentchan

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestHubRejectsDuplicateWhileSessionLiveAndAllowsReconnect(t *testing.T) {
	dir, err := os.MkdirTemp("", "hub-duplicate")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const id = "sb-duplicate"
	path := filepath.Join(dir, "s.sock")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	connected := make(chan string, 2)
	h.OnConnect = func(id string) { connected <- id }
	if err := h.Listen(id, path); err != nil {
		t.Fatal(err)
	}
	defer h.Close(id)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	configs := make(chan agentproto.Config, 1)
	first := fakeGuest(t, path, configs)
	defer first.Close()
	if err := h.WaitConnected(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-connected:
		if got != id {
			t.Fatalf("OnConnect(%q), want %q", got, id)
		}
	case <-ctx.Done():
		t.Fatal("OnConnect was not called for the first guest")
	}

	pty, err := h.OpenPTY(ctx, id, "main", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	readEcho := func(want string) {
		t.Helper()
		if _, err := pty.Write([]byte(want)); err != nil {
			t.Fatalf("write to active PTY: %v", err)
		}
		buf := make([]byte, 32)
		n, err := pty.Read(buf)
		if err != nil || string(buf[:n]) != strings.ToUpper(want) {
			t.Fatalf("active PTY read %q, %v; want %q", buf[:n], err, strings.ToUpper(want))
		}
	}
	readEcho("first")

	duplicate := fakeGuest(t, path, configs)
	defer duplicate.Close()
	if err := waitForClosedAgentSession(ctx, duplicate); err != nil {
		t.Fatalf("duplicate guest was not rejected: %v", err)
	}
	if first.IsClosed() {
		t.Fatal("duplicate guest closed the established session")
	}
	if _, ok := h.Connected(id); !ok {
		t.Fatal("established guest was removed from the hub")
	}
	readEcho("still-active")
	select {
	case got := <-connected:
		t.Fatalf("duplicate guest triggered OnConnect(%q)", got)
	default:
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitForHubDisconnected(ctx, h, id); err != nil {
		t.Fatal(err)
	}

	next := fakeGuest(t, path, configs)
	defer next.Close()
	if err := h.WaitConnected(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-connected:
		if got != id {
			t.Fatalf("OnConnect(%q), want %q after reconnect", got, id)
		}
	case <-ctx.Done():
		t.Fatal("OnConnect was not called for the reconnect")
	}
	if next.IsClosed() {
		t.Fatal("next guest was closed after the established session ended")
	}
}

func waitForClosedAgentSession(ctx context.Context, sess interface{ IsClosed() bool }) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if sess.IsClosed() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForHubDisconnected(ctx context.Context, h *Hub, id string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, ok := h.Connected(id); !ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
