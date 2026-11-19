//go:build !windows

package main

// prepareConsole has nothing to do outside Windows.
func prepareConsole() {}

// handOffConsole has nothing to hand off outside Windows, where a terminal starts studio.
func handOffConsole(string) bool { return false }
