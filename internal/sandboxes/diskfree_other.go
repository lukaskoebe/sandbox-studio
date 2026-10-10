//go:build !linux && !darwin && !windows

package sandboxes

import "errors"

func diskFree(string) (uint64, error) {
	return 0, errors.New("free disk space is unknown on this platform")
}
