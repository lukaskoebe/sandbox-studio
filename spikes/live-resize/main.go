// Command live-resize checks whether msb resizes a running VM's CPUs and memory live when the
// VM boots with headroom: a CPU maximum above its count and a memory ceiling above its size.
// Point MSB_HOME at a scratch directory; it boots a small Alpine VM, prints what the guest
// sees after each change, and removes the VM. "live-resize cost" instead boots one VM without
// headroom and one with, one after the other, and prints what each costs the host.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const vm = "liveresize-a"

func main() {
	log.SetFlags(log.Ltime)
	if os.Getenv("MSB_HOME") == "" {
		log.Fatal("set MSB_HOME to a scratch directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{}); err != nil {
		log.Fatal("install msb: ", err)
	}
	if len(os.Args) > 1 && os.Args[1] == "cost" {
		for _, c := range [][2]int{{1, 512}, {16, 512}, {1, 8192}, {4, 512}, {1, 1024}, {4, 1024}} {
			cost(ctx, uint8(c[0]), uint32(c[1]))
		}
		return
	}
	_ = msb.RemoveSandbox(ctx, vm)
	sb, err := msb.CreateSandbox(ctx, vm,
		msb.WithImage("alpine:3.20"),
		msb.WithCPUs(1), msb.WithMaxCPUs(4),
		msb.WithMemory(512), msb.WithMaxMemory(1536),
		msb.WithDetached())
	if err != nil {
		log.Fatal("create: ", err)
	}
	defer func() {
		h, err := msb.GetSandbox(context.Background(), vm)
		if err == nil {
			_ = h.Stop(context.Background())
		}
		if err := msb.RemoveSandbox(context.Background(), vm); err != nil {
			log.Print("remove: ", err)
		}
	}()
	guest := func(what string) {
		out, err := sb.Shell(ctx, "echo cpus=$(nproc) online=$(cat /sys/devices/system/cpu/online); grep MemTotal /proc/meminfo; cat /proc/sys/kernel/random/boot_id")
		if err != nil {
			log.Fatal(what, ": exec: ", err)
		}
		log.Printf("%s: %s", what, strings.Join(strings.Fields(out.Stdout()), " "))
	}
	guest("booted 1 CPU (max 4), 512 MiB (ceiling 1536)")

	h, err := msb.GetSandbox(ctx, vm)
	if err != nil {
		log.Fatal(err)
	}
	for _, step := range []struct {
		cpus   uint8
		memory uint32
	}{{3, 1024}, {2, 768}, {4, 1536}, {6, 2048}} {
		plan, err := h.Modify(ctx, msb.ModifyOptions{CPUs: step.cpus, MemoryMiB: step.memory, DryRun: true})
		if err != nil {
			log.Fatal("dry run: ", err)
		}
		b, _ := json.Marshal(plan)
		log.Printf("plan %d CPUs %d MiB: %s", step.cpus, step.memory, b)
		live := msb.ModifyOptions{Policy: msb.ModificationPolicyNoRestart}
		for _, c := range plan.Changes {
			if c.Disposition != "live" {
				continue
			}
			switch c.Field {
			case "cpus":
				live.CPUs = step.cpus
			case "memory":
				live.MemoryMiB = step.memory
			}
		}
		if live.CPUs == 0 && live.MemoryMiB == 0 {
			log.Print("nothing live")
			continue
		}
		res, err := h.Modify(ctx, live)
		if err != nil {
			log.Print("apply: ", err)
			continue
		}
		b, _ = json.Marshal(res.ResizeStatus)
		log.Printf("applied: %s", b)
		time.Sleep(3 * time.Second)
		guest(fmt.Sprintf("after %d CPUs %d MiB", step.cpus, step.memory))
	}
}

// cost boots a 1-CPU, 512 MiB VM with the given boot capacity and prints its host memory and
// threads once it has settled.
func cost(ctx context.Context, maxCPUs uint8, ceiling uint32) {
	_ = msb.RemoveSandbox(ctx, vm)
	sb, err := msb.CreateSandbox(ctx, vm, msb.WithImage("alpine:3.20"),
		msb.WithCPUs(1), msb.WithMaxCPUs(maxCPUs), msb.WithMemory(512), msb.WithMaxMemory(ceiling), msb.WithDetached())
	if err != nil {
		log.Fatal("create: ", err)
	}
	defer func() {
		if h, err := msb.GetSandbox(context.Background(), vm); err == nil {
			_ = h.Stop(context.Background())
		}
		_ = msb.RemoveSandbox(context.Background(), vm)
	}()
	if _, err := sb.Shell(ctx, "true"); err != nil {
		log.Fatal(err)
	}
	time.Sleep(15 * time.Second)
	pidOut, _ := exec.Command("pgrep", "-f", "liveresize-a").Output()
	ticks := func() (n uint64) {
		for _, pid := range strings.Fields(string(pidOut)) {
			st, err := os.ReadFile("/proc/" + pid + "/stat")
			if err != nil || !strings.Contains(string(st), "(libkrun VM)") {
				continue
			}
			f := strings.Fields(string(st)[strings.LastIndex(string(st), ")")+2:])
			var u, k uint64
			fmt.Sscan(f[11], &u)
			fmt.Sscan(f[12], &k)
			n += u + k
		}
		return n
	}
	t0 := ticks()
	time.Sleep(20 * time.Second)
	log.Printf("idle VM process CPU over 20 s: %.1f%% of one core", float64(ticks()-t0)/20)
	h, err := msb.GetSandbox(ctx, vm)
	if err != nil {
		log.Fatal(err)
	}
	m, err := h.Metrics(ctx)
	if err != nil {
		log.Fatal(err)
	}
	rss := uint64(0)
	if m.MemoryHostResidentBytes != nil {
		rss = *m.MemoryHostResidentBytes
	}
	out, _ := sb.Shell(ctx, "grep MemTotal /proc/meminfo; cat /sys/devices/system/cpu/online /sys/devices/system/cpu/possible")
	log.Printf("max %d CPUs, ceiling %d MiB: host resident %d MiB, guest uses %d MiB, cpu %.1f%%; guest %s",
		maxCPUs, ceiling, rss>>20, m.MemoryBytes>>20, m.CPUPercent, strings.Join(strings.Fields(out.Stdout()), " "))
	pids, _ := exec.Command("pgrep", "-f", "liveresize-a").Output()
	for _, pid := range strings.Fields(string(pids)) {
		st, _ := os.ReadFile("/proc/" + pid + "/status")
		for _, line := range strings.Split(string(st), "\n") {
			if strings.HasPrefix(line, "Threads:") || strings.HasPrefix(line, "VmRSS:") || strings.HasPrefix(line, "Name:") {
				log.Printf("  pid %s %s", pid, strings.Join(strings.Fields(line), " "))
			}
		}
	}
}
