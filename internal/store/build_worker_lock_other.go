//go:build !linux && !darwin && !windows

package store

import "fmt"

func tryBuildWorkerFileLock(string) (func() error, error) {
	return nil, fmt.Errorf("file-backed build worker locks are unsupported on this platform")
}

func isBuildWorkerFileLockConflict(error) bool { return false }
