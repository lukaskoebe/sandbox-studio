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
	"strings"
	"testing"
	"time"

	studioruntime "github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

func TestReceiveTransferSizedWaitsForEOFAfterReceiptAck(t *testing.T) {
	payload := deterministicTestPayload(4099)
	expectedHash := testSHA256(payload)
	hostConn, guestConn := newSplitPipePair()
	defer hostConn.Close()
	defer guestConn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if err := hostConn.SetDeadline(deadline); err != nil {
		t.Fatalf("set host test deadline: %v", err)
	}
	if err := guestConn.SetDeadline(deadline); err != nil {
		t.Fatalf("set guest test deadline: %v", err)
	}
	type outcome struct {
		stats transferStats
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		stats, err := receiveTransferSized(bufio.NewReaderSize(hostConn, 4096), hostConn, "raw", uint64(len(payload)), expectedHash)
		result <- outcome{stats: stats, err: err}
	}()

	if err := writeJSONLine(guestConn, handshake{Mode: "raw", Size: uint64(len(payload))}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	if err := writeTestBytes(guestConn, payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	guestAcks := bufio.NewReaderSize(guestConn, maxHandshakeBytes)
	receivedAck := decodeTestAck(t, guestAcks)
	if receivedAck.Status != "received" || receivedAck.Size != uint64(len(payload)) || receivedAck.SHA256 != expectedHash {
		t.Fatalf("receipt acknowledgement = %+v", receivedAck)
	}
	if err := guestConn.CloseWrite(); err != nil {
		t.Fatalf("half-close after receipt acknowledgement: %v", err)
	}
	finalAck := decodeTestAck(t, guestAcks)
	if finalAck.Status != "ack" || finalAck.Mode != "raw" || finalAck.Size != uint64(len(payload)) || finalAck.SHA256 != expectedHash {
		t.Fatalf("final acknowledgement = %+v", finalAck)
	}
	got := <-result
	if got.err != nil {
		t.Fatalf("receiveTransferSized() error = %v", got.err)
	}
	if got.stats.Bytes != uint64(len(payload)) || got.stats.Hash != expectedHash || !got.stats.EOF || got.stats.Err != nil {
		t.Fatalf("transfer stats = %+v", got.stats)
	}
}

func TestReceiveTransferSizedNacksInvalidPayload(t *testing.T) {
	payload := deterministicTestPayload(257)
	correctHash := testSHA256(payload)
	for _, tc := range []struct {
		name         string
		headerMode   string
		declaredSize uint64
		payload      []byte
		expectedSize uint64
		expectedHash string
	}{
		{name: "mode", headerMode: "yamux", declaredSize: uint64(len(payload)), payload: payload, expectedSize: uint64(len(payload)), expectedHash: correctHash},
		{name: "announced size", headerMode: "raw", declaredSize: uint64(len(payload) - 1), payload: payload, expectedSize: uint64(len(payload)), expectedHash: correctHash},
		{name: "short payload", headerMode: "raw", declaredSize: uint64(len(payload) + 1), payload: payload, expectedSize: uint64(len(payload) + 1), expectedHash: correctHash},
		{name: "hash", headerMode: "raw", declaredSize: uint64(len(payload)), payload: payload, expectedSize: uint64(len(payload)), expectedHash: strings.Repeat("0", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := transferInput(t, tc.headerMode, tc.declaredSize, tc.payload)
			var ackBytes bytes.Buffer
			stats, err := receiveTransferSized(bufio.NewReader(bytes.NewReader(input)), &ackBytes, "raw", tc.expectedSize, tc.expectedHash)
			if err == nil {
				t.Fatal("receiveTransferSized() succeeded for invalid payload")
			}
			if stats.Bytes != uint64(len(payload)) {
				t.Fatalf("received bytes = %d, want %d", stats.Bytes, len(payload))
			}
			if tc.name == "short payload" && !strings.Contains(err.Error(), "received 257 bytes; expected 258") {
				t.Fatalf("short payload error = %v, want actual count", err)
			}
			ack := decodeTestAck(t, bufio.NewReader(&ackBytes))
			if ack.Status != "nack" {
				t.Fatalf("ack status = %q, want nack", ack.Status)
			}
		})
	}
}

func TestReceiveTransferSizedRejectsTrailingData(t *testing.T) {
	payload := deterministicTestPayload(129)
	trailing := []byte("extra")
	input := transferInput(t, "raw", uint64(len(payload)), append(append([]byte(nil), payload...), trailing...))
	var ackBytes bytes.Buffer

	stats, err := receiveTransferSized(bufio.NewReader(bytes.NewReader(input)), &ackBytes, "raw", uint64(len(payload)), testSHA256(payload))
	if err == nil || !strings.Contains(err.Error(), "trailing bytes") {
		t.Fatalf("receiveTransferSized() error = %v, want trailing data rejection", err)
	}
	if stats.Bytes != uint64(len(payload)) || !stats.EOF {
		t.Fatalf("transfer stats = %+v, want announced count and real EOF", stats)
	}
	acks := decodeTestAcks(t, &ackBytes)
	if len(acks) != 2 || acks[0].Status != "received" || acks[1].Status != "nack" {
		t.Fatalf("acknowledgements = %+v, want receipt then final nack", acks)
	}
}

func TestReceiveTransferSizedRejectsMissingRealEOF(t *testing.T) {
	payload := deterministicTestPayload(131)
	input := transferInput(t, "raw", uint64(len(payload)), payload)
	readErr := errors.New("simulated timeout while waiting for peer FIN")
	reader := &errorAfterBytesReader{remaining: bytes.NewReader(input), err: readErr}
	var ackBytes bytes.Buffer

	stats, err := receiveTransferSized(bufio.NewReader(reader), &ackBytes, "raw", uint64(len(payload)), testSHA256(payload))
	if !errors.Is(err, readErr) {
		t.Fatalf("receiveTransferSized() error = %v, want missing-EOF error", err)
	}
	if stats.Bytes != uint64(len(payload)) || stats.EOF || !errors.Is(stats.Err, readErr) {
		t.Fatalf("transfer stats = %+v, want exact count without EOF", stats)
	}
	acks := decodeTestAcks(t, &ackBytes)
	if len(acks) != 2 || acks[0].Status != "received" || acks[1].Status != "nack" {
		t.Fatalf("acknowledgements = %+v, want receipt then final nack", acks)
	}
}

func TestDiagnosticCaptureIsBoundedAndMarksTruncation(t *testing.T) {
	var capture diagnosticCapture
	data := bytes.Repeat([]byte("x"), maxGuestOutput+100)
	capture.add(studioruntime.RunOutput{Stderr: true, Data: data})
	capture.add(studioruntime.RunOutput{Data: []byte("later output")})

	got, truncated := capture.snapshot()
	if len(got) != maxGuestOutput {
		t.Fatalf("captured %d bytes, want limit %d", len(got), maxGuestOutput)
	}
	if !truncated {
		t.Fatal("capture truncation was not reported")
	}
	if !bytes.HasPrefix(got, []byte("stderr: ")) {
		t.Fatalf("captured output lacks stream label: %q", got[:16])
	}
}

type errorAfterBytesReader struct {
	remaining *bytes.Reader
	err       error
}

func (r *errorAfterBytesReader) Read(p []byte) (int, error) {
	if r.remaining.Len() > 0 {
		return r.remaining.Read(p)
	}
	return 0, r.err
}

func transferInput(t *testing.T, mode string, declaredSize uint64, payload []byte) []byte {
	t.Helper()
	header, err := json.Marshal(handshake{Mode: mode, Size: declaredSize})
	if err != nil {
		t.Fatalf("marshal test handshake: %v", err)
	}
	return append(append(header, '\n'), payload...)
}

func decodeTestAck(t *testing.T, reader *bufio.Reader) acknowledgement {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read acknowledgement: %v", err)
	}
	var ack acknowledgement
	if err := json.Unmarshal(line, &ack); err != nil {
		t.Fatalf("decode acknowledgement: %v", err)
	}
	return ack
}

func decodeTestAcks(t *testing.T, source io.Reader) []acknowledgement {
	t.Helper()
	reader := bufio.NewReader(source)
	var acks []acknowledgement
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var ack acknowledgement
			if decodeErr := json.Unmarshal(line, &ack); decodeErr != nil {
				t.Fatalf("decode acknowledgement: %v", decodeErr)
			}
			acks = append(acks, ack)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return acks
			}
			t.Fatalf("read acknowledgements: %v", err)
		}
	}
}

type splitPipeConn struct {
	reader net.Conn
	writer net.Conn
}

// Two net.Pipe pairs provide a duplex connection with an independent write half-close.
func newSplitPipePair() (*splitPipeConn, *splitPipeConn) {
	guestToHostWriter, guestToHostReader := net.Pipe()
	hostToGuestWriter, hostToGuestReader := net.Pipe()
	guest := &splitPipeConn{reader: hostToGuestReader, writer: guestToHostWriter}
	host := &splitPipeConn{reader: guestToHostReader, writer: hostToGuestWriter}
	return host, guest
}

func (c *splitPipeConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *splitPipeConn) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *splitPipeConn) CloseWrite() error           { return c.writer.Close() }
func (c *splitPipeConn) Close() error                { return errors.Join(c.reader.Close(), c.writer.Close()) }
func (c *splitPipeConn) LocalAddr() net.Addr         { return c.reader.LocalAddr() }
func (c *splitPipeConn) RemoteAddr() net.Addr        { return c.reader.RemoteAddr() }
func (c *splitPipeConn) SetDeadline(deadline time.Time) error {
	return errors.Join(c.reader.SetDeadline(deadline), c.writer.SetDeadline(deadline))
}
func (c *splitPipeConn) SetReadDeadline(deadline time.Time) error {
	return c.reader.SetReadDeadline(deadline)
}
func (c *splitPipeConn) SetWriteDeadline(deadline time.Time) error {
	return c.writer.SetWriteDeadline(deadline)
}

func writeTestBytes(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func deterministicTestPayload(size int) []byte {
	data := make([]byte, size)
	fillPattern(data, 0)
	return data
}

func testSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
