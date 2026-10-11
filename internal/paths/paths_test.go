package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureGuestTraversable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	p := Paths{Data: filepath.Join(t.TempDir(), "data")}
	// An install from before the fix, with a private guest directory.
	if err := os.MkdirAll(p.Guest(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]os.FileMode{p.Data: 0o700, p.Guest(): 0o755} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", dir, got, want)
		}
	}
}
