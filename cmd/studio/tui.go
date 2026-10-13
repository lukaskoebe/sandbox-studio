package main

import (
	"context"
	"errors"
	"os"

	"golang.org/x/term"

	"github.com/lukaskoebe/sandbox-studio/internal/tui"
)

// runTUI is `studio tui`: the terminal client of the running Studio, reached on its loopback
// address the way `login-url` reaches it (docs/tui.md).
func runTUI(ctx context.Context, addr string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("studio tui needs an interactive terminal")
	}
	control, err := controlCredential(ctx)
	if err != nil {
		return err
	}
	return tui.Run(ctx, tui.Options{Addr: addr, Control: control})
}

// connectCLI signs the command line in to the Studio at addr as the local owner, as the TUI does.
func connectCLI(ctx context.Context, addr string) (*tui.Client, error) {
	control, err := controlCredential(ctx)
	if err != nil {
		return nil, err
	}
	return tui.Connect(ctx, addr, control)
}
