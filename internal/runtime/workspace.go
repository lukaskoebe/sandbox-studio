package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// HomePath is the agent user's home in the guest.
const HomePath = "/home/agent"

// Tree is a directory that fork, rebase and export copy between VMs.
type Tree string

// The copied trees: the workspace, and the agent user's home.
const (
	TreeWorkspace Tree = "workspace"
	TreeHome      Tree = "home"
)

// ExportTree streams the archive of a tree of a running VM to w. It runs
// `studio-agent <tree>-export` as root.
func (r *Runtime) ExportTree(ctx context.Context, owned OwnedVM, tree Tree, w io.Writer) error {
	if tree != TreeWorkspace && tree != TreeHome {
		return fmt.Errorf("unknown tree %q", tree)
	}
	return r.runAgent(ctx, owned, string(tree)+"-export", RunCommand{Stdout: w})
}

// ImportTree extracts an archive from rd into the empty tree of a running VM. It runs
// `studio-agent <tree>-import` as root.
func (r *Runtime) ImportTree(ctx context.Context, owned OwnedVM, tree Tree, rd io.Reader) error {
	if tree != TreeWorkspace && tree != TreeHome {
		return fmt.Errorf("unknown tree %q", tree)
	}
	return r.runAgent(ctx, owned, string(tree)+"-import", RunCommand{Stdin: rd})
}

func (r *Runtime) runAgent(ctx context.Context, owned OwnedVM, subcommand string, command RunCommand) error {
	command.Path, command.Args, command.User, command.Cwd = AgentPath, []string{subcommand}, "root", "/"
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
