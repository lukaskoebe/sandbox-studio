package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

// A start stopped by a signal, or by another program on its address, doesn't roll an update
// back; any other error before serving does.
func TestRollsBack(t *testing.T) {
	signalled, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("interrupt signal received"))
	stopped, stop := context.WithCancel(context.Background())
	stop()
	inUse := fmt.Errorf("listen: %w", &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)})
	for _, c := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"a failed start", context.Background(), errors.New("open the database: disk I/O error"), true},
		{"a failed start Studio then stopped", stopped, errors.New("open the database: disk I/O error"), true},
		{"a signal", signalled, context.Canceled, false},
		{"its address taken", context.Background(), inUse, false},
		{"Windows's address taken", context.Background(), fmt.Errorf("listen: %w", syscall.Errno(10048)), false},
	} {
		if got := rollsBack(c.ctx, c.err); got != c.want {
			t.Errorf("%s: rolls back %v, want %v", c.name, got, c.want)
		}
	}
}
