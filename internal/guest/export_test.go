//go:build linux

package guest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestServeExportUsesInjectedFramedExporter(t *testing.T) {
	want := []byte("fake guest layer")
	a := newFakeExportAgent(func(ctx context.Context, dst io.Writer) error {
		_, err := agentproto.WriteExport(ctx, dst, bytes.NewReader(want), int64(len(want)+1))
		return err
	})
	host, guest := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.handle(guest)
	}()
	if err := agentproto.WriteJSONLine(host, agentproto.Header{Kind: agentproto.KindExport}); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := agentproto.ReadExport(context.Background(), &got, host, 1024); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("exported bytes %q, want %q", got.Bytes(), want)
	}
	_ = host.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("guest export handler did not finish")
	}
}

func TestServeExportFramesPreflightErrorWithoutJSON(t *testing.T) {
	a := newFakeExportAgent(func(context.Context, io.Writer) error {
		return errors.New("capture discovery failed")
	})
	host, guest := net.Pipe()
	go a.handle(guest)
	if err := agentproto.WriteJSONLine(host, agentproto.Header{Kind: agentproto.KindExport}); err != nil {
		t.Fatal(err)
	}
	_, err := agentproto.ReadExport(context.Background(), io.Discard, host, 1024)
	if err == nil || !strings.Contains(err.Error(), "remote export error: capture discovery failed") || strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("export got %v, want a framed preflight error", err)
	}
	_ = host.Close()
}

func TestServeExportCancellationFollowsHostClose(t *testing.T) {
	called := make(chan context.Context, 1)
	a := newFakeExportAgent(func(ctx context.Context, _ io.Writer) error {
		called <- ctx
		<-ctx.Done()
		return ctx.Err()
	})
	host, guest := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.handle(guest)
	}()
	if err := agentproto.WriteJSONLine(host, agentproto.Header{Kind: agentproto.KindExport}); err != nil {
		t.Fatal(err)
	}
	var exportCtx context.Context
	select {
	case exportCtx = <-called:
	case <-time.After(2 * time.Second):
		_ = host.Close()
		t.Fatal("injected exporter was not called")
	}
	_ = host.Close()
	select {
	case <-exportCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("host stream close did not cancel export context")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("guest export handler did not stop after host close")
	}
}

func TestServeExportRefusesParallelExportWithFramedError(t *testing.T) {
	called := make(chan context.Context, 1)
	a := newFakeExportAgent(func(ctx context.Context, _ io.Writer) error {
		called <- ctx
		<-ctx.Done()
		return ctx.Err()
	})
	firstHost, firstGuest := net.Pipe()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		a.handle(firstGuest)
	}()
	if err := agentproto.WriteJSONLine(firstHost, agentproto.Header{Kind: agentproto.KindExport}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		_ = firstHost.Close()
		t.Fatal("first export did not start")
	}

	secondHost, secondGuest := net.Pipe()
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		a.handle(secondGuest)
	}()
	if err := agentproto.WriteJSONLine(secondHost, agentproto.Header{Kind: agentproto.KindExport}); err != nil {
		t.Fatal(err)
	}
	_, err := agentproto.ReadExport(context.Background(), io.Discard, secondHost, 1024)
	if err == nil || !strings.Contains(err.Error(), "another export is already running") || strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("second export got %v, want bounded framed refusal", err)
	}
	_ = secondHost.Close()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("refused export handler did not finish")
	}

	_ = firstHost.Close()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first export handler did not stop")
	}
}

func newFakeExportAgent(exporter func(context.Context, io.Writer) error) *Agent {
	return &Agent{
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		export: exporter,
	}
}
