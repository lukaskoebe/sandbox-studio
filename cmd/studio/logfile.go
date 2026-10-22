package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// maxLogSize is the size at which studio.log moves to studio.log.1 on start.
const maxLogSize = 10 << 20

// fileLogger logs everything to dir/studio.log and only warnings and errors to console, for
// a Studio whose console is a window the user would rather leave alone (Windows). A log over
// maxLogSize moves aside first, replacing the one before it.
func fileLogger(dir string, console io.Writer) (*slog.Logger, string, func() error, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", nil, err
	}
	path := filepath.Join(dir, "studio.log")
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogSize {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, "", nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", nil, err
	}
	log := slog.New(slog.NewMultiHandler(
		slog.NewTextHandler(f, nil),
		slog.NewTextHandler(console, &slog.HandlerOptions{Level: slog.LevelWarn}),
	))
	return log, path, f.Close, nil
}
