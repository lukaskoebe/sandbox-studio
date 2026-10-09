package agentproto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestExportRoundTripWithShortWrites(t *testing.T) {
	data := bytes.Repeat([]byte("oci-tar-data-"), 6000)
	wire := &shortWriter{max: 7}
	written, err := WriteExport(context.Background(), wire, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if written.Bytes != int64(len(data)) || written.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("unexpected write result: %+v", written)
	}

	destination := &shortWriter{max: 5}
	read, err := ReadExport(context.Background(), destination, bytes.NewReader(wire.Bytes()), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.Bytes(), data) {
		t.Fatal("round trip changed the raw export bytes")
	}
	if read != written {
		t.Fatalf("read result %+v does not match write result %+v", read, written)
	}
}

func TestWriteExportProducerErrorEmitsBoundedErrorWithoutCompletion(t *testing.T) {
	producerErr := errors.New(strings.Repeat("producer failed ", 200))
	source := &failAfterReader{first: []byte("partial tar"), err: producerErr}
	var wire bytes.Buffer
	result, err := WriteExport(context.Background(), &wire, source, 1024)
	if !errors.Is(err, producerErr) {
		t.Fatalf("got error %v, want producer error", err)
	}
	if result.Bytes != int64(len("partial tar")) {
		t.Fatalf("got partial result %+v", result)
	}

	frames := bytes.NewReader(wire.Bytes())
	typ, payload, err := readExportFrame(frames, 1024)
	if err != nil || typ != ExportFrameData || string(payload) != "partial tar" {
		t.Fatalf("first frame = %d %q, %v", typ, payload, err)
	}
	typ, payload, err = readExportFrame(frames, 1024)
	if err != nil || typ != ExportFrameError || len(payload) > ExportErrorMaxBytes {
		t.Fatalf("error frame = %d (%d bytes), %v", typ, len(payload), err)
	}
	if _, _, err := readExportFrame(frames, 1024); !errors.Is(err, io.EOF) {
		t.Fatalf("expected no completion frame, got %v", err)
	}

	var destination bytes.Buffer
	_, err = ReadExport(context.Background(), &destination, bytes.NewReader(wire.Bytes()), 1024)
	if err == nil || !strings.Contains(err.Error(), "remote export error") {
		t.Fatalf("reader got %v, want remote producer error", err)
	}
	if destination.String() != "partial tar" {
		t.Fatalf("reader received %q", destination.String())
	}
}

func TestReadExportRejectsTruncatedStreams(t *testing.T) {
	valid := encodeExport(t, []byte("tar data"))
	truncatedFrame := []byte{ExportFrameData, 0, 0, 0, 3, 'x'}
	cases := map[string][]byte{
		"empty":                nil,
		"data without trailer": encodeDataFrame(t, []byte("tar data")),
		"partial frame":        truncatedFrame,
		"truncated trailer":    valid[:len(valid)-1],
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadExport(context.Background(), io.Discard, bytes.NewReader(wire), 1024)
			if err == nil {
				t.Fatal("expected truncated stream error")
			}
		})
	}
}

func TestReadExportRejectsBadCompletion(t *testing.T) {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("tar")))
	cases := map[string]exportCompletion{
		"version": {Version: ExportVersion + 1, Bytes: 3, SHA256: digest},
		"count":   {Version: ExportVersion, Bytes: 4, SHA256: digest},
		"digest":  {Version: ExportVersion, Bytes: 3, SHA256: strings.Repeat("0", 64)},
	}
	for name, completion := range cases {
		t.Run(name, func(t *testing.T) {
			wire := encodeFrames(t, []byte("tar"), completion)
			_, err := ReadExport(context.Background(), io.Discard, bytes.NewReader(wire), 1024)
			if err == nil {
				t.Fatal("expected completion validation error")
			}
		})
	}
}

func TestReadExportRejectsOversizedFramesAndMaximum(t *testing.T) {
	t.Run("unknown frame kind", func(t *testing.T) {
		_, err := ReadExport(context.Background(), io.Discard, bytes.NewReader(frameHeader(99, 0)), 1024)
		if err == nil || !strings.Contains(err.Error(), "unknown export frame type") {
			t.Fatalf("got %v, want unknown frame error", err)
		}
	})

	for name, frame := range map[string][]byte{
		"data":       frameHeader(ExportFrameData, ExportDataChunkSize+1),
		"completion": frameHeader(ExportFrameComplete, maxExportCompleteBytes+1),
		"error":      frameHeader(ExportFrameError, ExportErrorMaxBytes+1),
	} {
		t.Run(name+" frame bound before payload", func(t *testing.T) {
			_, err := ReadExport(context.Background(), io.Discard, bytes.NewReader(frame), int64(ExportDataChunkSize*2))
			if err == nil || !strings.Contains(err.Error(), "too large") {
				t.Fatalf("got %v, want oversize frame error before payload read", err)
			}
		})
	}

	t.Run("aggregate limit before destination write", func(t *testing.T) {
		wire := frameHeader(ExportFrameData, 3)
		var destination bytes.Buffer
		_, err := ReadExport(context.Background(), &destination, bytes.NewReader(wire), 2)
		if err == nil || !strings.Contains(err.Error(), "maximum") || destination.Len() != 0 {
			t.Fatalf("got error %v and %d output bytes, want limit error before write", err, destination.Len())
		}
	})

	t.Run("producer maximum", func(t *testing.T) {
		var wire bytes.Buffer
		_, err := WriteExport(context.Background(), &wire, strings.NewReader("four"), 3)
		if err == nil || !strings.Contains(err.Error(), "maximum size") {
			t.Fatalf("got %v, want maximum size error", err)
		}
		frames := bytes.NewReader(wire.Bytes())
		typ, payload, frameErr := readExportFrame(frames, 3)
		if frameErr != nil || typ != ExportFrameData || string(payload) != "fou" {
			t.Fatalf("data frame = %d %q, %v", typ, payload, frameErr)
		}
		typ, _, frameErr = readExportFrame(frames, 3)
		if frameErr != nil || typ != ExportFrameError {
			t.Fatalf("terminal frame = %d, %v; want error frame", typ, frameErr)
		}
		if _, _, frameErr = readExportFrame(frames, 3); !errors.Is(frameErr, io.EOF) {
			t.Fatalf("expected no completion frame, got %v", frameErr)
		}
	})
}

func TestReadExportRejectsDuplicateTerminalAndTrailingData(t *testing.T) {
	valid := encodeExport(t, nil)
	duplicate := append([]byte(nil), valid...)
	duplicate = append(duplicate, encodeExport(t, nil)...)
	trailing := append(append([]byte(nil), valid...), 0x7f)

	for name, wire := range map[string][]byte{"duplicate terminal": duplicate, "trailing byte": trailing} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadExport(context.Background(), io.Discard, bytes.NewReader(wire), 1024)
			if err == nil {
				t.Fatal("expected trailing stream data error")
			}
		})
	}
}

func TestExportCancellationAndWriterErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wire bytes.Buffer
	if _, err := WriteExport(ctx, &wire, strings.NewReader("tar"), 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteExport got %v, want context cancellation", err)
	}
	if wire.Len() != 0 {
		t.Fatalf("canceled writer emitted %d bytes", wire.Len())
	}

	readCtx, cancelRead := context.WithCancel(context.Background())
	_, err := WriteExport(readCtx, &wire, &cancelEOFReader{cancel: cancelRead}, 1024)
	if !errors.Is(err, context.Canceled) || wire.Len() != 0 {
		t.Fatalf("canceled source returned %v and emitted %d bytes", err, wire.Len())
	}

	valid := encodeExport(t, []byte("tar"))
	var destination bytes.Buffer
	if _, err := ReadExport(ctx, &destination, bytes.NewReader(valid), 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadExport got %v, want context cancellation", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("canceled reader wrote %d bytes", destination.Len())
	}

	readCtx, cancelRead = context.WithCancel(context.Background())
	destination.Reset()
	if _, err := ReadExport(readCtx, &destination, &cancelOnRead{Reader: bytes.NewReader(valid), cancel: cancelRead}, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadExport got %v, want mid-stream context cancellation", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("mid-stream cancellation wrote %d bytes", destination.Len())
	}

	writeErr := errors.New("output failed")
	if _, err := WriteExport(context.Background(), &failWriter{err: writeErr}, strings.NewReader("tar"), 1024); !errors.Is(err, writeErr) {
		t.Fatalf("WriteExport got %v, want output error", err)
	}
	if _, err := ReadExport(context.Background(), &failWriter{err: writeErr}, bytes.NewReader(valid), 1024); !errors.Is(err, writeErr) {
		t.Fatalf("ReadExport got %v, want destination error", err)
	}
}

func encodeExport(t *testing.T, data []byte) []byte {
	t.Helper()
	var wire bytes.Buffer
	if _, err := WriteExport(context.Background(), &wire, bytes.NewReader(data), int64(len(data)+1)); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

func encodeDataFrame(t *testing.T, data []byte) []byte {
	t.Helper()
	var wire bytes.Buffer
	if err := writeExportFrame(&wire, ExportFrameData, data); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

func frameHeader(typ byte, n int) []byte {
	var header [5]byte
	header[0] = typ
	binary.BigEndian.PutUint32(header[1:], uint32(n))
	return header[:]
}

func encodeFrames(t *testing.T, data []byte, completion exportCompletion) []byte {
	t.Helper()
	trailer, err := json.Marshal(completion)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if len(data) > 0 {
		if err := writeExportFrame(&wire, ExportFrameData, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeExportFrame(&wire, ExportFrameComplete, trailer); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

type shortWriter struct {
	bytes.Buffer
	max int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.Buffer.Write(p)
}

type failAfterReader struct {
	first []byte
	err   error
	done  bool
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.first), nil
	}
	return 0, r.err
}

type failWriter struct {
	err error
}

func (w *failWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type cancelEOFReader struct {
	cancel context.CancelFunc
}

func (r *cancelEOFReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.EOF
}

type cancelOnRead struct {
	io.Reader
	cancel context.CancelFunc
	done   bool
}

func (r *cancelOnRead) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if !r.done {
		r.done = true
		r.cancel()
	}
	return n, err
}
