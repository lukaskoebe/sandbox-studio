package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// ExportWorkspace streams the workspace archive of a running VM to w. It runs
// `studio-agent workspace-export` as root.
func (r *Runtime) ExportWorkspace(ctx context.Context, owned OwnedVM, w io.Writer) error {
	return r.runAgent(ctx, owned, "workspace-export", RunCommand{Stdout: w})
}

// ImportWorkspace extracts a workspace archive from rd into the empty workspace of a
// running VM. It runs `studio-agent workspace-import` as root.
func (r *Runtime) ImportWorkspace(ctx context.Context, owned OwnedVM, rd io.Reader) error {
	return r.runAgent(ctx, owned, "workspace-import", RunCommand{Stdin: rd})
}

func (r *Runtime) runAgent(ctx context.Context, owned OwnedVM, subcommand string, command RunCommand) error {
	command.Path, command.Args, command.User, command.Cwd = agentPath, []string{subcommand}, "root", "/"
	stderr := make(chan RunOutput, 64)
	result, err := r.Run(ctx, owned, command, stderr)
	// Late output may still arrive after Run returns, so the channel is drained, not closed.
	var msg strings.Builder
drain:
	for msg.Len() < 4096 {
		select {
		case chunk := <-stderr:
			msg.Write(chunk.Data)
		default:
			break drain
		}
	}
	detail := strings.TrimSpace(msg.String())
	switch {
	case err != nil:
		return fmt.Errorf("%s: %w (%s)", subcommand, err, detail)
	case !result.ExitCodeKnown:
		return fmt.Errorf("%s: exit code unknown (%s)", subcommand, detail)
	case result.ExitCode != 0:
		return fmt.Errorf("%s: exit code %d (%s)", subcommand, result.ExitCode, detail)
	}
	return nil
}
