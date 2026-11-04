package msbdb

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRunningVMsFindsAnMSBMachineProcess(t *testing.T) {
	// A shell whose argv looks like an msb VM's; the second command keeps it from exec'ing.
	cmd := exec.Command("sh", "-c", "sleep 30; :", "machine", "--name", "studio-proctest-g1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, vm := range RunningVMs("studio-proctest-") {
			if vm.PID == cmd.Process.Pid && vm.Name == "studio-proctest-g1" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("RunningVMs = %v", RunningVMs("studio-proctest-"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestHoldersFindsAProcessWithTheCatalogOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "msb.db")
	for _, s := range []string{"", "-wal"} {
		if err := os.WriteFile(path+s, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// This process's own handles don't count.
	self, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer self.Close()
	if h := Holders(path); len(h) != 0 {
		t.Fatalf("Holders = %v, want none", h)
	}
	cmd := exec.Command("sh", "-c", "exec 3<\"$1\"; sleep 30", "sh", path+"-wal")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(Holders(path), cmd.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatalf("Holders = %v, want %d", Holders(path), cmd.Process.Pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
