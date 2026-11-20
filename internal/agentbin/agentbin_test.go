package agentbin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallWritesTheLoginShellEvenWithoutAnAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes")
	}
	dir := t.TempDir()
	// No agent is embedded for this architecture, which fails after the login shell is written.
	_ = Install(dir, "none")
	info, err := os.Stat(filepath.Join(dir, "bin", "login-shell"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

// windowsRename refuses, as Windows does, to replace dst while a sandbox holds it open, and
// to move it aside unless shareDelete.
func windowsRename(t *testing.T, held string, shareDelete bool) {
	t.Helper()
	t.Cleanup(func() { rename = os.Rename })
	rename = func(from, to string) error {
		if to == held {
			if _, err := os.Stat(held); err == nil {
				return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("Access is denied.")}
			}
		}
		if from == held && !shareDelete {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("Access is denied.")}
		}
		return os.Rename(from, to)
	}
}

func TestInstallLeavesAnAgentInUse(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "bin", "studio-agent")
	if err := install(dst, []byte("old")); err != nil {
		t.Fatal(err)
	}
	// Even where it could be moved aside: the running VM would mix the two.
	windowsRename(t, dst, true)
	err := install(dst, []byte("new"))
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "old" {
		t.Fatalf("agent = %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), "*")); len(left) != 1 {
		t.Fatalf("files = %v", left)
	}
}

func TestLoginShellIsValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cmd := exec.Command(sh, "-n")
	cmd.Stdin = strings.NewReader(loginShell)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
