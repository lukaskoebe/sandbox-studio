package agentchan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestHubBrowserDownloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Listen("vm1", path); err != nil {
		t.Fatal(err)
	}
	defer h.Close("vm1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	g := fakeGuest(t, path, make(chan agentproto.Config, 1))
	defer g.Close()
	if err := h.WaitConnected(ctx, "vm1"); err != nil {
		t.Fatal(err)
	}

	list, err := h.BrowserDownloads(ctx, "vm1", agentproto.BrowserDownloadsRequest{})
	if err != nil || len(list.Files) != 1 || list.Files[0].Name != "a.pdf" {
		t.Fatalf("listing %+v, %v", list, err)
	}
	var got string
	err = h.BrowserDownload(ctx, "vm1", agentproto.BrowserDownloadsRequest{Name: "b.pdf"}, func(d agentproto.BrowserDownload, body io.Reader) error {
		b, err := io.ReadAll(body)
		got = d.Name + ":" + string(b)
		return err
	})
	if err != nil || got != "b.pdf:abc" {
		t.Fatalf("download %q, %v", got, err)
	}
	err = h.BrowserDownload(ctx, "vm1", agentproto.BrowserDownloadsRequest{Name: "gone"}, func(agentproto.BrowserDownload, io.Reader) error {
		t.Fatal("the callback ran for a refused download")
		return nil
	})
	if err == nil || err.Error() != "no such download" {
		t.Fatalf("refused download: %v", err)
	}
	sentinel := errors.New("the sandbox refused")
	if err := h.BrowserDownload(ctx, "vm1", agentproto.BrowserDownloadsRequest{Name: "b.pdf"}, func(agentproto.BrowserDownload, io.Reader) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error: %v", err)
	}
	if err := h.BrowserDownload(ctx, "nope", agentproto.BrowserDownloadsRequest{Name: "b.pdf"}, nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("unknown VM: %v", err)
	}
}
