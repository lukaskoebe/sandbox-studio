package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/guest"
	"github.com/lukaskoebe/sandbox-studio/internal/guestcapture"
)

func connect(ctx context.Context, log *slog.Logger) error {
	a, err := guest.New(log)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

func boot() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return guest.Boot(self)
}

const shutdownTimeout = 65 * time.Second // three 20-second service stops plus the guest margin

func shutdown() error { return guest.Shutdown(shutdownTimeout) }

// supervise runs the supervisor boot started; it inherits the supervisor lock as fd 3.
func supervise(ctx context.Context, log *slog.Logger) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return guest.Supervise(ctx, log, self, os.NewFile(3, "supervisor.lock"))
}

func ociRuntime(args []string) error { return guest.OCIRuntime(args) }

func exportLayer(ctx context.Context, dst io.Writer, maxFreeze time.Duration) error {
	return guestcapture.Export(ctx, dst, maxFreeze)
}

func captureWatchdog(maxFreeze string) error { return guestcapture.RunWatchdog(maxFreeze) }
