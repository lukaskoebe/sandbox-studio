package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAppBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "studio")
	if got := appBinary(exe, "linux"); got != "" {
		t.Fatalf("no app installed: %q", got)
	}
	bundled := filepath.Join(dir, "Sandbox Studio.app", "Contents", "MacOS", "studio-app")
	if err := os.MkdirAll(filepath.Dir(bundled), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundled, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := appBinary(exe, "darwin"); got != bundled {
		t.Fatalf("macOS bundle: %q", got)
	}
	if got := appBinary(exe, "linux"); got != "" {
		t.Fatalf("a bundle counts only on macOS: %q", got)
	}
	plain := filepath.Join(dir, "studio-app.exe")
	if err := os.WriteFile(plain, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := appBinary(filepath.Join(dir, "studio.exe"), "windows"); got != plain {
		t.Fatalf("windows: %q", got)
	}
}

func TestStartAppReportsAnAppThatCantStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake app is a shell script")
	}
	t.Setenv("SANDBOX_STUDIO_HOME", t.TempDir())
	app := filepath.Join(t.TempDir(), "studio-app")
	script := "#!/bin/sh\necho 'error while loading shared libraries: libwebkit2gtk-4.1.so.0' >&2\nexit 127\n"
	if err := os.WriteFile(app, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := startApp(app, "studio", "127.0.0.1:7878")
	if err == nil || !strings.Contains(err.Error(), "libwebkit2gtk") {
		t.Fatalf("startApp = %v; want the loader's message", err)
	}
}

func TestAppLibHint(t *testing.T) {
	missing := errors.New("exit status 127: studio-app: error while loading shared libraries: libX11.so.6: cannot open shared object file: No such file or directory")
	if got := appLibHint(missing, true); !strings.Contains(got, "programs.nix-ld.libraries") || !strings.Contains(got, "pkgs.xorg.libX11") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("NixOS: %q", got)
	}
	if got := appLibHint(missing, false); !strings.Contains(got, "libwebkit2gtk-4.1-0") {
		t.Fatalf("elsewhere: %q", got)
	}
	if got := appLibHint(errors.New("exit status 1: no window"), true); got != "" {
		t.Fatalf("another failure: %q", got)
	}
}
