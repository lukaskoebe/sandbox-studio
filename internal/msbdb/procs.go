package msbdb

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// VM is a VM process msb's runtime runs.
type VM struct {
	PID  int
	Name string
}

// RunningVMs lists the msb VM processes whose sandbox name starts with prefix. A VM outlives
// Studio, so one can run while Studio repairs the database: it keeps the damaged file open
// and writes to it, and the rebuilt database doesn't know what it does after. On a system
// where processes can't be listed it returns none.
func RunningVMs(prefix string) []VM {
	var vms []VM
	for pid, args := range processArgs() {
		if name, ok := vmName(args); ok && strings.HasPrefix(name, prefix) {
			vms = append(vms, VM{PID: pid, Name: name})
		}
	}
	return vms
}

// vmName is the sandbox name of an msb VM process's argv: `msb machine … --name <name> …`.
func vmName(args []string) (string, bool) {
	machine := false
	for i, a := range args {
		if a == "machine" {
			machine = true
		}
		if machine && a == "--name" && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// Holders lists the processes other than this one that have the catalog at path, or its -wal
// or -shm file, open: msb's VMs, an `msb` command, Studio's SSH proxies or another Studio.
// Moving the files aside under them is what damages a catalog: SQLite in such a process, when
// it closes the files it renamed, deletes the -wal and -shm files at their old paths, which by
// then belong to the new catalog. On a system where open files can't be listed it returns none.
func Holders(path string) []int {
	var files []string
	for _, s := range suffixes {
		f := path + s
		// The kernel names open files by their real path.
		if dir, err := filepath.EvalSymlinks(filepath.Dir(f)); err == nil {
			f = filepath.Join(dir, filepath.Base(f))
		}
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil
	}
	pids := openBy(files)
	slices.Sort(pids)
	pids = slices.Compact(pids)
	return slices.DeleteFunc(pids, func(pid int) bool { return pid == os.Getpid() })
}
