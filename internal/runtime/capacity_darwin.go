package runtime

import "golang.org/x/sys/unix"

func hostMemoryMiB() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n >> 20
}
