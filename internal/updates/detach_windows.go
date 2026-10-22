package updates

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// detach starts the process without a console and outside the job of whatever started
// Studio, so it outlives this process.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup | createBreakawayFromJob, HideWindow: true}
}

// detachRetry is the same start without leaving the job, for a job that forbids breakaway.
func detachRetry(cmd *exec.Cmd) *exec.Cmd {
	retry := exec.Command(cmd.Path, cmd.Args[1:]...)
	retry.Stdout, retry.Stderr = cmd.Stdout, cmd.Stderr
	retry.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup, HideWindow: true}
	return retry
}
