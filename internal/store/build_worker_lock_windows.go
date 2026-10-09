//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func tryBuildWorkerFileLock(path string) (func() error, error) {
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	securityDescriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;OW)")
	if err != nil {
		return nil, fmt.Errorf("prepare lock file permissions: %w", err)
	}
	securityAttributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: securityDescriptor,
	}
	handle, err := windows.CreateFile(pathp, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, securityAttributes, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("stat lock file: %w", err)
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_DEVICE) != 0 {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("build worker lock path is not a regular file")
	}
	dacl, _, err := securityDescriptor.DACL()
	if err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("read lock file permissions: %w", err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("set lock file permissions: %w", err)
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("create lock file handle")
	}
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped); err != nil {
		return nil, errors.Join(err, file.Close())
	}

	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
			releaseErr = errors.Join(unlockErr, file.Close())
		})
		return releaseErr
	}, nil
}

func isBuildWorkerFileLockConflict(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
