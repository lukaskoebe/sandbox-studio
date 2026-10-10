package diskspace

import "testing"

func TestFree(t *testing.T) {
	n, err := Free(t.TempDir())
	if err != nil {
		t.Skip("free disk space is unknown on this platform:", err)
	}
	if n == 0 {
		t.Fatal("a writable temp dir reports no free space")
	}
}
