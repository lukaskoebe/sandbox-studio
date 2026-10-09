package agentchan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
)

const exportCancelTestTimeout = 5 * time.Second

func TestHubExportCancellationInterruptsUncooperativePeerAndKeepsSession(t *testing.T) {
	validTar := exportTestTar(t, "after cancellation")
	frameWritten := make(chan struct{})
	releasePeer := make(chan struct{})
	peerDone := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releasePeer) }) }
	var requests atomic.Int32

	h, _ := newExportTestHub(t, func(st net.Conn, _ *bufio.Reader) {
		switch requests.Add(1) {
		case 1:
			defer close(peerDone)
			header := []byte{agentproto.ExportFrameData, 0, 0, 0, 12}
			if _, err := io.Copy(st, bytes.NewReader(header)); err != nil {
				return
			}
			close(frameWritten)
			<-releasePeer // Deliberately neither read nor close after the partial frame.
		case 2:
			_, _ = agentproto.WriteExport(context.Background(), st, bytes.NewReader(validTar), int64(len(validTar)+1))
		}
	})
	t.Cleanup(release)

	dir := t.TempDir()
	// No caller deadline: without a read-interrupting Close, this would wait
	// for Hub.Export's much longer operation deadline instead of cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type exportResult struct {
		layer templateexport.Layer
		err   error
	}
	result, done := make(chan exportResult, 1), make(chan struct{})
	go func() {
		layer, err := h.Export(ctx, "export-test", dir, ocilayer.Limits{})
		result <- exportResult{layer: layer, err: err}
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-done:
		case <-time.After(exportCancelTestTimeout):
			t.Errorf("canceled export did not stop during cleanup")
		}
	})
	select {
	case <-frameWritten:
	case <-time.After(exportCancelTestTimeout):
		t.Fatal("fake guest did not write the incomplete frame header")
	}
	cancel()
	select {
	case got := <-result:
		if !errors.Is(got.err, context.Canceled) || got.layer.Path != "" {
			t.Fatalf("canceled Export returned layer %q and error %v", got.layer.Path, got.err)
		}
	case <-time.After(exportCancelTestTimeout):
		t.Fatal("canceled export did not interrupt the stalled source read")
	}
	assertExportDirEmpty(t, dir)
	if _, ok := h.Connected("export-test"); !ok {
		t.Fatal("canceling the export dropped the guest session")
	}
	select {
	case <-peerDone:
		t.Fatal("fake peer stopped before the test released it")
	default:
	}

	nextCtx, nextCancel := context.WithTimeout(context.Background(), exportCancelTestTimeout)
	defer nextCancel()
	layer, err := h.Export(nextCtx, "export-test", dir, ocilayer.Limits{})
	if err != nil {
		t.Fatalf("valid export after cancellation: %v", err)
	}
	if layer.Entries != 1 {
		t.Fatalf("valid export after cancellation has %d entries, want 1", layer.Entries)
	}
	if err := os.Remove(layer.Path); err != nil {
		t.Fatalf("remove subsequent export artifact: %v", err)
	}
	release()
	select {
	case <-peerDone:
	case <-time.After(exportCancelTestTimeout):
		t.Fatal("fake peer did not stop after release")
	}
}

func TestHubExportNormalizeAbortInterruptsUncooperativePeer(t *testing.T) {
	badTarHeader := make([]byte, 512)
	copy(badTarHeader, []byte("not a tar"))
	frameWritten := make(chan struct{})
	releasePeer := make(chan struct{})
	peerDone := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releasePeer) }) }
	h, _ := newExportTestHub(t, func(st net.Conn, _ *bufio.Reader) {
		defer close(peerDone)
		if err := agentproto.WriteFrame(st, agentproto.ExportFrameData, badTarHeader); err != nil {
			return
		}
		close(frameWritten)
		<-releasePeer // Keep the wire producer open while Normalize rejects the archive.
	})
	t.Cleanup(release)

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type exportResult struct {
		layer templateexport.Layer
		err   error
	}
	result, done := make(chan exportResult, 1), make(chan struct{})
	go func() {
		layer, err := h.Export(ctx, "export-test", dir, ocilayer.Limits{})
		result <- exportResult{layer: layer, err: err}
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case <-done:
		case <-time.After(exportCancelTestTimeout):
			t.Errorf("normalization-aborted export did not stop during cleanup")
		}
	})
	select {
	case <-frameWritten:
	case <-time.After(exportCancelTestTimeout):
		t.Fatal("fake guest did not send the invalid tar header")
	}
	select {
	case got := <-result:
		if got.err == nil || got.layer.Path != "" {
			t.Fatalf("Normalize rejection returned layer %q and error %v", got.layer.Path, got.err)
		}
	case <-time.After(exportCancelTestTimeout):
		t.Fatal("Normalize rejection waited for the stalled wire producer")
	}
	assertExportDirEmpty(t, dir)
	select {
	case <-peerDone:
		t.Fatal("fake peer stopped before the test released it")
	default:
	}
	release()
	select {
	case <-peerDone:
	case <-time.After(exportCancelTestTimeout):
		t.Fatal("fake peer did not stop after release")
	}
}
