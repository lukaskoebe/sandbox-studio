package agentproto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Export framing is what `studio-agent export-layer` writes to stdout, which
// Studio reads over msb exec. It uses the same
// [type u8][length u32 BE][payload] layout as WriteFrame, with smaller limits.
// ExportMaxFreeze bounds how long `studio-agent export-layer --max-freeze` may
// hold the guest root filesystem frozen. The host's export time limit uses the
// same bound.
const ExportMaxFreeze = 2 * time.Hour

const (
	// ExportVersion is the version in the completion frame.
	ExportVersion = 1
	// ExportFrameData carries raw bytes, in chunks no larger than ExportDataChunkSize.
	ExportFrameData byte = FrameData
	// ExportFrameComplete carries the bounded JSON completion object.
	ExportFrameComplete byte = 3
	// ExportFrameError carries a UTF-8 failure message.
	ExportFrameError byte = 4
	// ExportDataChunkSize is the maximum raw byte payload in a data frame.
	ExportDataChunkSize = 64 << 10
	// ExportErrorMaxBytes is the maximum error message size in bytes.
	ExportErrorMaxBytes    = 1 << 10
	maxExportCompleteBytes = 256
)

// ExportResult describes raw export bytes, without any frame headers or trailer.
// On error it describes the bytes successfully framed or written before failure.
type ExportResult struct {
	Bytes  int64
	SHA256 string
}

type exportCompletion struct {
	Version int    `json:"version"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
}

// WriteExport frames raw bytes from r onto w, ending with a version 1 completion
// frame containing the byte count and lowercase SHA-256 digest. If reading r
// fails, it attempts to send a bounded error frame and never sends completion.
// ctx must not be nil and maxBytes must be positive. The context is checked
// between I/O operations. Canceling it cannot interrupt a blocked generic
// Reader or Writer, so callers must close the underlying stream to unblock I/O.
// The function does not close w or r.
func WriteExport(ctx context.Context, w io.Writer, r io.Reader, maxBytes int64) (ExportResult, error) {
	if maxBytes <= 0 {
		return ExportResult{}, errors.New("export maximum must be positive")
	}

	h := sha256.New()
	var total int64
	snapshot := func() ExportResult {
		return ExportResult{Bytes: total, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	failSource := func(sourceErr error) (ExportResult, error) {
		if err := ctx.Err(); err != nil {
			return snapshot(), err
		}
		if err := writeExportFrame(w, ExportFrameError, boundedExportError(sourceErr)); err != nil {
			return snapshot(), fmt.Errorf("producer error: %w; write error frame: %w", sourceErr, err)
		}
		return snapshot(), sourceErr
	}

	buf := make([]byte, ExportDataChunkSize)
	noProgress := 0
	for {
		if err := ctx.Err(); err != nil {
			return snapshot(), err
		}
		remaining := maxBytes - total
		readSize := len(buf)
		if remaining < int64(readSize) {
			// Read at most one byte beyond the limit so an exact-size input can
			// still be distinguished from an oversized input.
			readSize = int(remaining) + 1
		}
		n, readErr := r.Read(buf[:readSize])
		if n < 0 || n > readSize {
			return failSource(fmt.Errorf("invalid export source read count %d", n))
		}
		if err := ctx.Err(); err != nil {
			return snapshot(), err
		}

		if n > 0 {
			noProgress = 0
			allowed := n
			overLimit := int64(n) > remaining
			if overLimit {
				allowed = int(remaining)
			}
			if allowed > 0 {
				if err := writeExportFrame(w, ExportFrameData, buf[:allowed]); err != nil {
					return snapshot(), fmt.Errorf("write export data frame: %w", err)
				}
				_, _ = h.Write(buf[:allowed])
				total += int64(allowed)
			}
			if overLimit {
				return failSource(fmt.Errorf("export exceeds maximum size of %d bytes", maxBytes))
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if err := ctx.Err(); err != nil {
					return snapshot(), err
				}
				trailer, err := json.Marshal(exportCompletion{
					Version: ExportVersion,
					Bytes:   total,
					SHA256:  hex.EncodeToString(h.Sum(nil)),
				})
				if err != nil {
					return snapshot(), fmt.Errorf("marshal export completion: %w", err)
				}
				if len(trailer) > maxExportCompleteBytes {
					return snapshot(), errors.New("export completion frame exceeds internal bound")
				}
				if err := ctx.Err(); err != nil {
					return snapshot(), err
				}
				if err := writeExportFrame(w, ExportFrameComplete, trailer); err != nil {
					return snapshot(), fmt.Errorf("write export completion frame: %w", err)
				}
				return snapshot(), nil
			}
			return failSource(readErr)
		}

		if n == 0 {
			noProgress++
			if noProgress >= 100 {
				return failSource(io.ErrNoProgress)
			}
		}
	}
}

// ReadExport decodes an export stream from r and writes its raw bytes to w.
// It returns success only after verifying the version, count, digest, and EOF
// after the completion frame. It writes bytes as they arrive, so callers should
// discard the destination unless this function returns nil error. ctx must not
// be nil and maxBytes must be positive. The context is checked between I/O
// operations. Canceling it cannot interrupt a blocked generic Reader or Writer,
// so callers must close the underlying stream to unblock I/O. The function does
// not close w or r.
func ReadExport(ctx context.Context, w io.Writer, r io.Reader, maxBytes int64) (ExportResult, error) {
	if maxBytes <= 0 {
		return ExportResult{}, errors.New("export maximum must be positive")
	}

	h := sha256.New()
	var total int64
	snapshot := func() ExportResult {
		return ExportResult{Bytes: total, SHA256: hex.EncodeToString(h.Sum(nil))}
	}

	for {
		if err := ctx.Err(); err != nil {
			return snapshot(), err
		}
		typ, payload, err := readExportFrame(r, maxBytes-total)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return snapshot(), errors.New("export stream ended before completion frame")
			}
			return snapshot(), fmt.Errorf("read export frame: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return snapshot(), err
		}

		switch typ {
		case ExportFrameData:
			if len(payload) == 0 {
				return snapshot(), errors.New("empty export data frame")
			}
			written, err := writeAll(w, payload)
			if written > 0 {
				_, _ = h.Write(payload[:written])
				total += int64(written)
			}
			if err != nil {
				return snapshot(), fmt.Errorf("write export data: %w", err)
			}

		case ExportFrameComplete:
			completion, err := decodeExportCompletion(payload)
			if err != nil {
				return snapshot(), fmt.Errorf("invalid export completion: %w", err)
			}
			if completion.Version != ExportVersion {
				return snapshot(), fmt.Errorf("unsupported export version %d", completion.Version)
			}
			if completion.Bytes != total {
				return snapshot(), fmt.Errorf("export byte count mismatch: trailer says %d, received %d", completion.Bytes, total)
			}
			wantDigest := hex.EncodeToString(h.Sum(nil))
			if completion.SHA256 != wantDigest {
				return snapshot(), errors.New("export SHA-256 mismatch")
			}
			if err := requireExportEOF(ctx, r); err != nil {
				return snapshot(), fmt.Errorf("export stream did not end after completion: %w", err)
			}
			return snapshot(), nil

		case ExportFrameError:
			if len(payload) == 0 || !utf8.Valid(payload) {
				return snapshot(), errors.New("invalid export error frame message")
			}
			remoteErr := fmt.Errorf("remote export error: %s", string(payload))
			if err := requireExportEOF(ctx, r); err != nil {
				return snapshot(), fmt.Errorf("%w (invalid stream after error frame: %v)", remoteErr, err)
			}
			return snapshot(), remoteErr
		}
	}
}

// writeExportFrame writes one export frame using the shared 5-byte frame header.
func writeExportFrame(w io.Writer, typ byte, payload []byte) error {
	limit, ok := exportFrameLimit(typ)
	if !ok {
		return fmt.Errorf("unknown export frame type %d", typ)
	}
	if len(payload) > limit {
		return fmt.Errorf("export frame too large: %d bytes", len(payload))
	}
	var header [5]byte
	header[0] = typ
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := writeAll(w, header[:]); err != nil {
		return err
	}
	if _, err := writeAll(w, payload); err != nil {
		return err
	}
	return nil
}

// readExportFrame checks the type-specific bound and remaining total limit
// before allocating a payload buffer.
func readExportFrame(r io.Reader, remaining int64) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	typ := header[0]
	limit, ok := exportFrameLimit(typ)
	if !ok {
		return 0, nil, fmt.Errorf("unknown export frame type %d", typ)
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > uint32(limit) {
		return 0, nil, fmt.Errorf("export frame too large: %d bytes", n)
	}
	if typ == ExportFrameData && int64(n) > remaining {
		return 0, nil, errors.New("export exceeds maximum size")
	}
	payload := make([]byte, int(n))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return typ, payload, nil
}

func exportFrameLimit(typ byte) (int, bool) {
	switch typ {
	case ExportFrameData:
		return ExportDataChunkSize, true
	case ExportFrameComplete:
		return maxExportCompleteBytes, true
	case ExportFrameError:
		return ExportErrorMaxBytes, true
	default:
		return 0, false
	}
}

func writeAll(w io.Writer, p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n, err := w.Write(p)
		if n < 0 || n > len(p) {
			return written, io.ErrShortWrite
		}
		written += n
		p = p[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func boundedExportError(err error) []byte {
	message := "export failed"
	if err != nil {
		message = err.Error()
	}
	if !utf8.ValidString(message) {
		message = strings.ToValidUTF8(message, "\uFFFD")
	}
	if len(message) > ExportErrorMaxBytes {
		message = message[:ExportErrorMaxBytes]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	if message == "" {
		message = "export failed"
	}
	return []byte(message)
}

func decodeExportCompletion(payload []byte) (exportCompletion, error) {
	var completion exportCompletion
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return completion, err
	}
	if token != json.Delim('{') {
		return completion, errors.New("expected JSON object")
	}
	seen := make(map[string]bool, 3)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return completion, err
		}
		key, ok := token.(string)
		if !ok {
			return completion, errors.New("invalid JSON object key")
		}
		if seen[key] {
			return completion, fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = true
		switch key {
		case "version":
			var value *int
			err = decoder.Decode(&value)
			if err == nil && value == nil {
				err = errors.New("must be an integer")
			}
			if err == nil {
				completion.Version = *value
			}
		case "bytes":
			var value *int64
			err = decoder.Decode(&value)
			if err == nil && value == nil {
				err = errors.New("must be an integer")
			}
			if err == nil {
				completion.Bytes = *value
			}
		case "sha256":
			var value *string
			err = decoder.Decode(&value)
			if err == nil && value == nil {
				err = errors.New("must be a string")
			}
			if err == nil {
				completion.SHA256 = *value
			}
		default:
			return completion, fmt.Errorf("unknown field %q", key)
		}
		if err != nil {
			return completion, fmt.Errorf("invalid field %q: %w", key, err)
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return completion, err
	}
	if token != json.Delim('}') {
		return completion, errors.New("expected end of JSON object")
	}
	if len(seen) != 3 {
		return completion, errors.New("missing required field")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return completion, errors.New("trailing JSON value")
		}
		return completion, err
	}
	if completion.Bytes < 0 {
		return completion, errors.New("negative byte count")
	}
	if len(completion.SHA256) != sha256.Size*2 || strings.ToLower(completion.SHA256) != completion.SHA256 {
		return completion, errors.New("SHA-256 must be 64 lowercase hexadecimal characters")
	}
	if _, err := hex.DecodeString(completion.SHA256); err != nil {
		return completion, errors.New("invalid SHA-256")
	}
	return completion, nil
}

func requireExportEOF(ctx context.Context, r io.Reader) error {
	var one [1]byte
	noProgress := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(one[:])
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if n > 0 {
			return errors.New("trailing data")
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		noProgress++
		if noProgress >= 100 {
			return io.ErrNoProgress
		}
	}
}
