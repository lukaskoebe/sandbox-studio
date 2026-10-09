// Command studio-agent runs inside every sandbox and connects back to Studio over vsock.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

func main() {
	cmd := "connect"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version":
		fmt.Println(version.Version)
	case "connect":
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := connect(ctx, log); err != nil && ctx.Err() == nil {
			log.Error("studio-agent stopped", "err", err)
			os.Exit(1)
		}
	case "export-layer":
		flags := flag.NewFlagSet("export-layer", flag.ExitOnError)
		maxFreeze := flags.Duration("max-freeze", 0, "longest time to hold the root filesystem frozen (default 60s)")
		_ = flags.Parse(os.Args[2:])
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := exportLayer(ctx, os.Stdout, *maxFreeze); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: export-layer: %v\n", err)
			os.Exit(1)
		}
	case "__capture-watchdog":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "studio-agent: capture watchdog: missing freeze limit")
			os.Exit(2)
		}
		if err := captureWatchdog(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: capture watchdog: %v\n", err)
			os.Exit(1)
		}
	case "boot":
		// Studio runs this after every VM boot: see guest.Boot.
		if err := boot(); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: %v\n", err)
			os.Exit(1)
		}
	case "shutdown":
		// Studio runs this before stopping the VM: see guest.Shutdown.
		if err := shutdown(); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: %v\n", err)
			os.Exit(1)
		}
	case "supervise":
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := supervise(ctx, log); err != nil {
			log.Error("supervisor stopped", "err", err)
			os.Exit(1)
		}
	case "oci-runtime":
		// Docker's runtime inside the sandbox: see guest.OCIRuntime.
		if err := ociRuntime(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: studio-agent [connect|boot|shutdown|version|oci-runtime|export-layer]\n")
		os.Exit(2)
	}
}
