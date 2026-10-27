//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a session of its own, so that it outlives the terminal it came from.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
