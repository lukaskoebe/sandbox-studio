package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestAgentSocketIsANamedPipeOnWindows(t *testing.T) {
	a := Paths{Data: `C:\Users\a\AppData\Local\Sandbox Studio`}.AgentSocket("sb1")
	b := Paths{Data: `D:\studio`}.AgentSocket("sb1")
	if runtime.GOOS != "windows" {
		if filepath.Base(a) != "sb1.sock" {
			t.Fatalf("socket %q", a)
		}
		return
	}
	// msb accepts only \\.\pipe\<name> with no empty, "." or ".." parts.
	if !strings.HasPrefix(a, `\\.\pipe\sandbox-studio-`) || !strings.HasSuffix(a, "-sb1") || strings.ContainsAny(a[len(`\\.\pipe\`):], `\/ `) || a == b {
		t.Fatalf("pipes %q and %q", a, b)
	}
}
