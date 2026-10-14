package agentchan

import (
	"context"
	"io"
	"log/slog"
	"strconv"
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

// TestHubCloseWhileClientsHangUp closes the hub while clients connect and hang up, which can
// make go-winio's listener lose the close.
func TestHubCloseWhileClientsHangUp(t *testing.T) {
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	data := t.TempDir()
	for i := range 50 {
		id := "sb" + strconv.Itoa(i)
		path := paths.Paths{Data: data}.AgentSocket(id)
		if err := h.Listen(id, path); err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		churned := make(chan struct{})
		go func() {
			defer close(churned)
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				if c, err := Dial(ctx, path); err == nil {
					c.Close()
				}
				cancel()
			}
		}()
		time.Sleep(20 * time.Millisecond)
		closed := make(chan struct{})
		go func() { h.Close(id); close(closed) }()
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: Close hangs", i)
		}
		close(stop)
		<-churned
	}
}
