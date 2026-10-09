//go:build linux

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestTransferWaitsForReceiptBeforeHalfClose(t *testing.T) {
	const size = uint64(73)
	hostConn, guestConn := newGuestSplitPipePair()
	defer hostConn.Close()
	defer guestConn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if err := hostConn.SetDeadline(deadline); err != nil {
		t.Fatalf("set host test deadline: %v", err)
	}
	if err := guestConn.SetDeadline(deadline); err != nil {
		t.Fatalf("set guest test deadline: %v", err)
	}
	closeWriteCalled := make(chan struct{}, 1)
	transferResult := make(chan error, 1)
	go func() {
		transferResult <- transferSized(guestConn, guestConn, func() error {
			closeWriteCalled <- struct{}{}
			return guestConn.CloseWrite()
		}, "raw", size)
	}()

	hostReader := bufio.NewReaderSize(hostConn, 4096)
	headerLine, err := hostReader.ReadSlice('\n')
	if err != nil {
		t.Fatalf("read guest handshake: %v", err)
	}
	var header handshake
	if err := json.Unmarshal(headerLine, &header); err != nil {
		t.Fatalf("decode guest handshake: %v", err)
	}
	if header.Mode != "raw" || header.Size != size {
		t.Fatalf("guest handshake = %+v", header)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(hostReader, payload); err != nil {
		t.Fatalf("read guest payload: %v", err)
	}
	expectedPayload := make([]byte, size)
	fillPattern(expectedPayload, 0)
	if !bytes.Equal(payload, expectedPayload) {
		t.Fatal("guest payload differs from the deterministic pattern")
	}
	digest := sha256.Sum256(payload)
	digestText := hex.EncodeToString(digest[:])
	select {
	case <-closeWriteCalled:
		t.Fatal("guest half-closed before receiving the receipt acknowledgement")
	default:
	}
	if err := writeJSONLine(hostConn, acknowledgement{Status: "received", Mode: "raw", Size: size, SHA256: digestText}); err != nil {
		t.Fatalf("write receipt acknowledgement: %v", err)
	}
	select {
	case <-closeWriteCalled:
	case <-time.After(time.Until(deadline)):
		t.Fatal("guest did not half-close after receiving the receipt acknowledgement")
	}
	var extra [1]byte
	n, err := hostReader.Read(extra[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read after announced payload = (%d, %v), want genuine EOF", n, err)
	}
	if err := writeJSONLine(hostConn, acknowledgement{Status: "ack", Mode: "raw", Size: size, SHA256: digestText}); err != nil {
		t.Fatalf("write final acknowledgement: %v", err)
	}
	if err := <-transferResult; err != nil {
		t.Fatalf("transferSized() error = %v", err)
	}
}

func TestMaxWriteConnCapsUnderlyingWritesAndPreservesBytes(t *testing.T) {
	payload := make([]byte, 97)
	fillPattern(payload, 0)
	underlying := &recordingNetConn{shortWrite: 3}
	conn := &maxWriteConn{Conn: underlying, limit: 11}
	h := sha256.New()

	sent, err := writeAndHash(conn, h, payload)
	digest := hex.EncodeToString(h.Sum(nil))
	if err != nil {
		t.Fatalf("writeAndHash() error = %v", err)
	}
	if sent != uint64(len(payload)) || !bytes.Equal(underlying.Bytes(), payload) {
		t.Fatalf("sent=%d underlying=%d bytes; payload=%d bytes", sent, underlying.Len(), len(payload))
	}
	wantHash := sha256.Sum256(payload)
	if digest != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("digest = %s, want %s", digest, hex.EncodeToString(wantHash[:]))
	}
	if len(underlying.writeSizes) < 2 {
		t.Fatalf("underlying writes = %v, want multiple capped writes", underlying.writeSizes)
	}
	for _, size := range underlying.writeSizes {
		if size > conn.limit {
			t.Fatalf("underlying Write size = %d, exceeds cap %d", size, conn.limit)
		}
	}
}

func TestMaxWriteConnPreservesPartialWriteError(t *testing.T) {
	payload := []byte("partial bytes before failure")
	wantErr := errors.New("simulated vsock write failure")
	underlying := &recordingNetConn{shortWrite: 4, writeErr: wantErr}
	conn := &maxWriteConn{Conn: underlying, limit: 8}
	h := sha256.New()

	sent, err := writeAndHash(conn, h, payload)
	digest := hex.EncodeToString(h.Sum(nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeAndHash() error = %v, want partial write error", err)
	}
	if sent != 4 || !bytes.Equal(underlying.Bytes(), payload[:4]) {
		t.Fatalf("sent=%d underlying=%q, want first 4 bytes %q", sent, underlying.Bytes(), payload[:4])
	}
	wantHash := sha256.Sum256(payload[:4])
	if digest != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("digest = %s, want partial digest %s", digest, hex.EncodeToString(wantHash[:]))
	}
	if len(underlying.writeSizes) != 1 || underlying.writeSizes[0] > conn.limit {
		t.Fatalf("underlying writes = %v, want one call no larger than %d", underlying.writeSizes, conn.limit)
	}
}

type recordingNetConn struct {
	bytes.Buffer
	writeSizes []int
	shortWrite int
	writeErr   error
}

func (c *recordingNetConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *recordingNetConn) Close() error                     { return nil }
func (c *recordingNetConn) LocalAddr() net.Addr              { return nil }
func (c *recordingNetConn) RemoteAddr() net.Addr             { return nil }
func (c *recordingNetConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingNetConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingNetConn) SetWriteDeadline(time.Time) error { return nil }

func (c *recordingNetConn) Write(p []byte) (int, error) {
	c.writeSizes = append(c.writeSizes, len(p))
	n := len(p)
	if c.shortWrite > 0 && n > c.shortWrite {
		n = c.shortWrite
	}
	_, _ = c.Buffer.Write(p[:n])
	if c.writeErr != nil {
		return n, c.writeErr
	}
	return n, nil
}

type guestSplitPipeConn struct {
	reader net.Conn
	writer net.Conn
}

func newGuestSplitPipePair() (*guestSplitPipeConn, *guestSplitPipeConn) {
	guestToHostWriter, guestToHostReader := net.Pipe()
	hostToGuestWriter, hostToGuestReader := net.Pipe()
	guest := &guestSplitPipeConn{reader: hostToGuestReader, writer: guestToHostWriter}
	host := &guestSplitPipeConn{reader: guestToHostReader, writer: hostToGuestWriter}
	return host, guest
}

func (c *guestSplitPipeConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *guestSplitPipeConn) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *guestSplitPipeConn) CloseWrite() error           { return c.writer.Close() }
func (c *guestSplitPipeConn) Close() error                { return errors.Join(c.reader.Close(), c.writer.Close()) }
func (c *guestSplitPipeConn) LocalAddr() net.Addr         { return c.reader.LocalAddr() }
func (c *guestSplitPipeConn) RemoteAddr() net.Addr        { return c.reader.RemoteAddr() }
func (c *guestSplitPipeConn) SetDeadline(deadline time.Time) error {
	return errors.Join(c.reader.SetDeadline(deadline), c.writer.SetDeadline(deadline))
}
func (c *guestSplitPipeConn) SetReadDeadline(deadline time.Time) error {
	return c.reader.SetReadDeadline(deadline)
}
func (c *guestSplitPipeConn) SetWriteDeadline(deadline time.Time) error {
	return c.writer.SetWriteDeadline(deadline)
}
