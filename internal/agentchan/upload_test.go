package agentchan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestHubUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Listen("sb1", path); err != nil {
		t.Fatal(err)
	}
	defer h.Close("sb1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	g := fakeGuest(t, path, make(chan agentproto.Config, 1))
	defer g.Close()
	if err := h.WaitConnected(ctx, "sb1"); err != nil {
		t.Fatal(err)
	}

	// A body larger than the stream's flow-control window arrives whole.
	body := strings.Repeat("ab", 1<<20)
	reply, err := h.Upload(ctx, "sb1", agentproto.UploadRequest{Name: "big.bin", Size: int64(len(body))}, strings.NewReader(body))
	if err != nil || reply != (agentproto.UploadReply{Name: "big.bin", Size: int64(len(body))}) {
		t.Fatalf("reply %+v, %v", reply, err)
	}

	var taken NameTakenError
	if _, err := h.Upload(ctx, "sb1", agentproto.UploadRequest{Name: "taken", Size: 1}, strings.NewReader("x")); !errors.As(err, &taken) || taken != "taken already exists" {
		t.Fatalf("taken name: %v", err)
	}
	if _, err := h.Upload(ctx, "sb1", agentproto.UploadRequest{Name: "short", Size: 10}, strings.NewReader("abc")); err == nil {
		t.Fatal("a short body was sent as a whole file")
	}
	if _, err := h.Upload(ctx, "sb1", agentproto.UploadRequest{Name: "big", Size: agentproto.MaxUploadBytes + 1}, strings.NewReader("")); err == nil {
		t.Fatal("an oversized file was accepted")
	}
	// A request line the guest could not read is refused before anything is sent.
	if _, err := h.Upload(ctx, "sb1", agentproto.UploadRequest{Dir: strings.Repeat("d/", agentproto.MaxUploadDir), Name: "a", Size: 1}, strings.NewReader("x")); err == nil {
		t.Fatal("a folder path over the limit was sent")
	}
	if _, err := h.Upload(ctx, "nope", agentproto.UploadRequest{Name: "a", Size: 1}, strings.NewReader("x")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("unknown sandbox: %v", err)
	}
}
