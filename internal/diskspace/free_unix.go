//go:build linux || darwin

package diskspace

import "golang.org/x/sys/unix"

// Free returns the bytes an unprivileged process may still write under dir.
func Free(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
