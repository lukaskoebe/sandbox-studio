//go:build !linux && !darwin && !windows

package diskspace

import "errors"

func Free(string) (uint64, error) {
	return 0, errors.New("free disk space is unknown on this platform")
}
