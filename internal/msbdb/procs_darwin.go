package msbdb

import (
	"os/exec"
	"strconv"
	"strings"
)

// processArgs lists every process's command line with ps. Its words are split at spaces,
// which msb's VM arguments don't contain.
func processArgs() map[int][]string {
	out := map[int][]string{}
	b, err := exec.Command("ps", "-axww", "-o", "pid=,command=").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			out[pid] = fields[1:]
		}
	}
	return out
}
