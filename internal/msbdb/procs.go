package msbdb

import "strings"

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
