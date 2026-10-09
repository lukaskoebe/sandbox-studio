package agentchan

import (
	"context"
	"errors"
	"net"
	"sync"
)

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
