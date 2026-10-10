//go:build !linux && !darwin && !windows

package doctor

func hostVirtualization(*Probes) {}
