package templateregistry

import (
	"os"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
)

// TestMain gives the received layers a disk with room: the receiver keeps a floor of free
// space, and a machine with less than that free would otherwise fail every publish.
func TestMain(m *testing.M) {
	templateexport.FreeSpace = func(string) (uint64, error) { return 1 << 40, nil }
	os.Exit(m.Run())
}
