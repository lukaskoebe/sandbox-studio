//go:build windows

package sandboxes

import "golang.org/x/sys/windows"

// diskFree returns the bytes the calling user may still write under dir.
func diskFree(dir string) (uint64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
