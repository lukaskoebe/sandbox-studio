package main

import (
	"context"
	"log/slog"

	"github.com/lukaskoebe/sandbox-studio/internal/guest"
)

func connect(ctx context.Context, log *slog.Logger) error {
	a, err := guest.New(log)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

func ociRuntime(args []string) error { return guest.OCIRuntime(args) }
