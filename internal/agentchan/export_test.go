package agentchan

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
)

func TestHubExportReceivesValidLayer(t *testing.T) {
	raw := exportTestTar(t, "layer payload")
	h, guest := newExportTestHub(t, func(st net.Conn, _ *bufio.Reader) {
		_, _ = agentproto.WriteExport(context.Background(), st, bytes.NewReader(raw), int64(len(raw)+1))
	})
	defer guest.Close()

	dir := t.TempDir()
	layer, err := h.Export(context.Background(), "export-test", dir, ocilayer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(layer.Path)
	if layer.Path == "" || layer.Size <= 0 || layer.UncompressedSize <= 0 || layer.Entries != 1 {
		t.Fatalf("unexpected layer metadata: %+v", layer)
	}
	compressed, err := os.Open(layer.Path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		compressed.Close()
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	header, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "payload.txt" || string(payload) != "layer payload" {
		t.Fatalf("received tar entry %q with payload %q", header.Name, payload)
	}
	_ = gz.Close()
	_ = compressed.Close()
}

func TestHubExportUnconnectedAndCanceledBeforeOpen(t *testing.T) {
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	dir := t.TempDir()
	if _, err := h.Export(context.Background(), "missing", dir, ocilayer.Limits{}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Export got %v, want ErrNotConnected", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Export(ctx, "missing", dir, ocilayer.Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Export got %v, want context cancellation", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Export created artifacts before opening: %v", entries)
	}
}

func TestHubExportReturnsRemoteErrorAndRemovesArtifacts(t *testing.T) {
	h, guest := newExportTestHub(t, func(st net.Conn, _ *bufio.Reader) {
		_, _ = agentproto.WriteExport(context.Background(), st, exportTestErrorReader{err: errors.New("capture failed")}, 16)
	})
	defer guest.Close()

	dir := t.TempDir()
	layer, err := h.Export(context.Background(), "export-test", dir, ocilayer.Limits{})
	if err == nil || !strings.Contains(err.Error(), "remote export error: capture failed") || layer.Path != "" {
		t.Fatalf("Export returned layer %+v and error %v, want remote error", layer, err)
	}
	assertExportDirEmpty(t, dir)
}

func TestHubExportCancellationClosesBlockedSourceAndRemovesArtifacts(t *testing.T) {
	blocked := make(chan struct{})
	peerDone := make(chan struct{})
	h, guest := newExportTestHub(t, func(st net.Conn, _ *bufio.Reader) {
		// Leave the data frame incomplete so Receive blocks while reading its
		// payload until caller cancellation closes the source stream.
		_, _ = st.Write([]byte{agentproto.ExportFrameData, 0, 0, 0, 12})
		close(blocked)
		var one [1]byte
		_, _ = st.Read(one[:])
		close(peerDone)
	})
	defer guest.Close()

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	type exportResult struct {
		layer templateexport.Layer
		err   error
	}
	result := make(chan exportResult, 1)
	go func() {
		layer, err := h.Export(ctx, "export-test", dir, ocilayer.Limits{})
		result <- exportResult{layer: layer, err: err}
	}()

	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("fake guest did not start the incomplete export frame")
	}
	cancel()
	select {
	case got := <-result:
		if !errors.Is(got.err, context.Canceled) || got.layer.Path != "" {
			t.Fatalf("Export returned layer path %q and error %v, want cancellation", got.layer.Path, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled export did not unblock the blocked source")
	}
	select {
	case <-peerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("host cancellation did not close the guest stream")
	}
	assertExportDirEmpty(t, dir)
}

func TestOpenExportStreamBoundsCanceledOpenerAndClosesLateStream(t *testing.T) {
	gate := make(chan struct{}, 1)
	openStarted := make(chan struct{})
	returnFromOpen := make(chan struct{})
	lateStreamClosed := make(chan struct{})
	closeStarted := make(chan struct{})
	allowCloseReturn := make(chan struct{})
	lateClient, latePeer := net.Pipe()
	defer latePeer.Close()
	lateStream := &delayedCloseConn{
		Conn:         lateClient,
		closeStarted: closeStarted,
		allowClose:   allowCloseReturn,
		closed:       lateStreamClosed,
	}

	ctx, cancel := context.WithCancel(context.Background())
	type openResult struct {
		st      net.Conn
		release func()
		err     error
	}
	first := make(chan openResult, 1)
	go func() {
		st, release, err := openExportStream(ctx, gate, func() (net.Conn, error) {
			close(openStarted)
			<-returnFromOpen
			return lateStream, nil
		})
		first <- openResult{st: st, release: release, err: err}
	}()
	select {
	case <-openStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("injected opener did not start")
	}
	cancel()
	select {
	case got := <-first:
		if !errors.Is(got.err, context.Canceled) || got.st != nil || got.release != nil {
			t.Fatalf("canceled opener returned stream %v, release %v, error %v", got.st, got.release != nil, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled caller remained blocked in yamux opener")
	}

	secondOpened := make(chan struct{}, 1)
	if _, _, err := openExportStream(context.Background(), gate, func() (net.Conn, error) {
		secondOpened <- struct{}{}
		return nil, errors.New("unexpected second open")
	}); !errors.Is(err, ErrExportInProgress) {
		t.Fatalf("second pending open got %v, want ErrExportInProgress", err)
	}
	select {
	case <-secondOpened:
		t.Fatal("second opener ran while the canceled opener was still pending")
	default:
	}

	close(returnFromOpen)
	select {
	case <-closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("late stream was not closed after opener returned")
	}
	closePendingOpener := make(chan struct{}, 1)
	if _, _, err := openExportStream(context.Background(), gate, func() (net.Conn, error) {
		closePendingOpener <- struct{}{}
		return nil, errors.New("unexpected open during late close")
	}); !errors.Is(err, ErrExportInProgress) {
		t.Fatalf("open during late stream close got %v, want ErrExportInProgress", err)
	}
	select {
	case <-closePendingOpener:
		t.Fatal("opener ran before the late stream close completed")
	default:
	}
	close(allowCloseReturn)
	select {
	case <-lateStreamClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("late stream close did not finish")
	}
	select {
	case gate <- struct{}{}:
		<-gate
	case <-time.After(2 * time.Second):
		t.Fatal("export opener gate was not released after closing the late stream")
	}

	activeClient, activePeer := net.Pipe()
	active, release, err := openExportStream(context.Background(), gate, func() (net.Conn, error) {
		return activeClient, nil
	})
	if err != nil || active == nil || release == nil {
		activePeer.Close()
		t.Fatalf("open after late close got stream %v, release %v, error %v", active, release != nil, err)
	}
	activeOpenerCalled := make(chan struct{}, 1)
	if _, _, err := openExportStream(context.Background(), gate, func() (net.Conn, error) {
		activeOpenerCalled <- struct{}{}
		return nil, errors.New("unexpected open during active export")
	}); !errors.Is(err, ErrExportInProgress) {
		t.Fatalf("open during active stream got %v, want ErrExportInProgress", err)
	}
	select {
	case <-activeOpenerCalled:
		t.Fatal("another opener ran while an export stream was active")
	default:
	}
	_ = active.Close()
	release()
	_ = activePeer.Close()
}

func newExportTestHub(t *testing.T, handleExport func(net.Conn, *bufio.Reader)) (*Hub, *yamux.Session) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "agent.sock")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Listen("export-test", socket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close("export-test") })

	nc, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := yamux.Client(nc, nil)
	if err != nil {
		nc.Close()
		t.Fatal(err)
	}
	hello, err := guest.Open()
	if err != nil {
		guest.Close()
		t.Fatal(err)
	}
	if err := agentproto.WriteJSONLine(hello, agentproto.Header{Kind: agentproto.KindHello}); err != nil {
		t.Fatal(err)
	}
	if err := agentproto.WriteJSONLine(hello, agentproto.Hello{Version: "export-test", Arch: "amd64"}); err != nil {
		t.Fatal(err)
	}
	_ = hello.Close()

	go func() {
		for {
			st, err := guest.Accept()
			if err != nil {
				return
			}
			go func(st net.Conn) {
				defer st.Close()
				br := bufio.NewReader(st)
				var hdr agentproto.Header
				if agentproto.ReadJSONLine(br, &hdr) != nil || hdr.Kind != agentproto.KindExport {
					return
				}
				handleExport(st, br)
			}(st)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.WaitConnected(ctx, "export-test"); err != nil {
		guest.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guest.Close() })
	return h, guest
}

func exportTestTar(t *testing.T, payload string) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	if err := tw.WriteHeader(&tar.Header{Name: "payload.txt", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func assertExportDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed export left artifacts: %v", entries)
	}
}

type exportTestErrorReader struct{ err error }

func (r exportTestErrorReader) Read([]byte) (int, error) { return 0, r.err }

type delayedCloseConn struct {
	net.Conn
	closeStarted chan struct{}
	allowClose   chan struct{}
	closed       chan struct{}
	closeOnce    sync.Once
}

func (c *delayedCloseConn) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.closeStarted)
		<-c.allowClose
		closeErr = c.Conn.Close()
		close(c.closed)
	})
	return closeErr
}
