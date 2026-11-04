//go:build !windows

package main

import (
	"io/fs"
	"syscall"
)

// diskUsage is what a file takes on the disk: a sandbox's disk is a sparse file far larger than that.
func diskUsage(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return info.Size()
}
