// Command transfer-memory measures what a rebase's copy costs the host in memory. A copy streams
// a tree through the guest's page cache, which a VM's host process keeps resident once touched.
// Point MSB_HOME at a scratch directory; it boots one small Alpine VM with an owned disk, starts
// it again before each copy, runs the guest's export and import on it (see README.md), and
// prints the VM's host-resident memory as it goes.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	vm       = "transfermem-a"
	memory   = 2048
	ceiling  = 4096
	fileMiB  = 1200
	settling = 5 * time.Second
)

func main() {
	log.SetFlags(log.Ltime)
	if os.Getenv("MSB_HOME") == "" {
		log.Fatal("set MSB_HOME to a scratch directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{}); err != nil {
		log.Fatal("install msb: ", err)
	}
	_ = msb.RemoveSandbox(ctx, vm)
	sb, err := msb.CreateSandbox(ctx, vm,
		msb.WithImage("alpine:3.20"),
		msb.WithCPUs(1), msb.WithMemory(memory), msb.WithMaxMemory(ceiling),
		msb.WithMounts(map[string]msb.MountConfig{
			"/data":  msb.Mount.Owned(msb.OwnedVolumeOptions{Kind: msb.VolumeKindDisk, SizeMiB: 6144}),
			"/spike": msb.Mount.Bind(os.Getenv("SPIKE_BIN"), msb.MountOptions{Readonly: true}),
		}),
		msb.WithDetached())
	if err != nil {
		log.Fatal("create: ", err)
	}
	defer func() {
		if h, err := msb.GetSandbox(context.Background(), vm); err == nil {
			_ = h.Stop(context.Background())
		}
		if err := msb.RemoveSandbox(context.Background(), vm); err != nil {
			log.Print("remove: ", err)
		}
	}()
	h, err := msb.GetSandbox(ctx, vm)
	if err != nil {
		log.Fatal(err)
	}
	sh := func(cmd string) string {
		out, err := sb.Shell(ctx, cmd)
		if err != nil {
			log.Fatalf("%s: %v", cmd, err)
		}
		return strings.TrimSpace(out.Stdout() + out.Stderr())
	}
	report := func(what string) {
		time.Sleep(settling)
		m, err := h.Metrics(ctx)
		if err != nil {
			log.Fatal(err)
		}
		_ = m
		guest := sh("awk '/^(MemTotal|MemFree|Cached|Dirty):/ {printf \"%s %d \", $1, $2/1024}' /proc/meminfo")
		log.Printf("%-44s host RSS %5d MiB | guest MiB: %s", what, vmRSS()>>20, guest)
	}

	log.Print("kernel: ", sh("uname -r; ls -la /spike; mount | grep spike"))
	report("booted")

	// A tree like a workspace: one large file and many small ones.
	sh(fmt.Sprintf("mkdir -p /data/src/small && dd if=/dev/urandom of=/data/src/big bs=1M count=%d 2>/dev/null", fileMiB))
	sh("cd /data/src/small && i=0; while [ $i -lt 200 ]; do mkdir d$i; j=0; while [ $j -lt 100 ]; do head -c 32768 /dev/urandom > d$i/f$j; j=$((j+1)); done; i=$((i+1)); done")
	for _, bin := range []string{"guestcopy", "guestcopy-old"} {
		sh("rm -rf /data/dst; mkdir /data/dst; sync")
		// As in a rebase, the copy starts from a VM just started, with nothing in its cache.
		if err := h.Stop(ctx); err != nil {
			log.Fatal("stop: ", err)
		}
		if sb, err = msb.StartSandboxDetached(ctx, vm); err != nil {
			log.Fatal("start: ", err)
		}
		if h, err = msb.GetSandbox(ctx, vm); err != nil {
			log.Fatal(err)
		}
		report("before " + bin)
		done := make(chan struct{})
		peak := uint64(0)
		go func() {
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					peak = max(peak, vmRSS())
				}
			}
		}()
		began := time.Now()
		out := sh("/spike/" + bin + " /data/src /data/dst; echo rc=$?")
		close(done)
		log.Printf("%s took %s, peak host RSS %d MiB: %s", bin, time.Since(began).Round(100*time.Millisecond), peak>>20, out)
		report("after " + bin)
		log.Print(sh("du -sm /data/src /data/dst | tr '\\n' ' '; grep -E '^(Slab|SReclaimable):' /proc/meminfo | tr -s ' ' | tr '\\n' ' '"))
	}
}

// vmRSS sums the resident memory of the host processes of the VM: msb runs it in a process
// whose command line names it.
func vmRSS() (total uint64) {
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		cmd, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if err != nil || !strings.Contains(string(cmd), vm) {
			continue
		}
		st, _ := os.ReadFile("/proc/" + d.Name() + "/status")
		for _, line := range strings.Split(string(st), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "VmRSS:" {
				var kb uint64
				fmt.Sscan(f[1], &kb)
				total += kb << 10
			}
		}
	}
	return total
}
