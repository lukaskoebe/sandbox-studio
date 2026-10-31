package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// HomePath is the agent user's home in the guest.
const HomePath = "/home/agent"

// Tree is a directory that fork, rebase and export copy between VMs.
type Tree string

// The copied trees: the workspace, the agent user's home, and Docker's data, which only a
// disk resize copies.
const (
	TreeWorkspace Tree = "workspace"
	TreeHome      Tree = "home"
	TreeDocker    Tree = "docker"
)

func (t Tree) known() bool { return t == TreeWorkspace || t == TreeHome || t == TreeDocker }

// ExportTree streams the archive of a tree of a running VM to w, compressed with zstd when
// fast is set and with gzip otherwise. It runs `studio-agent <tree>-export` as root. The
// guest agent comes from the host, so both sides of a fork read zstd; an export file stays
// gzip, which older Studios import.
func (r *Runtime) ExportTree(ctx context.Context, owned OwnedVM, tree Tree, fast bool, w io.Writer) error {
	if !tree.known() {
		return fmt.Errorf("unknown tree %q", tree)
	}
	args := []string{string(tree) + "-export"}
	if fast {
		args = append(args, "--zstd")
	}
	return r.runAgentAs(ctx, owned, args, RunCommand{User: "root", Cwd: "/", Stdout: w})
}

// UploadTree runs `studio-agent <tree>-export --zstd` as root in a running VM with the archive
// sent to url, Studio's transfer host, whose certificate caPEM's CA signs. A guest's writes to
// msb exec's stdout are slow; its TCP through the gateway isn't.
func (r *Runtime) UploadTree(ctx context.Context, owned OwnedVM, tree Tree, url string, caPEM []byte) error {
	if !tree.known() {
		return fmt.Errorf("unknown tree %q", tree)
	}
	return r.runAgentAs(ctx, owned, []string{string(tree) + "-export", "--zstd"}, RunCommand{User: "root", Cwd: "/",
		Env: map[string]string{agentproto.TransferURLEnv: url, agentproto.TransferCAEnv: string(caPEM)}})
}

// ImportTree extracts an archive from rd into the empty tree of a running VM. It runs
// `studio-agent <tree>-import` as root.
func (r *Runtime) ImportTree(ctx context.Context, owned OwnedVM, tree Tree, rd io.Reader) error {
	if !tree.known() {
		return fmt.Errorf("unknown tree %q", tree)
	}
	return r.runAgent(ctx, owned, string(tree)+"-import", RunCommand{Stdin: rd})
}

// RunAgentCommand runs `studio-agent <args>` in a running VM as the agent user, in its home,
// with stdin and stdout streamed. A failure carries the start of its stderr.
func (r *Runtime) RunAgentCommand(ctx context.Context, owned OwnedVM, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("no studio-agent command")
	}
	return r.runAgentAs(ctx, owned, args, RunCommand{
		User: "agent", Cwd: HomePath, Stdin: stdin, Stdout: stdout,
		Env: map[string]string{"HOME": HomePath, "USER": "agent", "LOGNAME": "agent",
			"PATH": "/home/agent/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
	})
}

func (r *Runtime) runAgent(ctx context.Context, owned OwnedVM, subcommand string, command RunCommand) error {
	command.User, command.Cwd = "root", "/"
	return r.runAgentAs(ctx, owned, []string{subcommand}, command)
}

func (r *Runtime) runAgentAs(ctx context.Context, owned OwnedVM, args []string, command RunCommand) error {
	subcommand := args[0]
	command.Path, command.Args = AgentPath, args
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
