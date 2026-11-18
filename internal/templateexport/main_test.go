package templateexport

import (
	"os"
	"testing"
)

// TestMain gives the tests a disk with room, so the free space of the one they run on doesn't
// decide whether they pass; the tests of a full disk model it with modelDisk.
func TestMain(m *testing.M) {
	FreeSpace = func(string) (uint64, error) { return 1 << 40, nil }
	os.Exit(m.Run())
}
