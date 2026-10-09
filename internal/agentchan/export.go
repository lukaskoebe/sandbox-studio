package agentchan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
)

const exportOperationTimeout = 90 * time.Second

// interruptibleExportConn makes closing a yamux stream interrupt local reads
// as well as sending the stream's close frame to the guest.
type interruptibleExportConn struct{ net.Conn }

func (c *interruptibleExportConn) Close() error {
	_ = c.Conn.SetDeadline(time.Now())
	return c.Conn.Close()
}

// ErrExportInProgress reports that this guest already has a pending or active
// export on its current agent connection.
var ErrExportInProgress = errors.New("guest export already pending or in progress")

// Export asks a sandbox to export its writable layer and stores the validated,
// normalized layer artifact in dir. Receive owns and closes the stream.
func (h *Hub) Export(ctx context.Context, id, dir string, limits ocilayer.Limits) (templateexport.Layer, error) {
	if ctx == nil {
		return templateexport.Layer{}, errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return templateexport.Layer{}, err
	}

	opCtx, cancel := context.WithTimeout(ctx, exportOperationTimeout)
	defer cancel()
	if err := opCtx.Err(); err != nil {
		return templateexport.Layer{}, err
	}

	st, release, err := h.openExport(opCtx, id)
	if err != nil {
		return templateexport.Layer{}, err
	}
	defer release()
	source := &interruptibleExportConn{Conn: st}
	if deadline, ok := opCtx.Deadline(); ok {
		if err := source.SetDeadline(deadline); err != nil {
			_ = source.Close()
			if ctxErr := opCtx.Err(); ctxErr != nil {
				return templateexport.Layer{}, ctxErr
			}
			return templateexport.Layer{}, fmt.Errorf("set export stream deadline: %w", err)
		}
	}
	stopClose := context.AfterFunc(opCtx, func() { _ = source.Close() })
	defer stopClose()
	if err := opCtx.Err(); err != nil {
		_ = source.Close()
		return templateexport.Layer{}, err
	}
	if err := agentproto.WriteJSONLine(source, agentproto.Header{Kind: agentproto.KindExport}); err != nil {
		_ = source.Close()
		if ctxErr := opCtx.Err(); ctxErr != nil {
			return templateexport.Layer{}, ctxErr
		}
		return templateexport.Layer{}, fmt.Errorf("write export request: %w", err)
	}
	if err := opCtx.Err(); err != nil {
		_ = source.Close()
		return templateexport.Layer{}, err
	}
	stopClose()
	return templateexport.Receive(opCtx, dir, source, limits)
}

// openExport isolates yamux's context-free Open call. If the caller cancels
// first, a stream that opens later is closed by the opener before it can leak.
func (h *Hub) openExport(ctx context.Context, id string) (net.Conn, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	c, ok := h.sessions[id]
	h.mu.Unlock()
	if !ok {
		return nil, nil, ErrNotConnected
	}
	return openExportStream(ctx, c.exportOpen, c.sess.Open)
}

// openExportStream runs yamux's context-free opener behind a per-connection
// gate. The caller owns release while using a returned stream. If its context
// ends first, the opener retains the gate until it closes any late stream.
func openExportStream(ctx context.Context, gate chan struct{}, open func() (net.Conn, error)) (net.Conn, func(), error) {
	return openLimitedStream(ctx, gate, open, ErrExportInProgress)
}

// openLimitedStream also bounds pending control requests. A canceled opener
// retains its slot until any late stream has closed, so cancellation cannot
// accumulate an unbounded number of blocked yamux openers.
func openLimitedStream(ctx context.Context, gate chan struct{}, open func() (net.Conn, error), busy error) (net.Conn, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("nil context")
	}
	if gate == nil {
		return nil, nil, errors.New("stream connection gate is unavailable")
	}
	if open == nil {
		return nil, nil, errors.New("stream opener is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case gate <- struct{}{}:
	default:
		return nil, nil, busy
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { <-gate }) }
	if err := ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}

	type openResult struct {
		st  net.Conn
		err error
	}
	opened := make(chan openResult)
	go func() {
		st, err := open()
		select {
		case opened <- openResult{st: st, err: err}:
		case <-ctx.Done():
			if st != nil {
				_ = st.Close()
			}
			release()
		}
	}()
	select {
	case result := <-opened:
		if err := ctx.Err(); err != nil {
			if result.st != nil {
				_ = result.st.Close()
			}
			release()
			return nil, nil, err
		}
		if result.err != nil {
			release()
			return nil, nil, result.err
		}
		if result.st == nil {
			release()
			return nil, nil, errors.New("yamux returned a nil stream")
		}
		return result.st, release, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}
