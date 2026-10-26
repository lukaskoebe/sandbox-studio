//go:build !windows

package updates

import (
	"os"
	"syscall"
)

// inPlace is whether Studio restarts into an update by replacing its own process.
const inPlace = true

// execInPlace replaces this process with exe: it keeps the PID, the terminal and the signals,
// so Ctrl-C stops the new Studio as it did the old one. It returns only if the exec failed.
func execInPlace(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
}
