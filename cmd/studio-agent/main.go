// Command studio-agent runs inside every sandbox and connects back to Studio over vsock.
package main

import (
	"context"
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
	default:
		fmt.Fprintf(os.Stderr, "usage: studio-agent [connect|version]\n")
		os.Exit(2)
	}
}
