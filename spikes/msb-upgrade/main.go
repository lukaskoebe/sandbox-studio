// Command msb-upgrade checks that Studio's start-up upgrade of msb (runtime.Ensure) keeps
// sandboxes working: a VM that an older msb runs keeps running and answering, it stops and
// starts on the new msb, and checkpoints taken on either side still work.
//
// Build it twice, against the old and the new SDK, and point MSB_HOME at a scratch directory:
//
//	msb-upgrade before <image> <guest dir>   # old SDK: install msb, boot a VM, checkpoint it
//	msb-upgrade after <image> <guest dir>    # new SDK: upgrade msb, then use that VM
//	msb-upgrade clean                        # remove the VM and its checkpoints
//
// <guest dir> holds bin/studio-agent, as Studio's agent directory does.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	vm      = "msbup-a"
	sandbox = "msbupsandbox1"
)

var egress = runtime.Egress{Nameserver: "127.0.0.1:53", Proxy: "127.0.0.1:1080", User: "msbup", PasswordEnv: "MSBUP_PASSWORD"}

func main() {
	log.SetFlags(log.Ltime)
	if os.Getenv("MSB_HOME") == "" {
		log.Fatal("set MSB_HOME to a scratch directory")
	}
	if len(os.Args) < 2 {
		log.Fatal("usage: msb-upgrade before|after|clean [image guest-dir]")
	}
	os.Setenv(egress.PasswordEnv, "unused")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	switch os.Args[1] {
	case "before":
		before(ctx, runtime.New(runtime.Options{Image: os.Args[2], GuestDir: os.Args[3]}))
	case "after":
		after(ctx, runtime.New(runtime.Options{Image: os.Args[2], GuestDir: os.Args[3]}))
	case "clean":
		clean(ctx, runtime.New(runtime.Options{}))
	}
}

func before(ctx context.Context, rt *runtime.Runtime) {
	if _, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{}); err != nil {
		log.Fatal(err)
	}
	log.Printf("SDK %s, %s", msb.SDKVersion(), msbVersion())
	spec := runtime.Spec{CPUs: 1, MemoryMiB: 1024, WorkspaceMiB: 1024, DockerMiB: 1024, HomeMiB: 512, Egress: egress}
	step("create and boot", func() error {
		return rt.Create(ctx, vm, spec, os.Getenv("MSB_HOME")+"/agent.sock", map[string]string{"studio.sandbox-name": "msbup"})
	})
	step("write a marker", func() error { return sh(ctx, vm, "echo before > /workspace/marker && sync") })
	step("checkpoint while running", func() error { return rt.CreateCheckpoint(ctx, vm, sandbox, "cp-before") })
	status(ctx, rt)
}

func after(ctx context.Context, rt *runtime.Runtime) {
	log.Printf("SDK %s, before the upgrade %s", msb.SDKVersion(), msbVersion())
	step("ensure the runtime", func() error { return ensure(ctx) })
	log.Printf("now %s", msbVersion())
	status(ctx, rt)
	step("read the marker in the VM the old msb runs", func() error { return expect(ctx, vm, "cat /workspace/marker", "before") })
	step("checkpoint the old msb's VM while running", func() error { return rt.CreateCheckpoint(ctx, vm, sandbox, "cp-running") })
	step("stop it", func() error { return rt.Stop(ctx, vm) })
	status(ctx, rt)
	step("checkpoint it stopped", func() error { return rt.CreateCheckpoint(ctx, vm, sandbox, "cp-stopped") })
	step("start it on the new msb", func() error { return rt.Start(ctx, vm) })
	status(ctx, rt)
	step("read the marker", func() error { return expect(ctx, vm, "cat /workspace/marker", "before") })
	step("write and read the workspace", func() error {
		return expect(ctx, vm, "echo after >> /workspace/marker && sync && tail -1 /workspace/marker", "after")
	})
	step("checkpoint on the new msb", func() error { return rt.CreateCheckpoint(ctx, vm, sandbox, "cp-after") })
	snaps, err := msb.Snapshot.List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range snaps {
		log.Printf("snapshot %s", s.Reference())
	}
	step("stop it again", func() error { return rt.Stop(ctx, vm) })
}

func clean(ctx context.Context, rt *runtime.Runtime) {
	for _, cp := range []string{"cp-after", "cp-stopped", "cp-running", "cp-before"} {
		if err := rt.RemoveCheckpoint(ctx, sandbox, cp); err != nil {
			log.Printf("remove %s: %v", cp, err)
		}
	}
	if err := rt.Remove(ctx, vm); err != nil {
		log.Printf("remove %s: %v", vm, err)
	}
}

func step(name string, f func() error) {
	start := time.Now()
	if err := f(); err != nil {
		log.Fatalf("FAIL %s: %v", name, err)
	}
	log.Printf("ok   %s (%s)", name, time.Since(start).Round(100*time.Millisecond))
}

func status(ctx context.Context, rt *runtime.Runtime) {
	s, err := rt.Status(ctx, vm)
	log.Printf("     %s is %s %v", vm, s, err)
}

func sh(ctx context.Context, name, script string) error {
	_, err := run(ctx, name, script)
	return err
}

func expect(ctx context.Context, name, script, want string) error {
	out, err := run(ctx, name, script)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != want {
		return fmt.Errorf("%q printed %q, not %q", script, out, want)
	}
	return nil
}

func run(ctx context.Context, name, script string) (string, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return "", err
	}
	sb, err := h.Connect(ctx)
	if err != nil {
		return "", err
	}
	defer sb.Detach(ctx)
	out, err := sb.Exec(ctx, "sh", []string{"-c", script}, msb.WithExecUser("root"), msb.WithExecTimeout(60*time.Second))
	if err != nil {
		return "", err
	}
	if !out.Success() {
		return "", fmt.Errorf("exit %d: %s", out.ExitCode(), out.Stderr())
	}
	return out.Stdout(), nil
}

func msbVersion() string {
	r, err := msb.ResolveRuntime(msb.RuntimeConfig{})
	if err != nil {
		return "no msb: " + err.Error()
	}
	out, err := exec.Command(r.MSBPath, "--version").Output()
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(out))
}
