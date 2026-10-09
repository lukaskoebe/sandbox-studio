//go:build linux

// Command vsock-bulk-guest sends a deterministic payload to the private host route.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/mdlayher/vsock"
)

const (
	payloadSize      = uint64(128 << 20)
	readBufferSize   = 128 << 10
	progressInterval = uint64(8 << 20)
	guestDeadline    = 40 * time.Second
	maxAckBytes      = 1024
)

type handshake struct {
	Mode string `json:"mode"`
	Size uint64 `json:"size"`
}

type acknowledgement struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
}

func main() {
	port := flag.Uint("port", 5001, "private host vsock route port")
	maxWrite := flag.Int("max-write", 0, "maximum bytes per underlying vsock Write call (0 leaves writes unchanged)")
	flag.Parse()
	args := flag.Args()
	if *port == 0 || uint64(*port) > uint64(^uint32(0)) || *maxWrite < 0 || *maxWrite > 65536 || len(args) != 1 || (args[0] != "raw" && args[0] != "yamux") {
		fmt.Fprintln(os.Stderr, "usage: vsock-bulk [-port 5001] [-max-write 0..65536] raw|yamux")
		os.Exit(2)
	}
	if err := run(args[0], uint32(*port), *maxWrite); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR mode=%s max_write_bytes=%d: %v\n", args[0], *maxWrite, err)
		os.Exit(1)
	}
}

func run(mode string, port uint32, maxWrite int) error {
	started := time.Now()
	conn, err := vsock.Dial(vsock.Host, port, nil)
	if err != nil {
		return fmt.Errorf("dial host vsock port %d: %w", port, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(guestDeadline)); err != nil {
		return fmt.Errorf("set vsock deadline: %w", err)
	}
	var transportConn net.Conn = conn
	if maxWrite > 0 {
		transportConn = &maxWriteConn{Conn: conn, limit: maxWrite}
	}

	var resultErr error
	switch mode {
	case "raw":
		resultErr = transferRaw(transportConn, conn.CloseWrite, mode)
	case "yamux":
		resultErr = transferYamux(transportConn, mode)
	default:
		resultErr = fmt.Errorf("unsupported mode %q", mode)
	}
	if resultErr != nil {
		return resultErr
	}
	fmt.Printf("GUEST mode=%s status=complete bytes=%d max_write_bytes=%d duration=%s\n", mode, payloadSize, maxWrite, time.Since(started).Round(time.Millisecond))
	return nil
}

func transferRaw(conn net.Conn, closeWrite func() error, mode string) error {
	return transfer(conn, conn, closeWrite, mode)
}

func transferYamux(conn net.Conn, mode string) error {
	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		return fmt.Errorf("start yamux client: %w", err)
	}
	defer session.Close()
	stream, err := session.Open()
	if err != nil {
		return fmt.Errorf("open guest-to-host bulk stream: %w", err)
	}
	defer stream.Close()
	if err := stream.SetDeadline(time.Now().Add(guestDeadline)); err != nil {
		return fmt.Errorf("set yamux stream deadline: %w", err)
	}
	// yamux v0.1.2 Stream.Close sends FIN while retaining the read side for ACK.
	return transfer(stream, stream, stream.Close, mode)
}

type maxWriteConn struct {
	net.Conn
	limit int
}

func (c *maxWriteConn) Write(p []byte) (int, error) {
	if c.limit <= 0 {
		return c.Conn.Write(p)
	}
	var total int
	for len(p) > 0 {
		chunk := p
		if len(chunk) > c.limit {
			chunk = chunk[:c.limit]
		}
		n, err := c.Conn.Write(chunk)
		if n < 0 || n > len(chunk) {
			return total, io.ErrShortWrite
		}
		if n > 0 {
			total += n
			p = p[n:]
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func transfer(w io.Writer, r io.Reader, closeWrite func() error, mode string) error {
	return transferSized(w, r, closeWrite, mode, payloadSize)
}

func transferSized(w io.Writer, r io.Reader, closeWrite func() error, mode string, size uint64) error {
	ackReader := bufio.NewReaderSize(r, maxAckBytes)
	if err := writeJSONLine(w, handshake{Mode: mode, Size: size}); err != nil {
		return errors.Join(fmt.Errorf("write bounded handshake: %w", err), closeGuestWrite(closeWrite))
	}
	sent, digest, sendErr := sendPayloadSized(w, size)
	if sendErr != nil {
		transferErr := errors.Join(
			fmt.Errorf("send payload: sent %d/%d bytes: %w", sent, size, sendErr),
			closeGuestWrite(closeWrite),
		)
		receipt, err := readAck(ackReader)
		if err != nil {
			return errors.Join(transferErr, fmt.Errorf("read host receipt response after %d bytes: %w", sent, err))
		}
		receiptErr := validateAck(receipt, "received", mode, size, digest)
		if receiptErr == nil {
			final, err := readAck(ackReader)
			if err != nil {
				return errors.Join(transferErr, fmt.Errorf("read final host acknowledgement: %w", err))
			}
			if err := validateAck(final, "ack", mode, size, digest); err != nil {
				transferErr = errors.Join(transferErr, err)
			}
		} else {
			transferErr = errors.Join(transferErr, receiptErr)
		}
		return transferErr
	}

	receipt, err := readAck(ackReader)
	if err != nil {
		return errors.Join(fmt.Errorf("read host receipt acknowledgement after %d bytes: %w", sent, err), closeGuestWrite(closeWrite))
	}
	if err := validateAck(receipt, "received", mode, size, digest); err != nil {
		return errors.Join(err, closeGuestWrite(closeWrite))
	}
	if err := closeGuestWrite(closeWrite); err != nil {
		return err
	}
	final, err := readAck(ackReader)
	if err != nil {
		return fmt.Errorf("read final host acknowledgement after %d bytes: %w", sent, err)
	}
	if err := validateAck(final, "ack", mode, size, digest); err != nil {
		return err
	}
	return nil
}

func closeGuestWrite(closeWrite func() error) error {
	if err := closeWrite(); err != nil {
		return fmt.Errorf("close guest write side: %w", err)
	}
	return nil
}

func validateAck(ack acknowledgement, wantStatus, mode string, size uint64, digest string) error {
	if ack.Status != wantStatus || ack.Mode != mode || ack.Size != size || ack.SHA256 != digest {
		return fmt.Errorf("host acknowledgement mismatch: status=%q mode=%q bytes=%d sha256=%s; want status=%q mode=%q bytes=%d sha256=%s",
			ack.Status, ack.Mode, ack.Size, ack.SHA256, wantStatus, mode, size, digest)
	}
	return nil
}

func sendPayload(w io.Writer) (uint64, string, error) {
	return sendPayloadSized(w, payloadSize)
}

func sendPayloadSized(w io.Writer, size uint64) (uint64, string, error) {
	h := sha256.New()
	buf := make([]byte, readBufferSize)
	var sent uint64
	nextProgress := progressInterval
	for sent < size {
		n := uint64(len(buf))
		if remaining := size - sent; n > remaining {
			n = remaining
		}
		fillPattern(buf[:n], sent)
		written, err := writeAndHash(w, h, buf[:n])
		sent += written
		if sent >= nextProgress {
			fmt.Printf("PROGRESS sent=%d/%d\n", sent, size)
			for nextProgress <= sent {
				nextProgress += progressInterval
			}
		}
		if err != nil {
			return sent, hex.EncodeToString(h.Sum(nil)), err
		}
	}
	return sent, hex.EncodeToString(h.Sum(nil)), nil
}

func writeAndHash(w io.Writer, h hash.Hash, value []byte) (uint64, error) {
	var written uint64
	for len(value) > 0 {
		n, err := w.Write(value)
		if n > 0 {
			_, _ = h.Write(value[:n])
			written += uint64(n)
			value = value[n:]
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func writeJSONLine(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	for len(data) > 0 {
		n, err := w.Write(data)
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

func readAck(reader *bufio.Reader) (acknowledgement, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return acknowledgement{}, err
	}
	if len(line) > maxAckBytes {
		return acknowledgement{}, errors.New("host acknowledgement exceeds the 1024-byte limit")
	}
	var ack acknowledgement
	if err := json.Unmarshal(line, &ack); err != nil {
		return acknowledgement{}, err
	}
	return ack, nil
}

func fillPattern(buf []byte, offset uint64) {
	for i := range buf {
		position := offset + uint64(i)
		buf[i] = byte(position*31 + position/256*17 + 0x5a)
	}
}
