//go:build unix && !darwin

package msbdb

// aclGrants has no access lists to read here: msb checks them only on macOS.
func aclGrants(string) string { return "" }
