//go:build !unix

package msbdb

// inspectDir has no ownership rule to apply here: msb checks Windows access lists itself.
func inspectDir(string) (why string, mine, private bool, err error) { return "", true, false, nil }

func makePrivate(string) (bool, error) { return false, nil }
