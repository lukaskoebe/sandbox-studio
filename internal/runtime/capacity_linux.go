package runtime

import "golang.org/x/sys/unix"

func hostMemoryMiB() uint64 {
	var info unix.Sysinfo_t
	if unix.Sysinfo(&info) != nil {
		return 0
	}
	return uint64(info.Totalram) * uint64(info.Unit) >> 20
}
