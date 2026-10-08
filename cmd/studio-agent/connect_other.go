//go:build !linux

package main

import (
	"context"
	"errors"
	"log/slog"
)

func connect(context.Context, *slog.Logger) error {
	return errors.New("studio-agent only runs inside Linux sandboxes")
}
