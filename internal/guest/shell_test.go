//go:build linux

package guest

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTerminalShell(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	zsh := filepath.Join(bin, "zsh")
	old := zshPaths
	t.Cleanup(func() { zshPaths = old })
	zshPaths = []string{filepath.Join(bin, "missing"), zsh}
	a := &Agent{log: slog.New(slog.NewTextHandler(io.Discard, nil)), user: &account{uid: uint32(os.Getuid()), gid: uint32(os.Getgid()), home: home}}
	ask := func(s string) { a.shell.Store(&s) }

	if got := a.terminalShell(); got != "/bin/bash" {
		t.Fatalf("before any config: %s", got)
	}
	ask("zsh")
	if got := a.terminalShell(); got != "/bin/bash" {
		t.Fatalf("zsh asked for but missing: %s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
		t.Fatal("a ~/.zshrc was written for bash")
	}
	if err := os.WriteFile(zsh, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := a.terminalShell(); got != zsh {
		t.Fatalf("zsh: %s", got)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".zshrc")); err != nil || string(b) != zshrc {
		t.Fatalf("~/.zshrc: %q %v", b, err)
	}
	// The user's own file stays as it is.
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("PROMPT='> '\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.terminalShell()
	if b, _ := os.ReadFile(filepath.Join(home, ".zshrc")); string(b) != "PROMPT='> '\n" {
		t.Fatalf("~/.zshrc was replaced: %q", b)
	}
	ask("bash")
	if got := a.terminalShell(); got != "/bin/bash" {
		t.Fatalf("back to bash: %s", got)
	}
}

func TestEnsureZshrcLeavesOtherStartupFiles(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".zprofile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{log: slog.New(slog.NewTextHandler(io.Discard, nil)), user: &account{uid: uint32(os.Getuid()), gid: uint32(os.Getgid()), home: home}}
	a.ensureZshrc()
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
		t.Fatal("a ~/.zshrc was written next to ~/.zprofile")
	}
}

func TestTerminalCommandSetsTheShellBeforeAttaching(t *testing.T) {
	args := terminalCommand("main", "/usr/bin/zsh")
	set := slices.Index(args, "default-shell")
	attach := slices.Index(args, "new-session")
	if set < 0 || attach < set || args[set+1] != "/usr/bin/zsh" || !slices.Equal(args[attach:], []string{"new-session", "-A", "-s", "main"}) {
		t.Fatalf("args %q", args)
	}
}
