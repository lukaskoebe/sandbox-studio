package agentchan

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Run sends the command to the guest and returns its output and exit code.
func TestHubRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Listen("sb1", path); err != nil {
		t.Fatal(err)
	}
	defer h.Close("sb1")
	fakeGuest(t, path, make(chan agentproto.Config, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, err := h.Run(ctx, "sb1", agentproto.RunRequest{Args: []string{"claude", "-p"}, Env: map[string]string{"TOKEN": "studio-x"}, TimeoutSec: 5})
		if err == ErrNotConnected && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if out.Stdout != "claude -p studio-x" || out.ExitCode != 3 {
			t.Fatalf("reply %+v", out)
		}
		return
	}
}
