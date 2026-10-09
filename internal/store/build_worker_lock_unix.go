//go:build linux || darwin

package store

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

func tryBuildWorkerFileLock(path string) (func() error, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("stat lock file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, fmt.Errorf("build worker lock path is not a regular file")
	}
	if err := unix.Fchmod(fd, 0600); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("set lock file permissions: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("create lock file handle")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(err, file.Close())
	}

	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			unlockErr := unix.Flock(fd, unix.LOCK_UN)
			releaseErr = errors.Join(unlockErr, file.Close())
		})
		return releaseErr
	}, nil
}

func isBuildWorkerFileLockConflict(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
