package msbdb

import (
	"os"
	"path/filepath"
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
