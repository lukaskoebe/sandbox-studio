package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// Options are what the terminal UI needs to reach a running Studio.
type Options struct {
	Addr    string // the loopback address of the running Studio, such as 127.0.0.1:7878
	Control string // the control credential of this installation (webauth.ControlToken)
}

// Run shows the terminal UI until the user quits, then ends its session. It returns an error
// wrapping ErrNotRunning when no Studio answers at opts.Addr.
func Run(ctx context.Context, opts Options) error {
	client, err := Connect(ctx, opts.Addr, opts.Control)
	if err != nil {
		return err
	}
	defer client.Close(context.WithoutCancel(ctx))
	_, err = tea.NewProgram(newApp(ctx, client, opts.Addr), tea.WithContext(ctx)).Run()
	return err
}
