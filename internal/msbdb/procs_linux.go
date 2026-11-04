package msbdb

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// processArgs reads every process's argv from /proc.
func processArgs() map[int][]string {
	out := map[int][]string{}
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(d, "cmdline"))
		if err != nil || len(b) == 0 {
			continue
		}
		out[pid] = strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	}
	return out
}

// openBy lists the processes with one of files open, from their /proc/<pid>/fd links. Only
// the user's own processes can be read, which msb's are.
func openBy(files []string) []int {
	var pids []int
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(d, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if to, err := os.Readlink(filepath.Join(d, "fd", fd.Name())); err == nil && slices.Contains(files, to) {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}
