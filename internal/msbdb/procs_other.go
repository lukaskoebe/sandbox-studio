//go:build !linux && !darwin

package msbdb

// processArgs can't list processes here.
func processArgs() map[int][]string { return nil }
