//go:build !linux

package main

import (
	"context"
	"errors"
	"io"
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

func exportLayer(context.Context, io.Writer) error {
	return errors.New("studio-agent export-layer only runs inside Linux sandboxes")
}

func captureWatchdog() error {
	return errors.New("studio-agent capture watchdog only runs inside Linux sandboxes")
}

func workspaceExport(io.Writer, io.Writer) error {
	return errors.New("studio-agent workspace-export only runs inside Linux sandboxes")
}

func workspaceImport(io.Reader) error {
	return errors.New("studio-agent workspace-import only runs inside Linux sandboxes")
}
