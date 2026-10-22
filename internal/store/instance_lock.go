package store

import (
	"errors"
	"fmt"
)

// ErrLocked reports that another process holds a lock file.
var ErrLocked = errors.New("another process holds the lock")

// TryLock takes the lock file at path without waiting, for one Studio per data directory.
// The lock lasts until release is called or the process exits.
func TryLock(path string) (release func() error, err error) {
	release, err = tryBuildWorkerFileLock(path)
	if err != nil {
		if isBuildWorkerFileLockConflict(err) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return onceBuildWorkerRelease(release), nil
}
