package main

import (
	"context"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/tui"
)

// runTUI is `studio tui`: the terminal client of the running Studio, reached on its loopback
// address the way `login-url` reaches it (docs/tui.md).
func runTUI(ctx context.Context, addr string) error {
	control, err := controlCredential(ctx)
	if err != nil {
		return err
	}
	client, err := tui.Connect(ctx, addr, control)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	envs, err := client.Environments(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("connected to Studio at %s (%d environments)\n", addr, len(envs))
	return nil
}
