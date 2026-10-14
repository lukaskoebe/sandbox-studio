package agentchan

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
)

// TestHubNamedPipe serves a guest over the named pipe msb routes vsock to on Windows.
func TestHubNamedPipe(t *testing.T) {
	path := paths.Paths{Data: t.TempDir()}.AgentSocket("sb1")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Listen("sb1", path); err != nil {
		t.Fatal(err)
	}
	defer h.Close("sb1")
	sess := fakeGuest(t, path, nil)
	defer sess.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.WaitConnected(ctx, "sb1"); err != nil {
		t.Fatal(err)
	}
	got, err := h.Sessions(ctx, "sb1")
	if err != nil || len(got) != 1 || got[0].Name != "main" {
		t.Fatalf("sessions %+v, %v", got, err)
	}
}
