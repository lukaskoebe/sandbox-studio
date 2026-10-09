package agentchan

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

var errTestBusy = errors.New("busy")

func TestOpenLimitedStreamBoundsCanceledOpenerAndClosesLateStream(t *testing.T) {
	gate := make(chan struct{}, 1)
	openStarted := make(chan struct{})
	returnFromOpen := make(chan struct{})
	lateStreamClosed := make(chan struct{})
	closeStarted := make(chan struct{})
	allowCloseReturn := make(chan struct{})
	lateClient, latePeer := net.Pipe()
	defer latePeer.Close()
	lateStream := &delayedCloseConn{
		Conn:         lateClient,
		closeStarted: closeStarted,
		allowClose:   allowCloseReturn,
		closed:       lateStreamClosed,
	}

	ctx, cancel := context.WithCancel(context.Background())
	type openResult struct {
		st      net.Conn
		release func()
		err     error
	}
	first := make(chan openResult, 1)
	go func() {
		st, release, err := openLimitedStream(ctx, gate, func() (net.Conn, error) {
			close(openStarted)
			<-returnFromOpen
			return lateStream, nil
		}, errTestBusy)
		first <- openResult{st: st, release: release, err: err}
	}()
	select {
	case <-openStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("injected opener did not start")
	}
	cancel()
	select {
	case got := <-first:
		if !errors.Is(got.err, context.Canceled) || got.st != nil || got.release != nil {
			t.Fatalf("canceled opener returned stream %v, release %v, error %v", got.st, got.release != nil, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled caller remained blocked in yamux opener")
	}

	secondOpened := make(chan struct{}, 1)
	if _, _, err := openLimitedStream(context.Background(), gate, func() (net.Conn, error) {
		secondOpened <- struct{}{}
		return nil, errors.New("unexpected second open")
	}, errTestBusy); !errors.Is(err, errTestBusy) {
		t.Fatalf("second pending open got %v, want errTestBusy", err)
	}
	select {
	case <-secondOpened:
		t.Fatal("second opener ran while the canceled opener was still pending")
	default:
	}

	close(returnFromOpen)
	select {
	case <-closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("late stream was not closed after opener returned")
	}
	closePendingOpener := make(chan struct{}, 1)
	if _, _, err := openLimitedStream(context.Background(), gate, func() (net.Conn, error) {
		closePendingOpener <- struct{}{}
		return nil, errors.New("unexpected open during late close")
	}, errTestBusy); !errors.Is(err, errTestBusy) {
		t.Fatalf("open during late stream close got %v, want errTestBusy", err)
	}
	select {
	case <-closePendingOpener:
		t.Fatal("opener ran before the late stream close completed")
	default:
	}
	close(allowCloseReturn)
	select {
	case <-lateStreamClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("late stream close did not finish")
	}
	select {
	case gate <- struct{}{}:
		<-gate
	case <-time.After(2 * time.Second):
		t.Fatal("opener gate was not released after closing the late stream")
	}

	activeClient, activePeer := net.Pipe()
	active, release, err := openLimitedStream(context.Background(), gate, func() (net.Conn, error) {
		return activeClient, nil
	}, errTestBusy)
	if err != nil || active == nil || release == nil {
		activePeer.Close()
		t.Fatalf("open after late close got stream %v, release %v, error %v", active, release != nil, err)
	}
	activeOpenerCalled := make(chan struct{}, 1)
	if _, _, err := openLimitedStream(context.Background(), gate, func() (net.Conn, error) {
		activeOpenerCalled <- struct{}{}
		return nil, errors.New("unexpected open during active stream")
	}, errTestBusy); !errors.Is(err, errTestBusy) {
		t.Fatalf("open during active stream got %v, want errTestBusy", err)
	}
	select {
	case <-activeOpenerCalled:
		t.Fatal("another opener ran while a stream was active")
	default:
	}
	_ = active.Close()
	release()
	_ = activePeer.Close()
}

type delayedCloseConn struct {
	net.Conn
	closeStarted chan struct{}
	allowClose   chan struct{}
	closed       chan struct{}
	closeOnce    sync.Once
}

func (c *delayedCloseConn) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.closeStarted)
		<-c.allowClose
		closeErr = c.Conn.Close()
		close(c.closed)
	})
	return closeErr
}
