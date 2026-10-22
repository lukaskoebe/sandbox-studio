//go:build !windows

package updates

import (
	"os/exec"
	"syscall"
)

// detach starts the process in a session of its own, so the terminal's hangup doesn't end it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func detachRetry(*exec.Cmd) *exec.Cmd { return nil }
