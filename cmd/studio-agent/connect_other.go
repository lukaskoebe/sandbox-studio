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

func boot() error {
	return errors.New("studio-agent only runs inside Linux sandboxes")
}

func shutdown() error {
	return errors.New("studio-agent only runs inside Linux sandboxes")
}

func supervise(context.Context, *slog.Logger) error {
	return errors.New("studio-agent only runs inside Linux sandboxes")
}

func ociRuntime([]string) error {
	return errors.New("studio-agent only runs inside Linux sandboxes")
}
