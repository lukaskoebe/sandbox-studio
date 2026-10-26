//go:build !linux && !darwin

package runtime

func hostMemoryMiB() uint64 { return 0 }
