package msbdb

import (
	"os/exec"
	"testing"
	"time"
)

func TestRunningVMsFindsAnMSBMachineProcess(t *testing.T) {
	// A shell whose argv looks like an msb VM's; the second command keeps it from exec'ing.
	cmd := exec.Command("sh", "-c", "sleep 30; :", "machine", "--name", "studio-proctest-g1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
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
