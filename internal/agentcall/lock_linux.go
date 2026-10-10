//go:build linux

package agentcall

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// lockFile takes an exclusive flock on f, polling until ctx ends.
func lockFile(ctx context.Context, f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
