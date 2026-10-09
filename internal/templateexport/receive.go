// Package templateexport receives and validates guest-produced OCI layers.
package templateexport

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
)

// Layer describes a validated, normalized, gzip-compressed OCI layer artifact.
type Layer struct {
	Path             string
	Digest           string
	DiffID           string
	Size             int64
	UncompressedSize int64
	Entries          int64
}

// Receive consumes one guest export stream and stores a normalized OCI layer
// gzip blob in dir. Size is the compressed blob size; UncompressedSize is the
// normalized tar size. Digest and DiffID use the OCI "sha256:<hex>" form.
// Receive takes ownership of src and closes it on every return. src.Close must
// unblock a concurrent Read; cancellation and early tar rejection rely on it.
// The caller must provide an existing private directory. Failures discard and
// remove the temporary artifact; Receive never extracts layer entries.
func Receive(ctx context.Context, dir string, src io.ReadCloser, limits ocilayer.Limits) (layer Layer, retErr error) {
	var artifactPath string
	var artifact *os.File
	keepArtifact := false
	var sourceCloseOnce sync.Once
	var sourceCloseErr error
	closeSource := func() error {
		if src == nil {
			return nil
		}
		sourceCloseOnce.Do(func() { sourceCloseErr = src.Close() })
		return sourceCloseErr
	}
	defer func() {
		if artifact != nil {
			if err := artifact.Close(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("close layer artifact: %w", err))
				layer = Layer{}
			}
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				retErr = errors.Join(err, retErr)
				layer = Layer{}
			}
		}
		if err := closeSource(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close export source: %w", err))
			layer = Layer{}
		}
		if retErr != nil {
			layer = Layer{}
		}
		if artifactPath != "" && (!keepArtifact || retErr != nil) {
			if err := os.Remove(artifactPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove incomplete layer artifact: %w", err))
				layer = Layer{}
			}
		}
	}()

	if src == nil {
		return Layer{}, errors.New("nil export source")
	}
	if ctx == nil {
		return Layer{}, errors.New("nil context")
	}
	if dir == "" {
		return Layer{}, errors.New("layer artifact directory is required")
	}
	if err := ctx.Err(); err != nil {
		return Layer{}, err
	}
	if limits.MaxInputBytes < 0 {
		return Layer{}, errors.New("OCI layer input limit must not be negative")
	}
	if limits.MaxInputBytes == 0 {
		limits.MaxInputBytes = ocilayer.DefaultLimits().MaxInputBytes
	}

	var err error
	artifact, err = os.CreateTemp(dir, ".oci-layer-*.tar.gz")
	if err != nil {
		return Layer{}, fmt.Errorf("create layer artifact: %w", err)
	}
	artifactPath = artifact.Name()
	if err := artifact.Chmod(0o600); err != nil {
		return Layer{}, fmt.Errorf("set layer artifact permissions: %w", err)
	}

	blobHash := sha256.New()
	blobOutput := &countingWriter{writer: io.MultiWriter(artifact, blobHash)}
	gz, err := gzip.NewWriterLevel(blobOutput, gzip.DefaultCompression)
	if err != nil {
		return Layer{}, fmt.Errorf("create gzip writer: %w", err)
	}
	gz.Header.ModTime = time.Time{}
	gz.Header.Name = ""
	gz.Header.Comment = ""
	gz.Header.OS = 255

	diffHash := sha256.New()
	normalizedOutput := io.MultiWriter(diffHash, gz)
	pipeReader, pipeWriter := io.Pipe()
	producerResult := make(chan error, 1)
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		_, readErr := agentproto.ReadExport(ctx, pipeWriter, src, limits.MaxInputBytes)
		// Publish the framing result before closing the pipe. Normalize can then
		// distinguish a wire failure from its own early tar rejection.
		producerResult <- readErr
		if readErr != nil {
			_ = pipeWriter.CloseWithError(readErr)
		} else {
			_ = pipeWriter.Close()
		}
	}()

	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			closeErr := ctx.Err()
			_ = closeSource()
			_ = pipeReader.CloseWithError(closeErr)
			_ = pipeWriter.CloseWithError(closeErr)
		case <-watchStop:
		}
	}()

	stats, normalizeErr := ocilayer.Normalize(ctx, normalizedOutput, pipeReader, limits)
	var wireErr error
	wireFinished := false
	select {
	case wireErr = <-producerResult:
		wireFinished = true
	default:
	}
	abortForNormalize := normalizeErr != nil && !wireFinished
	if !wireFinished {
		abortErr := normalizeErr
		if ctxErr := ctx.Err(); ctxErr != nil {
			abortErr = ctxErr
		}
		if abortErr == nil {
			abortErr = errors.New("export producer did not finish after normalization")
		}
		_ = closeSource()
		_ = pipeReader.CloseWithError(abortErr)
		_ = pipeWriter.CloseWithError(abortErr)
		wireErr = <-producerResult
	}
	<-producerDone
	_ = pipeReader.Close()
	close(watchStop)
	<-watchDone

	var operationErr error
	if ctxErr := ctx.Err(); ctxErr != nil {
		operationErr = ctxErr
	} else if abortForNormalize {
		operationErr = normalizeErr
	} else if wireErr != nil {
		operationErr = wireErr
	} else if normalizeErr != nil {
		operationErr = normalizeErr
	}

	gzipErr := gz.Close()
	if gzipErr != nil {
		operationErr = errors.Join(operationErr, fmt.Errorf("finish gzip layer: %w", gzipErr))
	}
	if operationErr != nil {
		return Layer{}, operationErr
	}
	if err := ctx.Err(); err != nil {
		return Layer{}, err
	}

	layer = Layer{
		Path:             artifactPath,
		Digest:           "sha256:" + hex.EncodeToString(blobHash.Sum(nil)),
		DiffID:           "sha256:" + hex.EncodeToString(diffHash.Sum(nil)),
		Size:             blobOutput.count,
		UncompressedSize: stats.OutputBytes,
		Entries:          stats.Entries,
	}
	keepArtifact = true
	return layer, nil
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.count += int64(n)
	return n, err
}
