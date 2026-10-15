// Package cli is Studio's command line: short commands for scripts and for quick work without a
// browser, such as `studio ls`, `studio start api` or `studio ask api "run the tests"`
// (docs/cli.md). Like the TUI it is a client of the running Studio's HTTP API on its loopback
// address, signed in as the local owner; it adds no endpoints and never reads the catalog
// itself, except for the install key the sign-in derives its credential from.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/term"

	"github.com/lukaskoebe/sandbox-studio/internal/tui"
)

// Exit statuses. A failed command exits 1, a usage error 2 and Ctrl-C 130, as in v1, so scripts
// can tell them apart.
const (
	ExitOK          = 0
	ExitFailed      = 1
	ExitUsage       = 2
	ExitInterrupted = 130
)

// Options are what a command needs from its process.
type Options struct {
	Addr string
	// Connect signs in to the Studio at Addr as the local owner.
	Connect func(ctx context.Context, addr string) (*tui.Client, error)
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	// Interactive reports whether stdin and stdout are a terminal: confirmations are asked
	// there, and refused elsewhere unless --yes is given.
	Interactive bool
	// Server and Flags are main's own commands and flags, for the help.
	Server []ServerCommand
	Flags  *flag.FlagSet
}

// DefaultOptions are the options of the studio process.
func DefaultOptions(addr string, connect func(context.Context, string) (*tui.Client, error)) Options {
	return Options{
		Addr: addr, Connect: connect,
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
	}
}

// command is one subcommand. flags declares its flags on fs; run gets the arguments that remain.
type command struct {
	name    string
	args    string // the usage line after the name
	summary string
	// offline commands need no Studio.
	offline bool
	flags   func(fs *flag.FlagSet, c *call)
	run     func(c *call, args []string) error
}

// call is one run of a command: its options, its connection and the common flags.
type call struct {
	ctx    context.Context
	opts   Options
	client *tui.Client
	cmd    *command
	env    string // --env: an environment name or ID
	json   bool   // --json: print the API's JSON instead of text
	yes    bool   // --yes, for the commands that ask first
	envID  string // the resolved environment, set by environment()
	extra  any    // the command's own flags, set by its flags func
}

// usageError is a command used wrongly: it exits with ExitUsage and shows the command's usage.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

// failure is a command that failed with a status of its own, such as a turn that ended in error.
type failure struct {
	status int
	msg    string
}

func (e failure) Error() string { return e.msg }

// Commands reports whether name is a CLI subcommand, so main can tell them from the server's own.
func Commands(name string) bool {
	return slices.ContainsFunc(commands, func(c *command) bool { return c.name == name }) || name == "help" || name == completeCommand
}

// Run runs the subcommand in args[0] and returns the exit status.
func Run(ctx context.Context, opts Options, args []string) int {
	if len(args) == 0 || args[0] == "help" || IsHelp(args[0]) {
		topic := ""
		if len(args) > 1 {
			topic = args[1]
		}
		if !Help(opts.Stdout, topic, opts.Server, opts.Flags) {
			fmt.Fprintf(opts.Stderr, "studio: unknown command %q. Run `studio help` for the commands.\n", topic)
			return ExitUsage
		}
		return ExitOK
	}
	if args[0] == completeCommand {
		return complete(ctx, opts, args[1:])
	}
	cmd := lookup(args[0])
	if cmd == nil {
		fmt.Fprintf(opts.Stderr, "studio: unknown command %q. Run `studio help` for the commands.\n", args[0])
		return ExitUsage
	}
	c := &call{ctx: ctx, opts: opts, cmd: cmd}
	fs := flag.NewFlagSet("studio "+cmd.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if !cmd.offline {
		fs.StringVar(&c.env, "env", "", "the environment, by name or ID (default $STUDIO_ENV, else the first)")
		fs.BoolVar(&c.json, "json", false, "print JSON for scripts")
	}
	if cmd.flags != nil {
		cmd.flags(fs, c)
	}
	rest, err := parse(fs, args[1:])
	if errors.Is(err, flag.ErrHelp) {
		printUsage(opts.Stdout, cmd)
		return ExitOK
	}
	if err != nil {
		fmt.Fprintf(opts.Stderr, "studio %s: %v\n", cmd.name, err)
		printUsage(opts.Stderr, cmd)
		return ExitUsage
	}
	if c.env == "" && opts.Getenv != nil {
		c.env = opts.Getenv("STUDIO_ENV")
	}
	if !cmd.offline {
		client, err := opts.Connect(ctx, opts.Addr)
		if err != nil {
			fmt.Fprintf(opts.Stderr, "studio: %s\n", clean(err.Error()))
			return ExitFailed
		}
		c.client = client
		defer client.Close(context.WithoutCancel(ctx))
	}
	return c.status(cmd.run(c, rest))
}

// status prints err and returns the exit status it stands for.
func (c *call) status(err error) int {
	var ue usageError
	var f failure
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, context.Canceled) && c.ctx.Err() != nil:
		fmt.Fprintln(c.opts.Stderr, "studio: interrupted")
		return ExitInterrupted
	case errors.As(err, &ue):
		fmt.Fprintf(c.opts.Stderr, "studio %s: %s\n", c.cmd.name, ue.msg)
		printUsage(c.opts.Stderr, c.cmd)
		return ExitUsage
	case errors.As(err, &f):
		if f.msg != "" {
			fmt.Fprintf(c.opts.Stderr, "studio %s: %s\n", c.cmd.name, clean(f.msg))
		}
		return f.status
	default:
		fmt.Fprintf(c.opts.Stderr, "studio %s: %s\n", c.cmd.name, clean(err.Error()))
		return ExitFailed
	}
}

// parse parses flags wherever they are among the arguments, so `studio stop api --json` works
// like `studio stop --json api`. Everything after "--" is an argument.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil && !isBool(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return rest, nil
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// ServerCommand is one of the commands main runs itself, for the help.
type ServerCommand struct {
	Name, Args, Summary string
	// Detail is printed under the summary by "studio help <name>".
	Detail string
}

// IsHelp reports whether arg asks for help.
func IsHelp(arg string) bool { return arg == "-h" || arg == "-help" || arg == "--help" }

// Help prints the help for topic: "" is every command, else one command, a server command
// or a sandbox command. global holds studio's own flags, if known. It reports false for a
// topic that is no command.
func Help(w io.Writer, topic string, server []ServerCommand, global *flag.FlagSet) bool {
	if topic != "" {
		if cmd := lookup(topic); cmd != nil {
			printUsage(w, cmd)
			return true
		}
		for _, c := range server {
			if c.Name == topic {
				fmt.Fprintf(w, "Usage: studio %s\n\n%s\n", strings.TrimSpace(c.Name+" "+c.Args), c.Summary)
				if c.Detail != "" {
					fmt.Fprintf(w, "\n%s\n", c.Detail)
				}
				return true
			}
		}
		return false
	}
	fmt.Fprint(w, `Sandbox Studio runs coding agents in microVM sandboxes on this machine, with approvals for
what they reach and a browser of their own.

Usage:
  studio [flags]                       run Studio; open the login link it prints
  studio [flags] <command> [arguments]

`)
	if len(server) > 0 {
		fmt.Fprintln(w, "Studio itself:")
		for _, c := range server {
			fmt.Fprintf(w, "  %-12s %s\n", c.Name, c.Summary)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Sandboxes and approvals, in a running Studio:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-12s %s\n", c.name, c.summary)
	}
	if global != nil {
		fmt.Fprintln(w, "\nFlags:")
		out := global.Output()
		global.SetOutput(w)
		global.PrintDefaults()
		global.SetOutput(out)
	}
	fmt.Fprint(w, `
The sandbox commands take --env (or $STUDIO_ENV) and --json, and name sandboxes by name or
ID. Run "studio help <command>" or "studio <command> -h" for a command's arguments and flags.
Documentation: https://lukaskoebe.github.io/sandbox-studio/
`)
	return true
}

func printUsage(w io.Writer, cmd *command) {
	fmt.Fprintf(w, "Usage: studio %s %s\n\n%s\n", cmd.name, cmd.args, cmd.summary)
	fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
	c := &call{}
	if !cmd.offline {
		fs.StringVar(&c.env, "env", "", "the environment, by name or ID (default $STUDIO_ENV, else the first)")
		fs.BoolVar(&c.json, "json", false, "print JSON for scripts")
	}
	if cmd.flags != nil {
		cmd.flags(fs, c)
	}
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })
	if n > 0 {
		fmt.Fprintln(w, "\nFlags:")
		fs.SetOutput(w)
		fs.PrintDefaults()
	}
}

// printJSON prints v as indented JSON. JSON keeps strings as they are, escaped: scripts get the
// data, not terminal control sequences.
func (c *call) printJSON(v any) error {
	enc := json.NewEncoder(c.opts.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// clean makes text from Studio or a sandbox safe to print on a terminal: control characters,
// which could move the cursor or change the terminal, are shown as escapes. Newlines and tabs stay.
func clean(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isControl(r) {
			fmt.Fprintf(&b, "\\x%02x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isControl(r rune) bool { return r != '\n' && r != '\t' && unicode.IsControl(r) }
