package templateexport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
)

func TestReceiveValidTarGzipAndDigests(t *testing.T) {
	input := makeTestTar(t)
	wire := makeExportWire(t, input)
	dir := t.TempDir()

	layer, err := Receive(context.Background(), dir, io.NopCloser(bytes.NewReader(wire)), ocilayer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(layer.Path)
	checkLayer(t, layer, "payload.txt", "layer payload")

	second, err := Receive(context.Background(), dir, io.NopCloser(bytes.NewReader(wire)), ocilayer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(second.Path)
	if second.Digest != layer.Digest || second.DiffID != layer.DiffID || second.Size != layer.Size {
		t.Fatalf("gzip output is not deterministic: first=%+v second=%+v", layer, second)
	}
}

func TestReceiveBadTarRemovesArtifact(t *testing.T) {
	dir := t.TempDir()
	layer, err := Receive(context.Background(), dir, io.NopCloser(bytes.NewReader(makeExportWire(t, []byte("not a tar")))), ocilayer.Limits{})
	if err == nil || layer != (Layer{}) {
		t.Fatalf("got layer %+v and error %v, want failure with zero layer", layer, err)
	}
	assertNoArtifacts(t, dir)
}

func TestReceiveCorruptRawDigestAfterValidTarRemovesArtifact(t *testing.T) {
	dir := t.TempDir()
	wire := makeExportWire(t, makeTestTar(t))
	wire = corruptExportDigest(t, wire)
	layer, err := Receive(context.Background(), dir, io.NopCloser(bytes.NewReader(wire)), ocilayer.Limits{})
	if err == nil || layer != (Layer{}) {
		t.Fatalf("got layer %+v and error %v, want raw digest failure", layer, err)
	}
	if !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("got %v, want export digest error", err)
	}
	assertNoArtifacts(t, dir)
}

func TestReceiveTruncatedWireRemovesArtifact(t *testing.T) {
	dir := t.TempDir()
	wire := makeExportWire(t, makeTestTar(t))
	wire = wire[:len(wire)-1]
	layer, err := Receive(context.Background(), dir, io.NopCloser(bytes.NewReader(wire)), ocilayer.Limits{})
	if err == nil || layer != (Layer{}) {
		t.Fatalf("got layer %+v and error %v, want truncated stream failure", layer, err)
	}
	assertNoArtifacts(t, dir)
}

func TestReceiveSourceCloseErrorRemovesArtifact(t *testing.T) {
	dir := t.TempDir()
	closeErr := errors.New("source close failed")
	source := &closeErrorSource{
		Reader:   bytes.NewReader(makeExportWire(t, makeTestTar(t))),
		closeErr: closeErr,
	}
	layer, err := Receive(context.Background(), dir, source, ocilayer.Limits{})
	if !errors.Is(err, closeErr) || layer != (Layer{}) {
		t.Fatalf("got layer %+v and error %v, want source close failure", layer, err)
	}
	if source.closeCalls != 1 {
		t.Fatalf("source closed %d times, want once", source.closeCalls)
	}
	assertNoArtifacts(t, dir)
}

func TestReceiveCancellationClosesBlockedSource(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, server := net.Pipe()
	defer server.Close()
	source := &observedConn{Conn: client, readStarted: make(chan struct{})}
	type result struct {
		layer Layer
		err   error
	}
	done := make(chan result, 1)
	go func() {
		layer, err := Receive(ctx, dir, source, ocilayer.Limits{})
		done <- result{layer: layer, err: err}
	}()
	select {
	case <-source.readStarted:
	case <-time.After(2 * time.Second):
		cancel()
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("Receive did not begin reading the blocked source")
	}
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.layer != (Layer{}) {
			t.Fatalf("got layer %+v and error %v, want cancellation", got.layer, got.err)
		}
	case <-time.After(2 * time.Second):
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("cancellation did not close and unblock the source")
	}
	assertNoArtifacts(t, dir)
}

func TestReceiveEarlyInvalidTarClosesPausedSource(t *testing.T) {
	dir := t.TempDir()
	sourceReader, sourceWriter := io.Pipe()
	type result struct {
		layer Layer
		err   error
	}
	done := make(chan result, 1)
	go func() {
		layer, err := Receive(context.Background(), dir, sourceReader, ocilayer.Limits{})
		done <- result{layer: layer, err: err}
	}()

	frameSent := make(chan struct{})
	resumeProducer := make(chan struct{})
	producerDone := make(chan error, 1)
	badHeader := make([]byte, 512)
	copy(badHeader, []byte("not a tar"))
	go func() {
		err := agentproto.WriteFrame(sourceWriter, agentproto.ExportFrameData, badHeader)
		close(frameSent)
		if err == nil {
			<-resumeProducer
			err = agentproto.WriteFrame(sourceWriter, agentproto.ExportFrameData, []byte("more data"))
		}
		producerDone <- err
	}()
	select {
	case <-frameSent:
	case <-time.After(2 * time.Second):
		_ = sourceReader.Close()
		close(resumeProducer)
		<-producerDone
		<-done
		t.Fatal("paused source did not deliver its first frame")
	}
	select {
	case got := <-done:
		if got.err == nil || got.layer != (Layer{}) {
			t.Fatalf("got layer %+v and error %v, want early tar rejection", got.layer, got.err)
		}
	case <-time.After(2 * time.Second):
		_ = sourceReader.Close()
		close(resumeProducer)
		<-done
		<-producerDone
		t.Fatal("early tar rejection did not close the paused source")
	}
	close(resumeProducer)
	if err := <-producerDone; !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("paused producer got %v, want closed pipe", err)
	}
	_ = sourceWriter.Close()
	assertNoArtifacts(t, dir)
}

func checkLayer(t *testing.T, layer Layer, expectedName, expectedPayload string) {
	t.Helper()
	compressed, err := os.ReadFile(layer.Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(layer.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode is %04o, want 0600", info.Mode().Perm())
	}
	if int64(len(compressed)) != layer.Size {
		t.Fatalf("compressed size is %d, layer reports %d", len(compressed), layer.Size)
	}
	wantDigest := sha256.Sum256(compressed)
	if layer.Digest != "sha256:"+hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("compressed digest is %q", layer.Digest)
	}
	if len(compressed) < 10 || !bytes.Equal(compressed[4:8], []byte{0, 0, 0, 0}) || compressed[9] != 255 {
		t.Fatalf("gzip header is not deterministic: %x", compressed[:min(len(compressed), 10)])
	}

	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	normalized, readErr := io.ReadAll(gz)
	closeErr := gz.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read gzip layer: read=%v close=%v", readErr, closeErr)
	}
	if int64(len(normalized)) != layer.UncompressedSize {
		t.Fatalf("normalized size is %d, layer reports %d", len(normalized), layer.UncompressedSize)
	}
	wantDiffID := sha256.Sum256(normalized)
	if layer.DiffID != "sha256:"+hex.EncodeToString(wantDiffID[:]) {
		t.Fatalf("uncompressed diff ID is %q", layer.DiffID)
	}

	tarReader := tar.NewReader(bytes.NewReader(normalized))
	header, err := tarReader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != expectedName {
		t.Fatalf("tar entry name is %q", header.Name)
	}
	payload, err := io.ReadAll(tarReader)
	if err != nil || string(payload) != expectedPayload {
		t.Fatalf("tar entry payload is %q, error %v", payload, err)
	}
	if _, err := tarReader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("tar has unexpected extra entry: %v", err)
	}
	if layer.Entries != 1 {
		t.Fatalf("layer has %d entries, want 1", layer.Entries)
	}
}

func makeTestTar(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	if err := writer.WriteHeader(&tar.Header{
		Name:     "payload.txt",
		Mode:     0o644,
		Size:     int64(len("layer payload")),
		ModTime:  time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatUSTAR,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "layer payload"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func makeExportWire(t *testing.T, raw []byte) []byte {
	t.Helper()
	var wire bytes.Buffer
	if _, err := agentproto.WriteExport(context.Background(), &wire, bytes.NewReader(raw), int64(len(raw)+1)); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

func corruptExportDigest(t *testing.T, wire []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	reader := bytes.NewReader(wire)
	found := false
	for {
		typ, payload, err := agentproto.ReadFrame(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if typ == agentproto.ExportFrameComplete {
			var completion struct {
				Version int    `json:"version"`
				Bytes   int64  `json:"bytes"`
				SHA256  string `json:"sha256"`
			}
			if err := json.Unmarshal(payload, &completion); err != nil {
				t.Fatal(err)
			}
			if completion.SHA256[0] == '0' {
				completion.SHA256 = "1" + completion.SHA256[1:]
			} else {
				completion.SHA256 = "0" + completion.SHA256[1:]
			}
			payload, err = json.Marshal(completion)
			if err != nil {
				t.Fatal(err)
			}
			found = true
		}
		if err := agentproto.WriteFrame(&output, typ, payload); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("export has no completion frame")
	}
	return output.Bytes()
}

func assertNoArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failure left artifacts in directory: %v", entries)
	}
}

type observedConn struct {
	net.Conn
	readStarted chan struct{}
	readOnce    sync.Once
}

type closeErrorSource struct {
	io.Reader
	closeErr   error
	closeCalls int
}

func (s *closeErrorSource) Close() error {
	s.closeCalls++
	return s.closeErr
}

func (c *observedConn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() { close(c.readStarted) })
	return c.Conn.Read(p)
}
