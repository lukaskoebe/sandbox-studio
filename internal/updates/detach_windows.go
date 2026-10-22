package updates

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup  = 0x00000200
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// detach starts the process with a console of its own that has no window, and outside the
// job of whatever started Studio, so it outlives this process. Without a console at all
// (DETACHED_PROCESS), each console program Studio runs would open a window of its own.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup | createBreakawayFromJob, HideWindow: true}
}

// detachRetry is the same start without leaving the job, for a job that forbids breakaway.
func detachRetry(cmd *exec.Cmd) *exec.Cmd {
	retry := exec.Command(cmd.Path, cmd.Args[1:]...)
	retry.Stdout, retry.Stderr = cmd.Stdout, cmd.Stderr
	retry.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup, HideWindow: true}
	return retry
}
