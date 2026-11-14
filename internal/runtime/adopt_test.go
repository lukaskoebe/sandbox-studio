package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLostDisksFindsMsbsDirectoryOrWhatARepairKeptAside(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MSB_HOME", home)
	if _, err := LostDisks("ss-a-g2"); !errors.Is(err, ErrNoDisks) {
		t.Fatalf("no directory: %v, want ErrNoDisks", err)
	}
	// A directory without owned disks is a leftover, not the VM's disks.
	dir := filepath.Join(home, "sandboxes", "ss-a-g2")
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LostDisks("ss-a-g2"); !errors.Is(err, ErrNoDisks) {
		t.Fatalf("a leftover: %v, want ErrNoDisks", err)
	}
	// What an interrupted repair kept aside, the newest first.
	for _, at := range []string{"20261113T100000Z", "20261114T100000Z"} {
		if err := os.MkdirAll(filepath.Join(home, adoptDir, "ss-a-g2."+at, "disks", "owned-volumes"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, adoptDir, "ss-a-g20.20261115T100000Z", "disks", "owned-volumes"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := LostDisks("ss-a-g2")
	if want := filepath.Join(home, adoptDir, "ss-a-g2.20261114T100000Z", "disks"); err != nil || got != want {
		t.Fatalf("kept aside: %q, %v; want %q", got, err, want)
	}
	// msb's own directory wins.
	if err := os.MkdirAll(filepath.Join(dir, "owned-volumes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := LostDisks("ss-a-g2"); err != nil || got != dir {
		t.Fatalf("msb's directory: %q, %v; want %q", got, err, dir)
	}
}
