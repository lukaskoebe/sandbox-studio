//go:build !windows

package launcher

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
)

// StartServer starts studio in a session of its own, so that it outlives the app, and
// appends its log to logs/studio.log in the data directory.
func StartServer(studio, addr string) (<-chan error, error) {
	cmd := exec.Command(studio, "-addr", addr, "-no-browser")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if p, err := paths.Default(); err == nil {
		dir := filepath.Join(p.Data, "logs")
		if err := os.MkdirAll(dir, 0o700); err == nil {
			if f, err := os.OpenFile(filepath.Join(dir, "studio.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				defer f.Close() // the child has its own copy
				cmd.Stdout, cmd.Stderr = f, f
			}
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

// RunStudio runs a short studio command and returns its output.
func RunStudio(ctx context.Context, studio string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, studio, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(append(out.Bytes(), errOut.Bytes()...))
		if len(msg) > 0 {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return out.String(), nil
}
