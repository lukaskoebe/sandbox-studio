package launcher

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup  = 0x00000200
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// StartServer starts studio.exe with a console of its own that has no window: the programs
// Studio runs share that console instead of each opening a window. It runs outside the
// caller's job, so that it outlives the tray or the app. Studio logs to its own file there.
func StartServer(studio, addr string) (<-chan error, error) {
	cmd := exec.Command(studio, "-addr", addr, "-no-browser")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup | createBreakawayFromJob, HideWindow: true}
	if err := cmd.Start(); err != nil {
		// A job that forbids breakaway.
		cmd = exec.Command(studio, "-addr", addr, "-no-browser")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup, HideWindow: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

// RunStudio runs a short studio.exe command without a window and returns its output.
func RunStudio(ctx context.Context, studio string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, studio, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
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
