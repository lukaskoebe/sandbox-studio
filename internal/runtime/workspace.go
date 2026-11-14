package runtime

import (
	"bytes"
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

// The copied trees: the workspace, the agent user's home, Docker's data, which only a disk
// resize copies, and the toolchains in /opt/mise, which a rebase that keeps tools merges into
// the new template's.
const (
	TreeWorkspace Tree = "workspace"
	TreeHome      Tree = "home"
	TreeDocker    Tree = "docker"
	TreeTools     Tree = "tools"
)

func (t Tree) known() bool {
	return t == TreeWorkspace || t == TreeHome || t == TreeDocker || t == TreeTools
}

// ExportOptions say how ExportTree and UploadTree archive a tree.
type ExportOptions struct {
	// Fast compresses with zstd, which both sides of a copy between VMs read; without it the
	// archive is gzip, as an export file stays for older Studios to import.
	Fast bool
	// ToolsHave names, for TreeTools, what the VM the archive goes to has already (ToolsHave),
	// which the archive leaves out.
	ToolsHave []string
}

// args and env are the export's command line and its environment beyond RunCommand's.
func (o ExportOptions) command(tree Tree) (args []string, env map[string]string) {
	args = []string{string(tree) + "-export"}
	if o.Fast {
		args = append(args, "--zstd")
	}
	if tree == TreeTools && len(o.ToolsHave) > 0 {
		if list := strings.Join(o.ToolsHave, "\n"); len(list) <= agentproto.MaxToolsHave {
			env = map[string]string{agentproto.ToolsHaveEnv: list}
		}
	}
	return args, env
}

// ExportTree streams the archive of a tree of a running VM to w, as opts says. It runs
// `studio-agent <tree>-export` as root. The guest agent comes from the host, so both sides of
// a fork read zstd.
func (r *Runtime) ExportTree(ctx context.Context, owned OwnedVM, tree Tree, opts ExportOptions, w io.Writer) error {
	if !tree.known() {
		return fmt.Errorf("unknown tree %q", tree)
	}
	args, env := opts.command(tree)
	return r.runAgentAs(ctx, owned, args, RunCommand{User: "root", Cwd: "/", Stdout: w, Env: env})
}

// UploadTree runs `studio-agent <tree>-export` as root in a running VM with the archive, in
// ExportTree's format, sent to url, Studio's transfer host, whose certificate caPEM's CA signs.
// A guest's writes to msb exec's stdout are slow; its TCP through the gateway isn't.
func (r *Runtime) UploadTree(ctx context.Context, owned OwnedVM, tree Tree, opts ExportOptions, url string, caPEM []byte) error {
	if !tree.known() {
		return fmt.Errorf("unknown tree %q", tree)
	}
	args, env := opts.command(tree)
	if env == nil {
		env = map[string]string{}
	}
	env[agentproto.TransferURLEnv], env[agentproto.TransferCAEnv] = url, string(caPEM)
	return r.runAgentAs(ctx, owned, args, RunCommand{User: "root", Cwd: "/", Env: env})
}

// ToolsHave names what a running VM has in /opt/mise that a tools import into it keeps of its
// own: each installs/<tool>/<version> and plugins/<name>, as `studio-agent tools-have` prints.
func (r *Runtime) ToolsHave(ctx context.Context, owned OwnedVM) ([]string, error) {
	var out limitedBuffer
	out.max = 4 * agentproto.MaxToolsHave
	if err := r.runAgent(ctx, owned, "tools-have", RunCommand{Stdout: &out}); err != nil {
		return nil, err
	}
	if out.over {
		return nil, nil
	}
	return strings.Fields(out.b.String()), nil
}

// limitedBuffer keeps up to max bytes and notes that more came.
type limitedBuffer struct {
	b    bytes.Buffer
	max  int
	over bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.b.Len()+len(p) > l.max {
		l.over = true
	} else {
		l.b.Write(p)
	}
	return len(p), nil
}

// ImportTree extracts an archive from rd into the empty tree of a running VM. It runs
// `studio-agent <tree>-import` as root. progress, if set, is told how far it got: the bytes of
// files written and the archive's total, 0 when it doesn't say.
func (r *Runtime) ImportTree(ctx context.Context, owned OwnedVM, tree Tree, rd io.Reader, progress func(copied, total int64)) error {
	if !tree.known() {
		return fmt.Errorf("unknown tree %q", tree)
	}
	// Always read, or the lines would go where runAgent looks for the error, which they'd crowd out.
	return r.runAgent(ctx, owned, string(tree)+"-import", RunCommand{Stdin: rd, Stdout: &copyLines{report: progress}})
}

// copyLines reads the agentproto.CopyProgressPrefix lines an import writes to its stdout and
// tells report, if set.
type copyLines struct {
	report  func(copied, total int64)
	partial []byte
}

func (c *copyLines) Write(p []byte) (int, error) {
	c.partial = append(c.partial, p...)
	for {
		i := bytes.IndexByte(c.partial, '\n')
		if i < 0 {
			break
		}
		line := string(c.partial[:i])
		c.partial = c.partial[i+1:]
		var copied, total int64
		if rest, ok := strings.CutPrefix(line, agentproto.CopyProgressPrefix); ok {
			if _, err := fmt.Sscan(rest, &copied, &total); err == nil && c.report != nil {
				c.report(copied, total)
			}
		}
	}
	if len(c.partial) > 4096 {
		c.partial = c.partial[:0] // not a progress line
	}
	return len(p), nil
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
