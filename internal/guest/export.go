//go:build linux

package guest

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

const exportOperationTimeout = 90 * time.Second

func (a *Agent) serveExport(st net.Conn, br *bufio.Reader) error {
	if !a.exporting.CompareAndSwap(false, true) {
		return writeExportFailure(st, errors.New("another export is already running"))
	}
	if a.export == nil {
		a.exporting.Store(false)
		return writeExportFailure(st, errors.New("guest export is unavailable"))
	}
	if !a.configMu.TryLock() {
		a.exporting.Store(false)
		return writeExportFailure(st, errors.New("configuration update is in progress"))
	}
	defer a.configMu.Unlock()
	defer a.exporting.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), exportOperationTimeout)
	deadline, _ := ctx.Deadline()
	if err := st.SetWriteDeadline(deadline); err != nil {
		cancel()
		return err
	}

	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		_, _ = br.ReadByte()
		// The host sends only the JSON header. Any later byte is invalid, and
		// EOF/error means the host abandoned this stream.
		cancel()
		_ = st.SetWriteDeadline(time.Now())
	}()

	framedDst := &exportCountingWriter{dst: st}
	exportErr := a.export(ctx, framedDst)
	if exportErr != nil && framedDst.written == 0 && ctx.Err() == nil {
		if frameErr := writeExportFailure(st, exportErr); frameErr != nil {
			exportErr = errors.Join(exportErr, frameErr)
		}
	}
	ctxErr := ctx.Err()
	cancel()
	_ = st.Close() // also releases the watcher on the normal completion path
	<-watchDone
	if ctxErr != nil {
		return ctxErr
	}
	return exportErr
}

// writeExportFailure uses the export framing for errors discovered before the
// producer starts. WriteExport bounds and sanitizes the error frame.
func writeExportFailure(st net.Conn, failure error) error {
	if failure == nil {
		failure = errors.New("guest export failed")
	}
	deadline := time.Now().Add(time.Second)
	_ = st.SetWriteDeadline(deadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	sourceErr := &exportSourceError{err: failure}
	_, err := agentproto.WriteExport(ctx, st, exportFailureReader{err: sourceErr}, 1)
	if err == sourceErr {
		return nil
	}
	return err
}

type exportFailureReader struct{ err error }

func (r exportFailureReader) Read([]byte) (int, error) { return 0, r.err }

type exportSourceError struct{ err error }

func (e *exportSourceError) Error() string { return e.err.Error() }

func (e *exportSourceError) Unwrap() error { return e.err }

type exportCountingWriter struct {
	dst     io.Writer
	written int64
}

func (w *exportCountingWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.written += int64(n)
	return n, err
}
