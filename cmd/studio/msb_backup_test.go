//go:build !e2e

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
)

// The copies of msb's catalog are made by `studio msb-backup`, in a process of its own: see
// msbBackup.
func TestMSBBackupCopiesTheCatalogNextToIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MSB_HOME", home)
	if err := os.Mkdir(filepath.Join(home, "db"), 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := msbdb.Path()
	if err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, path, "ss-a-g1")

	stdout := captureStdout(t, func() {
		if code := runMSBBackup(nil); code != 0 {
			t.Fatalf("msb-backup exited %d", code)
		}
	})
	out := strings.TrimSpace(stdout)
	if !strings.HasPrefix(out, path+msbdb.BackupPrefix) {
		t.Fatalf("msb-backup printed %q, want a copy next to %s", out, path)
	}
	known, err := msbdb.Sandboxes(t.Context(), out)
	if err != nil || len(known) != 1 || known[0] != "ss-a-g1" {
		t.Fatalf("the copy knows %v (%v), want ss-a-g1", known, err)
	}
	if code := runMSBBackup([]string{"extra"}); code != 2 {
		t.Fatalf("msb-backup with an argument exited %d, want 2", code)
	}
}

// captureStdout returns what f printed to os.Stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	b := make([]byte, 1<<16)
	n, _ := r.Read(b)
	return string(b[:n])
}
